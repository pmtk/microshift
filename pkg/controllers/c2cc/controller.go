package c2cc

/*
TODO:
- Healthcheck/reachability of the other clusters
- CR(D) for reporting back the status (Routes created, Latency)
- Make reconcile attempts after starting microshift much faster (like every 10sec or so), then slow down to 1 a minute? Or 10/30 seconds?
  Should there be a limit to the initial attempts which exceeding would cause microshift to exit(1)? Or CR is enough for reporting status?
- Support Dual Stack & IPv6
- Cleanup the routes - but not on microshift shutdown/restart. Maybe on start and align with the configuration.
  - Kernel routes: handled via dedicated table 200 with protocol tagging
  - OVNK: scan GR and remove old routes with our ExternalID
- Open NBDB connection once and reuse instead of reopening each time.
- DNS: Setup forwarding per configured cluster (more than 1). Allow for configurable domains.
- DNS: Don't assume cluster.local (both local and remote) for the 'rewrite name regex'?
  It cannot be changed right now and we know MicroShift should be on the other side, so it's fine for now?
- Document: firewall requirements (scoped zone for remote pod/service CIDRs) and optional IPsec (transport mode, pod/service CIDRs).
- Maybe check if ovnkube-master Pod (nbdb container) is ready before reconcile to avoid hitting dead socket?
- Consider using ovn-kubernetes/go-controller/pkg/nbdb/ instead of nbdb.go to avoid unexpected schema changes
 */

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/openshift/microshift/pkg/config"
	"github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/model"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/vishvananda/netlink"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"sigs.k8s.io/knftables"
)

const (
	ovnNBSocket          = "/var/run/ovn/ovnnb_db.sock"
	ownerControllerValue = "microshift-c2cc"
	ownerControllerKey   = "k8s.ovn.org/owner-controller"
	policyDstIP          = "dst-ip"
	reconcileInterval    = 15 * time.Second

	// Dedicated Linux routing table and ip rule for C2CC routes,
	// isolating them from the main table and other route sources.
	c2ccRouteTable   = 200
	c2ccRouteProto   = 200
	c2ccRulePriority = 100

	// Dedicated table and rule for routing inbound cross-cluster
	// service traffic via the management port (ovn-k8s-mp0) instead
	// of br-ex. This avoids the GR's load balancer implicit SNAT
	// which rewrites the source to the join switch IP (100.64.x.x).
	// The node switch LB handles DNAT without SNAT, preserving
	// the original pod source IP.
	c2ccSvcRouteTable   = 201
	c2ccSvcRulePriority = 99

	// Management port interface and gateway IP.
	mgmtPortIface = "ovn-k8s-mp0"

	// OVN-K annotation that tells ovn-kubernetes to exclude these subnets
	// from nftables masquerading. This is the cooperative API used by
	// submariner and other consumers to avoid fighting OVN-K's reconciler.
	// This controls the mgmtport-no-snat-subnets-v4 nft set which exempts
	// inbound cross-cluster traffic from management port SNAT.
	ovnNodeDontSNATSubnets = "k8s.ovn.org/node-ingress-snat-exclude-subnets"

	// nftables table and chain managed by OVN-K that masquerades outbound
	// pod traffic. We insert destination-based exclusions so cross-cluster
	// traffic preserves the original pod source IP.
	nftOVNTable          = "inet ovn-kubernetes"
	nftPodSubnetMasqChain = "ovn-kube-pod-subnet-masq"

	// Comment tag used to identify C2CC-managed nftables rules.
	nftC2CCComment = "c2cc-no-masq"
)

// buildNamedUUID creates an OVSDB-safe named UUID by replacing characters
// that are invalid in <id> (RFC 7047) with underscores.
func buildNamedUUID(prefix, suffix string) string {
	r := strings.NewReplacer(".", "_", "/", "_", ":", "_", "-", "_")
	return r.Replace(prefix + suffix)
}

