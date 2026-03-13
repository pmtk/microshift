package c2cc

/*
 * TODO:
 * - CR(D) for reporting back the status (Routes created, Latency)
 * - Consider moving `close(ready)` after 1st successful reconcile? Might prolong svc readiness greatly...
 * - Make reconcile attempts after starting ushift much faster (like every 10sec or so), then slow down to 1 a minute?
 * - Dual Stack & IPv6
 */

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/openshift/microshift/pkg/config"
	"github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/model"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

const (
	ovnNBSocket          = "/var/run/ovn/ovnnb_db.sock"
	ownerControllerValue = "microshift-c2cc"
	ownerControllerKey   = "k8s.ovn.org/owner-controller"
	policyDstIP          = "dst-ip"
	reconcileInterval    = 60 * time.Second
)

// buildNamedUUID creates an OVSDB-safe named UUID by replacing characters
// that are invalid in <id> (RFC 7047) with underscores.
func buildNamedUUID(prefix, suffix string) string {
	r := strings.NewReplacer(".", "_", "/", "_", ":", "_", "-", "_")
	return r.Replace(prefix + suffix)
}

// C2CCRouteManager is a MicroShift controller that sets up cross-cluster
// networking by configuring OVN static routes and Linux underlay routes
// for each remote cluster. It periodically reconciles the desired state
// to recover from external changes (OVN restarts, etc.).
type C2CCRouteManager struct {
	nodeName       string
	remoteClusters []config.RemoteCluster
}

func NewC2CCRouteManager(cfg *config.Config) *C2CCRouteManager {
	return &C2CCRouteManager{
		nodeName:       cfg.CanonicalNodeName(),
		remoteClusters: cfg.C2CC.RemoteClusters,
	}
}

func (c *C2CCRouteManager) Name() string           { return "c2cc-route-manager" }
func (c *C2CCRouteManager) Dependencies() []string { return []string{"kube-apiserver"} }

func (c *C2CCRouteManager) Run(ctx context.Context, ready chan<- struct{}, stopped chan<- struct{}) error {
	defer close(stopped)

	if len(c.remoteClusters) == 0 {
		klog.Infof("%s is disabled (no remote clusters configured)", c.Name())
		close(ready)
		return ctx.Err()
	}

	klog.Infof("%s starting: configuring routes for %d remote cluster(s), reconcile interval: %s", c.Name(), len(c.remoteClusters), reconcileInterval)
	close(ready)

	// Reconciliation loop: OVN infrastructure (GR_<node>) may not exist yet
	// at startup, so we keep retrying until it succeeds and then periodically
	// re-check to recover from external changes (OVN restarts, etc.).
	wait.UntilWithContext(ctx, func(ctx context.Context) {
		if err := c.reconcile(ctx); err != nil {
			klog.Errorf("%s reconciliation failed (will retry): %v", c.Name(), err)
		}
	}, reconcileInterval)

	return ctx.Err()
}

// reconcile ensures OVN routes and Linux routes are all in the desired state.
// It is safe to call repeatedly — each step is idempotent.
func (c *C2CCRouteManager) reconcile(ctx context.Context) error {
	if err := c.reconcileOVNRoutes(ctx); err != nil {
		return fmt.Errorf("reconciling OVN routes: %w", err)
	}

	if err := c.reconcileLinuxRoutes(); err != nil {
		return fmt.Errorf("reconciling Linux routes: %w", err)
	}

	return nil
}

// reconcileOVNRoutes connects to the OVN NB DB and ensures static routes
// exist on the Gateway Router for each remote cluster's pod and service CIDRs.
func (c *C2CCRouteManager) reconcileOVNRoutes(ctx context.Context) error {
	nbClient, err := c.connectToNBDB(ctx)
	if err != nil {
		return fmt.Errorf("connecting to OVN NB DB: %w", err)
	}
	defer nbClient.Close()

	grName := "GR_" + c.nodeName
	outport := "rtoe-" + grName

	for i, rc := range c.remoteClusters {
		for _, cidr := range []string{rc.ClusterNetwork, rc.ServiceNetwork} {
			if err := c.ensureOVNStaticRoute(ctx, nbClient, grName, cidr, rc.NextHop, outport); err != nil {
				return fmt.Errorf("ensuring OVN route for remote cluster %d (%s -> %s): %w", i, cidr, rc.NextHop, err)
			}
		}
	}

	return nil
}

// connectToNBDB waits for the OVN NB DB socket to appear and connects via libovsdb.
func (c *C2CCRouteManager) connectToNBDB(ctx context.Context) (client.Client, error) {
	if err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := os.Stat(ovnNBSocket)
		return err == nil, nil
	}); err != nil {
		return nil, fmt.Errorf("timed out waiting for OVN NB DB socket at %s: %w", ovnNBSocket, err)
	}

	dbModel, err := nbdbModel()
	if err != nil {
		return nil, fmt.Errorf("creating NB DB model: %w", err)
	}

	dbModel.SetIndexes(map[string][]model.ClientIndex{
		logicalRouterTable: {{Columns: []model.ColumnKey{{Column: "name"}}}},
	})

	endpoint := "unix:" + ovnNBSocket
	nbClient, err := client.NewOVSDBClient(dbModel, client.WithEndpoint(endpoint))
	if err != nil {
		return nil, fmt.Errorf("creating OVSDB client: %w", err)
	}

	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := nbClient.Connect(connectCtx); err != nil {
		return nil, fmt.Errorf("connecting to OVN NB DB at %s: %w", endpoint, err)
	}

	monitorCtx, monitorCancel := context.WithTimeout(ctx, 30*time.Second)
	defer monitorCancel()

	_, err = nbClient.Monitor(monitorCtx,
		nbClient.NewMonitor(
			client.WithTable(&LogicalRouter{}),
			client.WithTable(&LogicalRouterStaticRoute{}),
		),
	)
	if err != nil {
		nbClient.Close()
		return nil, fmt.Errorf("monitoring OVN NB DB tables: %w", err)
	}

	klog.V(4).Infof("%s connected to OVN NB DB", c.Name())
	return nbClient, nil
}

