package config

import (
	"fmt"
	"net"
)

// C2CC configures cluster-to-cluster communication with remote MicroShift clusters.
// When configured, MicroShift will set up OVN static routes and Linux underlay routes
// to enable pod-to-pod and pod-to-service connectivity between clusters.
type C2CC struct {
	// List of remote clusters to establish connectivity with.
	// +kubebuilder:validation:Optional
	RemoteClusters []RemoteCluster `json:"remoteClusters,omitempty"`
}

// RemoteCluster defines the networking parameters of a remote MicroShift cluster.
type RemoteCluster struct {
	// TODO: Domain to reference remote cluster from local Pod that gets rewritten. Also, make it optional.

	// Next hop IP address used for routing to this remote cluster.
	NextHop string `json:"nextHop"`

	// Cluster network CIDR of the remote cluster (e.g., "10.45.0.0/24").
	ClusterNetwork string `json:"clusterNetwork"`

	// Service network CIDR of the remote cluster (e.g., "10.46.0.0/16").
	ServiceNetwork string `json:"serviceNetwork"`
}

// IsEnabled returns true if at least one remote cluster is configured.
func (c C2CC) IsEnabled() bool {
	return len(c.RemoteClusters) > 0
}

// parsedCIDR holds a parsed network with a label for error messages.
type parsedCIDR struct {
	label string
	cidr  string
	net   *net.IPNet
}

func (c C2CC) validate(localClusterNetworks, localServiceNetworks []string) error {
	// TODO: Make sure ovn-k is enabled.

	// Parse and collect all local networks.
	var allNets []parsedCIDR
	for _, cidr := range localClusterNetworks {
		_, n, _ := net.ParseCIDR(cidr) // Not checking err, assuming it's correct because validateNetworkStack() runs before this.
		allNets = append(allNets, parsedCIDR{label: "local cluster network", cidr: cidr, net: n})
	}
	for _, cidr := range localServiceNetworks {
		_, n, _ := net.ParseCIDR(cidr) // Same reason for lack of err check
		allNets = append(allNets, parsedCIDR{label: "local service network", cidr: cidr, net: n})
	}

	// Validate and collect all remote networks, checking each against all previously seen networks.
	for i, rc := range c.RemoteClusters {
		if ip := net.ParseIP(rc.NextHop); ip == nil {
			return fmt.Errorf("c2cc.remoteClusters[%d].nextHop %q is not a valid IP address", i, rc.NextHop)
		}

		podLabel := fmt.Sprintf("c2cc.remoteClusters[%d].clusterNetwork", i)
		_, podNet, err := net.ParseCIDR(rc.ClusterNetwork)
		if err != nil {
			return fmt.Errorf("%s %q is not a valid CIDR: %v", podLabel, rc.ClusterNetwork, err)
		}
		for _, existing := range allNets {
			if podNet.Contains(existing.net.IP) || existing.net.Contains(podNet.IP) {
				return fmt.Errorf("%s %q overlaps with %s %q", podLabel, rc.ClusterNetwork, existing.label, existing.cidr)
			}
		}
		allNets = append(allNets, parsedCIDR{label: podLabel, cidr: rc.ClusterNetwork, net: podNet})

		svcLabel := fmt.Sprintf("c2cc.remoteClusters[%d].serviceNetwork", i)
		_, svcNet, err := net.ParseCIDR(rc.ServiceNetwork)
		if err != nil {
			return fmt.Errorf("%s %q is not a valid CIDR: %v", svcLabel, rc.ServiceNetwork, err)
		}
		for _, existing := range allNets {
			if svcNet.Contains(existing.net.IP) || existing.net.Contains(svcNet.IP) {
				return fmt.Errorf("%s %q overlaps with %s %q", svcLabel, rc.ServiceNetwork, existing.label, existing.cidr)
			}
		}
		allNets = append(allNets, parsedCIDR{label: svcLabel, cidr: rc.ServiceNetwork, net: svcNet})
	}
	return nil
}