// buildSNATExcludeMatch builds an OVN match expression that prevents
// SNAT for traffic destined to remote cluster CIDRs. When set on the
// GR's per-pod SNAT entries, this preserves the original pod source IP
// for cross-cluster traffic while leaving regular outbound SNAT intact.
func (c *C2CCRouteManager) buildSNATExcludeMatch() string {
	var conditions []string
	for _, rc := range c.remoteClusters {
		for _, cidr := range []string{rc.ClusterNetwork, rc.ServiceNetwork} {
			conditions = append(conditions, fmt.Sprintf("ip4.dst != %s", cidr))
		}
	}
	if len(conditions) == 0 {
		return ""
	}
	sort.Strings(conditions)
	return strings.Join(conditions, " && ")
}

// routeSpec describes a desired Linux route entry.
type routeSpec struct {
	dst *net.IPNet
	gw  net.IP
}

// C2CCRouteManager is a MicroShift controller that sets up cross-cluster
// networking by configuring OVN static routes, SNAT exemptions, and Linux
// underlay routes for each remote cluster. All OVN changes are applied in
// a single atomic OVSDB transaction to avoid half-configured states.
type C2CCRouteManager struct {
	nodeName          string
	kubeconfig        string
	remoteClusters    []config.RemoteCluster
	localServiceCIDRs []string
	kubeClient        kubernetes.Interface
}

func NewC2CCRouteManager(cfg *config.Config) *C2CCRouteManager {
	return &C2CCRouteManager{
		nodeName:         cfg.CanonicalNodeName(),
		kubeconfig:       cfg.KubeConfigPath(config.KubeAdmin),
		remoteClusters:   cfg.C2CC.RemoteClusters,
		localServiceCIDRs: cfg.Network.ServiceNetwork,
	}
}

func (c *C2CCRouteManager) Name() string           { return "c2cc-route-manager" }
func (c *C2CCRouteManager) Dependencies() []string {
	// Ideally we'd wait for OVN-K master Pod, but:
	// 1. It's not possible using ServiceManager framework.
	// 2. We don't want to hold microshift.service readiness waiting for Pod
	//    (also it would behave too differently with C2CC enabled and disabled).
	return []string{"kubelet"}
}