// ensureOVNStaticRoute creates a static route on the given logical router if
// one with the same prefix and nexthop does not already exist.
func (c *C2CCRouteManager) ensureOVNStaticRoute(ctx context.Context, nbClient client.Client, routerName, prefix, nexthop, outport string) error {
	// Use Where().List() instead of Get() because the Logical_Router table
	// has no schema-level indexes; Get() only checks schema indexes while
	// Where().List() also checks client indexes (which we set on "name").
	var routers []LogicalRouter
	if err := nbClient.Where(&LogicalRouter{Name: routerName}).List(ctx, &routers); err != nil {
		return fmt.Errorf("finding logical router %s: %w", routerName, err)
	}
	if len(routers) == 0 {
		return fmt.Errorf("finding logical router %s: not found", routerName)
	}
	router := &routers[0]

	// Check if the route already exists on this router.
	for _, routeUUID := range router.StaticRoutes {
		route := &LogicalRouterStaticRoute{UUID: routeUUID}
		if err := nbClient.Get(ctx, route); err != nil {
			klog.V(4).Infof("%s could not get route %s: %v", c.Name(), routeUUID, err)
			continue
		}
		if route.IPPrefix == prefix && route.Nexthop == nexthop {
			klog.V(4).Infof("%s OVN route already exists: %s -> %s on %s", c.Name(), prefix, nexthop, routerName)
			return nil
		}
	}

	// Create the new static route.
	policy := policyDstIP
	route := &LogicalRouterStaticRoute{
		UUID:       buildNamedUUID("c2cc-route-", prefix),
		IPPrefix:   prefix,
		Nexthop:    nexthop,
		OutputPort: &outport,
		Policy:     &policy,
		ExternalIDs: map[string]string{
			ownerControllerKey: ownerControllerValue,
		},
	}

	createOps, err := nbClient.Create(route)
	if err != nil {
		return fmt.Errorf("building create ops for route %s: %w", prefix, err)
	}

	// Mutate the router to include the new route's UUID in its StaticRoutes.
	mutateOps, err := nbClient.Where(router).Mutate(router, model.Mutation{
		Field:   &router.StaticRoutes,
		Mutator: ovsdb.MutateOperationInsert,
		Value:   []string{route.UUID},
	})
	if err != nil {
		return fmt.Errorf("building mutate ops for router %s: %w", routerName, err)
	}

	ops := append(createOps, mutateOps...)
	txnCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	results, err := nbClient.Transact(txnCtx, ops...)
	if err != nil {
		return fmt.Errorf("transacting OVN route creation: %w", err)
	}
	for _, r := range results {
		if r.Error != "" {
			return fmt.Errorf("OVN transaction error: %s: %s", r.Error, r.Details)
		}
	}

	klog.Infof("%s added OVN route: %s -> %s via %s on %s", c.Name(), prefix, nexthop, outport, routerName)
	return nil
}

// reconcileLinuxRoutes ensures Linux routing table entries exist for each
// remote cluster's pod and service CIDRs.
func (c *C2CCRouteManager) reconcileLinuxRoutes() error {
	for _, rc := range c.remoteClusters {
		gw := net.ParseIP(rc.NextHop)
		if gw == nil {
			return fmt.Errorf("invalid remote next hop IP: %s", rc.NextHop)
		}

		for _, cidr := range []string{rc.ClusterNetwork, rc.ServiceNetwork} {
			if err := c.ensureLinuxRoute(cidr, gw); err != nil {
				return fmt.Errorf("ensuring Linux route for %s via %s: %w", cidr, rc.NextHop, err)
			}
		}
	}
	return nil
}

func (c *C2CCRouteManager) ensureLinuxRoute(cidr string, gw net.IP) error {
	_, dst, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %s: %w", cidr, err)
	}

	existingRoutes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{Dst: dst}, netlink.RT_FILTER_DST)
	if err != nil {
		return fmt.Errorf("listing routes for %s: %w", cidr, err)
	}

	for _, r := range existingRoutes {
		if r.Gw.Equal(gw) {
			klog.V(4).Infof("%s Linux route already exists: %s via %s", c.Name(), cidr, gw)
			return nil
		}
	}

	route := &netlink.Route{
		Dst: dst,
		Gw:  gw,
	}
	if err := netlink.RouteAdd(route); err != nil {
		return fmt.Errorf("adding route %s via %s: %w", cidr, gw, err)
	}

	klog.Infof("%s added Linux route: %s via %s", c.Name(), cidr, gw)
	return nil
}
