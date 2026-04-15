package c2cc

import (
	"github.com/ovn-kubernetes/libovsdb/model"
)

// Minimal OVN Northbound DB models for C2CC route management.
// Only the fields needed for static route and NAT operations are defined.
// These mirror the generated models in ovn-kubernetes/go-controller/pkg/nbdb/
// but avoid importing the full ovn-kubernetes module.

const (
	logicalRouterTable            = "Logical_Router"
	logicalRouterStaticRouteTable = "Logical_Router_Static_Route"
	natTable                      = "NAT"

	natTypeSNAT = "snat"
)

// LogicalRouter is a minimal model for the OVN Logical_Router table.
type LogicalRouter struct {
	UUID         string            `ovsdb:"_uuid"`
	Name         string            `ovsdb:"name"`
	StaticRoutes []string          `ovsdb:"static_routes"`
	Nat          []string          `ovsdb:"nat"`
	ExternalIDs  map[string]string `ovsdb:"external_ids"`
}

// LogicalRouterStaticRoute is a minimal model for the OVN Logical_Router_Static_Route table.
type LogicalRouterStaticRoute struct {
	UUID        string            `ovsdb:"_uuid"`
	ExternalIDs map[string]string `ovsdb:"external_ids"`
	IPPrefix    string            `ovsdb:"ip_prefix"`
	Nexthop     string            `ovsdb:"nexthop"`
	OutputPort  *string           `ovsdb:"output_port"`
	Policy      *string           `ovsdb:"policy"`
}

// NAT is a minimal model for the OVN NAT table.
// Only the fields needed to modify SNAT match expressions are included.
type NAT struct {
	UUID       string `ovsdb:"_uuid"`
	Type       string `ovsdb:"type"`
	ExternalIP string `ovsdb:"external_ip"`
	LogicalIP  string `ovsdb:"logical_ip"`
	Match      string `ovsdb:"match"`
}

// nbdbModel returns a ClientDBModel for the OVN Northbound DB containing
// only the tables required for C2CC route management.
func nbdbModel() (model.ClientDBModel, error) {
	return model.NewClientDBModel("OVN_Northbound", map[string]model.Model{
		logicalRouterTable:            &LogicalRouter{},
		logicalRouterStaticRouteTable: &LogicalRouterStaticRoute{},
		natTable:                      &NAT{},
	})
}