func (c *C2CCRouteManager) Run(ctx context.Context, ready chan<- struct{}, stopped chan<- struct{}) error {
	defer close(stopped)

	if len(c.remoteClusters) == 0 {
		klog.Infof("%s is disabled (no remote clusters configured)", c.Name())
		close(ready)
		return ctx.Err()
	}

	klog.Infof("%s starting: configuring routes for %d remote cluster(s), reconcile interval: %s", c.Name(), len(c.remoteClusters), reconcileInterval)

	// Ideally, this would be after the first successful reconcile,
	// but that would mean holding microshift.service readiness
	// and potentially breaking startup flow.
	// Real readiness information will be in the CR.
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

// reconcile ensures all cross-cluster networking state is correct.
// OVN routes and SNAT exemptions are applied in a single atomic
// OVSDB transaction — either all OVN state is applied or none is.
func (c *C2CCRouteManager) reconcile(ctx context.Context) error {
	nbClient, err := c.connectToNBDB(ctx)
	if err != nil {
		return fmt.Errorf("connecting to OVN NB DB: %w", err)
	}
	defer nbClient.Close()

	if err := c.reconcileOVN(ctx, nbClient); err != nil {
		return fmt.Errorf("reconciling OVN state: %w", err)
	}

	if err := c.reconcileNoSNATAnnotation(ctx); err != nil {
		return fmt.Errorf("reconciling no-SNAT annotation: %w", err)
	}

	if err := c.reconcileLinuxRoutes(); err != nil {
		return fmt.Errorf("reconciling Linux routes: %w", err)
	}

	if err := c.reconcileServiceRoutes(); err != nil {
		return fmt.Errorf("reconciling service routes: %w", err)
	}

	if err := c.reconcileNftables(); err != nil {
		return fmt.Errorf("reconciling nftables: %w", err)
	}

	return nil
}

// reconcileOVN applies all OVN changes (static routes + SNAT match
// modifications) in a single OVSDB transaction. If the transaction
// fails, no partial state is left behind.
func (c *C2CCRouteManager) reconcileOVN(ctx context.Context, nbClient client.Client) error {
	grName := "GR_" + c.nodeName
	outport := "rtoe-" + grName

	var routers []LogicalRouter
	if err := nbClient.Where(&LogicalRouter{Name: grName}).List(ctx, &routers); err != nil {
		return fmt.Errorf("finding logical router %s: %w", grName, err)
	}
	if len(routers) == 0 {
		return fmt.Errorf("logical router %s not found", grName)
	}
	router := &routers[0]

	var allOps []ovsdb.Operation

	// --- Static routes ---
	// Index existing routes by prefix+nexthop to detect what already exists.
	type routeKey struct{ prefix, nexthop string }
	existingRoutes := make(map[routeKey]bool)
	for _, routeUUID := range router.StaticRoutes {
		route := &LogicalRouterStaticRoute{UUID: routeUUID}
		if err := nbClient.Get(ctx, route); err != nil {
			klog.V(4).Infof("%s could not get route %s: %v", c.Name(), routeUUID, err)
			continue
		}
		existingRoutes[routeKey{route.IPPrefix, route.Nexthop}] = true
	}

	for _, rc := range c.remoteClusters {
		for _, cidr := range []string{rc.ClusterNetwork, rc.ServiceNetwork} {
			if existingRoutes[routeKey{cidr, rc.NextHop}] {
				continue
			}

			policy := policyDstIP
			route := &LogicalRouterStaticRoute{
				UUID:       buildNamedUUID("c2cc-route-", cidr),
				IPPrefix:   cidr,
				Nexthop:    rc.NextHop,
				OutputPort: &outport,
				Policy:     &policy,
				ExternalIDs: map[string]string{
					ownerControllerKey: ownerControllerValue,
				},
			}

			createOps, err := nbClient.Create(route)
			if err != nil {
				return fmt.Errorf("building create ops for route %s: %w", cidr, err)
			}
			allOps = append(allOps, createOps...)

			mutateOps, err := nbClient.Where(router).Mutate(router, model.Mutation{
				Field:   &router.StaticRoutes,
				Mutator: ovsdb.MutateOperationInsert,
				Value:   []string{route.UUID},
			})
			if err != nil {
				return fmt.Errorf("building mutate ops for router %s: %w", grName, err)
			}
			allOps = append(allOps, mutateOps...)
		}
	}

	// --- SNAT exclusions ---
	// Modify existing SNAT rules on the GR to skip masquerading for
	// C2CC destination CIDRs. Without this, the GR rewrites the pod
	// source IP to its external IP (e.g. 10.44.0.0) for all outbound
	// traffic, destroying the original pod source IP for cross-cluster
	// communication. OVN-K may reset these match fields on reconcile,
	// but this controller re-applies them every cycle.
	desiredMatch := c.buildSNATExcludeMatch()
	for _, natUUID := range router.Nat {
		natEntry := &NAT{UUID: natUUID}
		if err := nbClient.Get(ctx, natEntry); err != nil {
			klog.V(4).Infof("%s could not get NAT %s: %v", c.Name(), natUUID, err)
			continue
		}
		if natEntry.Type != natTypeSNAT {
			continue
		}
		if natEntry.Match == desiredMatch {
			continue
		}

		natEntry.Match = desiredMatch
		updateOps, err := nbClient.Where(natEntry).Update(natEntry, &natEntry.Match)
		if err != nil {
			return fmt.Errorf("building update ops for SNAT %s (logical_ip=%s): %w", natUUID, natEntry.LogicalIP, err)
		}
		allOps = append(allOps, updateOps...)
	}

	// --- Single atomic transaction ---
	if len(allOps) == 0 {
		klog.V(4).Infof("%s OVN state is up to date", c.Name())
		return nil
	}

	txnCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	results, err := nbClient.Transact(txnCtx, allOps...)
	if err != nil {
		return fmt.Errorf("transacting OVN state: %w", err)
	}
	for _, r := range results {
		if r.Error != "" {
			return fmt.Errorf("OVN transaction error: %s: %s", r.Error, r.Details)
		}
	}

	klog.Infof("%s applied %d OVN operations in single transaction", c.Name(), len(allOps))
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
			client.WithTable(&NAT{}),
		),
	)
	if err != nil {
		nbClient.Close()
		return nil, fmt.Errorf("monitoring OVN NB DB tables: %w", err)
	}

	klog.V(4).Infof("%s connected to OVN NB DB", c.Name())
	return nbClient, nil
}

// reconcileNoSNATAnnotation sets the OVN-K node annotation that tells
// ovn-kubernetes to exclude remote cluster CIDRs from nftables masquerading.
// This is the cooperative API (used by submariner) — OVN-K reads it and
// configures its own SNAT exclusions, eliminating the reconciler fight
// that occurs when modifying SNAT match expressions directly.
func (c *C2CCRouteManager) getKubeClient() (kubernetes.Interface, error) {
	if c.kubeClient != nil {
		return c.kubeClient, nil
	}
	restCfg, err := clientcmd.BuildConfigFromFlags("", c.kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("building rest config: %w", err)
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("creating kube client: %w", err)
	}
	c.kubeClient = client
	return c.kubeClient, nil
}

func (c *C2CCRouteManager) reconcileNoSNATAnnotation(ctx context.Context) error {
	kubeClient, err := c.getKubeClient()
	if err != nil {
		return err
	}

	node, err := kubeClient.CoreV1().Nodes().Get(ctx, c.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting node %s: %w", c.nodeName, err)
	}

	var subnets []string
	for _, rc := range c.remoteClusters {
		subnets = append(subnets, rc.ClusterNetwork, rc.ServiceNetwork)
	}
	sort.Strings(subnets)

	desiredJSON, err := json.Marshal(subnets)
	if err != nil {
		return fmt.Errorf("marshaling subnets: %w", err)
	}
	desired := string(desiredJSON)

	if node.Annotations[ovnNodeDontSNATSubnets] == desired {
		klog.V(4).Infof("%s no-SNAT annotation already correct", c.Name())
		return nil
	}

	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Annotations[ovnNodeDontSNATSubnets] = desired

	if _, err := kubeClient.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating no-SNAT annotation on node %s: %w", c.nodeName, err)
	}

	klog.Infof("%s updated no-SNAT annotation on node %s: %s", c.Name(), c.nodeName, desired)
	return nil
}

// reconcileLinuxRoutes ensures Linux routing table entries and
// destination-scoped ip rules exist in a dedicated table for each
// remote cluster's pod and service CIDRs. Only routes and rules that
// differ from the desired state are added or removed.
func (c *C2CCRouteManager) reconcileLinuxRoutes() error {
	// Build desired state.
	desired := make(map[string]routeSpec)
	for _, rc := range c.remoteClusters {
		gw := net.ParseIP(rc.NextHop)
		if gw == nil {
			return fmt.Errorf("invalid remote next hop IP: %s", rc.NextHop)
		}

		for _, cidr := range []string{rc.ClusterNetwork, rc.ServiceNetwork} {
			_, dst, err := net.ParseCIDR(cidr)
			if err != nil {
				return fmt.Errorf("parsing CIDR %s: %w", cidr, err)
			}
			desired[dst.String()] = routeSpec{dst: dst, gw: gw}
		}
	}

	// --- IP rules: "to <cidr> lookup table 200" scoped per destination ---
	if err := c.reconcileIPRules(desired); err != nil {
		return fmt.Errorf("reconciling ip rules: %w", err)
	}

	// --- Routes in table 200 ---
	// List existing routes in our dedicated table.
	existing, err := netlink.RouteListFiltered(netlink.FAMILY_ALL,
		&netlink.Route{Table: c2ccRouteTable}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return fmt.Errorf("listing routes in table %d: %w", c2ccRouteTable, err)
	}

	// Compare existing vs desired: remove stale, skip matching.
	for i := range existing {
		r := &existing[i]
		if r.Dst == nil {
			continue
		}
		key := r.Dst.String()
		if d, ok := desired[key]; ok && r.Gw.Equal(d.gw) {
			// Already correct — don't touch it.
			delete(desired, key)
		} else {
			// Stale or wrong gateway — remove.
			if err := netlink.RouteDel(r); err != nil {
				return fmt.Errorf("deleting stale route %s: %w", key, err)
			}
			klog.Infof("%s removed stale Linux route: %s (table %d)", c.Name(), key, c2ccRouteTable)
		}
	}

	// Add missing routes.
	for _, spec := range desired {
		route := &netlink.Route{
			Dst:      spec.dst,
			Gw:       spec.gw,
			Table:    c2ccRouteTable,
			Protocol: c2ccRouteProto,
		}
		if err := netlink.RouteAdd(route); err != nil {
			return fmt.Errorf("adding route %s via %s to table %d: %w", spec.dst, spec.gw, c2ccRouteTable, err)
		}
		klog.Infof("%s added Linux route: %s via %s (table %d)", c.Name(), spec.dst, spec.gw, c2ccRouteTable)
	}

	return nil
}

// reconcileNftables inserts destination-based return rules into
// OVN-K's ovn-kube-pod-subnet-masq nftables chain. Without these
// rules, OVN-K masquerades ALL outbound pod traffic (rewrites pod
// source IP to the node's underlay IP). For cross-cluster traffic
// this destroys the original pod source IP. Inserting "ip daddr
// <remote CIDR> return" at the top of the chain skips masquerade
// for traffic destined to remote clusters, preserving source IPs
// end-to-end.
//
// These rules are re-checked every reconcile cycle because OVN-K
// may recreate the chain on restart.
func (c *C2CCRouteManager) reconcileNftables() error {
	nft, err := knftables.New(knftables.InetFamily, "ovn-kubernetes")
	if err != nil {
		return fmt.Errorf("creating knftables interface: %w", err)
	}

	// List existing rules to detect what we own.
	existingRules, err := nft.ListRules(context.TODO(), nftPodSubnetMasqChain)
	if err != nil {
		return fmt.Errorf("listing rules in chain %s: %w", nftPodSubnetMasqChain, err)
	}

	// Build the set of desired CIDRs.
	desiredCIDRs := make(map[string]bool)
	for _, rc := range c.remoteClusters {
		for _, cidr := range []string{rc.ClusterNetwork, rc.ServiceNetwork} {
			desiredCIDRs[cidr] = true
		}
	}

	// Count existing C2CC rules. ListRules only returns Handle and
	// Comment (not the rule text), so we identify our rules by comment
	// and compare the count against the desired set.
	var existingC2CCHandles []int
	for _, r := range existingRules {
		if r.Comment != nil && *r.Comment == nftC2CCComment && r.Handle != nil {
			existingC2CCHandles = append(existingC2CCHandles, *r.Handle)
		}
	}

	// If the count matches, rules are up to date (same CIDRs, already
	// inserted at the top). Skip the delete+re-insert cycle.
	if len(existingC2CCHandles) == len(desiredCIDRs) {
		klog.V(4).Infof("%s nftables masquerade bypass rules are up to date (%d rules)", c.Name(), len(desiredCIDRs))
		return nil
	}

	// Otherwise, delete all existing C2CC rules and re-insert the
	// correct set at the top of the chain. This handles both stale
	// rules (CIDR changes) and ordering issues (OVN-K may recreate
	// the chain with its masquerade rule at the top).
	tx := nft.NewTransaction()

	for _, handle := range existingC2CCHandles {
		tx.Delete(&knftables.Rule{
			Chain:  nftPodSubnetMasqChain,
			Handle: knftables.PtrTo(handle),
		})
	}

	for cidr := range desiredCIDRs {
		tx.Insert(&knftables.Rule{
			Chain:   nftPodSubnetMasqChain,
			Rule:    knftables.Concat("ip", "daddr", cidr, "return"),
			Comment: knftables.PtrTo(nftC2CCComment),
		})
	}

	if err := nft.Run(context.TODO(), tx); err != nil {
		return fmt.Errorf("applying nft masquerade bypass rules: %w", err)
	}
	klog.Infof("%s reconciled %d nft masquerade bypass rule(s) (deleted %d stale)", c.Name(), len(desiredCIDRs), len(existingC2CCHandles))
	return nil
}

// reconcileServiceRoutes ensures inbound cross-cluster service traffic
// enters OVN via the management port (ovn-k8s-mp0) instead of br-ex.
//
// When service traffic enters via br-ex → GR, the GR's load balancer
// does DNAT (service IP → pod IP) but also implicit SNAT (source →
// 100.64.x.x join IP) to ensure return traffic traverses the GR for
// de-DNAT. This destroys the original pod source IP.
//
// Routing the traffic via ovn-k8s-mp0 instead makes it enter OVN at
// the node switch level, where the same load balancer does DNAT only
// (switches don't SNAT). The source IP is preserved and return traffic
// routes correctly via ovn_cluster_router's default route → GR →
// static routes → underlay.
//
// Implementation: per-remote-cluster ip rules match traffic from remote
// pod CIDRs destined for local service CIDRs, directing it to table 201
// which routes local service CIDRs via ovn-k8s-mp0.
func (c *C2CCRouteManager) reconcileServiceRoutes() error {
	// Resolve the management port's gateway IP (ovn_cluster_router port
	// on the node's logical switch). We find it from the existing kernel
	// route for the local service CIDR.
	mgmtLink, err := netlink.LinkByName(mgmtPortIface)
	if err != nil {
		return fmt.Errorf("finding %s interface: %w", mgmtPortIface, err)
	}

	// Get the gateway IP from the management port's addresses —
	// it's the .1 address in the same subnet.
	addrs, err := netlink.AddrList(mgmtLink, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("listing addresses on %s: %w", mgmtPortIface, err)
	}

	var mgmtGatewayV4, mgmtGatewayV6 net.IP
	for _, addr := range addrs {
		if addr.IP.To4() != nil {
			gw := make(net.IP, len(addr.IP.To4()))
			copy(gw, addr.IP.To4())
			gw[len(gw)-1] = 1 // .1 is ovn_cluster_router's port
			mgmtGatewayV4 = gw
		} else if addr.IP.To16() != nil {
			gw := make(net.IP, len(addr.IP.To16()))
			copy(gw, addr.IP.To16())
			gw[len(gw)-1] = 1
			mgmtGatewayV6 = gw
		}
	}

	// --- Ip rules: from <remote_cluster_cidr> to <local_svc_cidr> lookup table 201 ---
	existingRules, err := netlink.RuleList(netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("listing ip rules: %w", err)
	}

	type ruleKey struct{ src, dst string }
	existingC2CCRules := make(map[ruleKey]bool)
	for _, r := range existingRules {
		if r.Priority == c2ccSvcRulePriority && r.Table == c2ccSvcRouteTable {
			key := ruleKey{}
			if r.Src != nil {
				key.src = r.Src.String()
			}
			if r.Dst != nil {
				key.dst = r.Dst.String()
			}
			existingC2CCRules[key] = true
		}
	}

	for _, rc := range c.remoteClusters {
		_, remoteSrc, err := net.ParseCIDR(rc.ClusterNetwork)
		if err != nil {
			return fmt.Errorf("parsing remote cluster network %s: %w", rc.ClusterNetwork, err)
		}

		for _, localSvcCIDR := range c.localServiceCIDRs {
			_, localDst, err := net.ParseCIDR(localSvcCIDR)
			if err != nil {
				return fmt.Errorf("parsing local service network %s: %w", localSvcCIDR, err)
			}

			key := ruleKey{remoteSrc.String(), localDst.String()}
			if existingC2CCRules[key] {
				delete(existingC2CCRules, key)
				continue
			}

			rule := netlink.NewRule()
			rule.Priority = c2ccSvcRulePriority
			rule.Table = c2ccSvcRouteTable
			rule.Src = remoteSrc
			rule.Dst = localDst
			if err := netlink.RuleAdd(rule); err != nil {
				return fmt.Errorf("adding ip rule from %s to %s table %d: %w",
					remoteSrc, localDst, c2ccSvcRouteTable, err)
			}
			klog.Infof("%s added ip rule: from %s to %s lookup table %d",
				c.Name(), remoteSrc, localDst, c2ccSvcRouteTable)
		}
	}

	// Remove stale service ip rules (remote clusters removed from config).
	for key := range existingC2CCRules {
		_, src, _ := net.ParseCIDR(key.src)
		_, dst, _ := net.ParseCIDR(key.dst)
		rule := netlink.NewRule()
		rule.Priority = c2ccSvcRulePriority
		rule.Table = c2ccSvcRouteTable
		rule.Src = src
		rule.Dst = dst
		if err := netlink.RuleDel(rule); err != nil {
			return fmt.Errorf("deleting stale service ip rule from %s to %s: %w", key.src, key.dst, err)
		}
		klog.Infof("%s removed stale service ip rule: from %s to %s lookup table %d",
			c.Name(), key.src, key.dst, c2ccSvcRouteTable)
	}

	// --- Routes in table 201: local service CIDRs via ovn-k8s-mp0 ---
	existingRoutes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL,
		&netlink.Route{Table: c2ccSvcRouteTable}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return fmt.Errorf("listing routes in table %d: %w", c2ccSvcRouteTable, err)
	}

	existingRouteDsts := make(map[string]bool)
	for _, r := range existingRoutes {
		if r.Dst != nil {
			existingRouteDsts[r.Dst.String()] = true
		}
	}

	for _, localSvcCIDR := range c.localServiceCIDRs {
		_, dst, err := net.ParseCIDR(localSvcCIDR)
		if err != nil {
			return fmt.Errorf("parsing local service CIDR %s: %w", localSvcCIDR, err)
		}

		if existingRouteDsts[dst.String()] {
			continue
		}

		var gw net.IP
		if dst.IP.To4() != nil {
			gw = mgmtGatewayV4
		} else {
			gw = mgmtGatewayV6
		}
		if gw == nil {
			klog.Warningf("%s no management port gateway for service CIDR %s, skipping", c.Name(), localSvcCIDR)
			continue
		}

		route := &netlink.Route{
			Dst:       dst,
			Gw:        gw,
			Table:     c2ccSvcRouteTable,
			Protocol:  c2ccRouteProto,
			LinkIndex: mgmtLink.Attrs().Index,
		}
		if err := netlink.RouteAdd(route); err != nil {
			return fmt.Errorf("adding service route %s via %s to table %d: %w",
				dst, gw, c2ccSvcRouteTable, err)
		}
		klog.Infof("%s added service route: %s via %s dev %s (table %d)",
			c.Name(), dst, gw, mgmtPortIface, c2ccSvcRouteTable)
	}

	return nil
}

// reconcileIPRules ensures destination-scoped ip rules exist for each
// remote cluster CIDR, directing only cross-cluster traffic to table 200.
// Stale rules (from removed remote clusters) are cleaned up.
func (c *C2CCRouteManager) reconcileIPRules(desired map[string]routeSpec) error {
	rules, err := netlink.RuleList(netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("listing ip rules: %w", err)
	}

	// Index existing C2CC rules by destination CIDR.
	existingRules := make(map[string]bool)
	for _, r := range rules {
		if r.Priority == c2ccRulePriority && r.Table == c2ccRouteTable && r.Dst != nil {
			existingRules[r.Dst.String()] = true
		}
	}

	// Add missing rules for desired CIDRs.
	for cidr, spec := range desired {
		if existingRules[cidr] {
			delete(existingRules, cidr)
			continue
		}

		rule := netlink.NewRule()
		rule.Priority = c2ccRulePriority
		rule.Table = c2ccRouteTable
		rule.Dst = spec.dst
		if err := netlink.RuleAdd(rule); err != nil {
			return fmt.Errorf("adding ip rule to %s table %d: %w", cidr, c2ccRouteTable, err)
		}
		klog.Infof("%s added ip rule: to %s lookup table %d (priority %d)", c.Name(), cidr, c2ccRouteTable, c2ccRulePriority)
	}

	// Remove stale rules (CIDRs no longer in config).
	for cidr := range existingRules {
		_, dst, err := net.ParseCIDR(cidr)
		if err != nil {
			klog.Warningf("%s could not parse stale rule CIDR %s: %v", c.Name(), cidr, err)
			continue
		}
		rule := netlink.NewRule()
		rule.Priority = c2ccRulePriority
		rule.Table = c2ccRouteTable
		rule.Dst = dst
		if err := netlink.RuleDel(rule); err != nil {
			return fmt.Errorf("deleting stale ip rule to %s: %w", cidr, err)
		}
		klog.Infof("%s removed stale ip rule: to %s lookup table %d", c.Name(), cidr, c2ccRouteTable)
	}

	return nil
}
