package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestC2CC_IsEnabled(t *testing.T) {
	tests := []struct {
		name string
		c2cc C2CC
		want bool
	}{
		{
			name: "disabled when no remote clusters",
			c2cc: C2CC{},
			want: false,
		},
		{
			name: "disabled when empty slice",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{}},
			want: false,
		},
		{
			name: "enabled when remote clusters configured",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.1.1", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.46.0.0/16"},
			}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.c2cc.IsEnabled())
		})
	}
}

func TestC2CC_Validate(t *testing.T) {
	localClusterNetworks := []string{"10.42.0.0/16"}
	localServiceNetworks := []string{"10.43.0.0/16"}

	tests := []struct {
		name    string
		c2cc    C2CC
		wantErr string
	}{
		{
			name: "valid remote cluster",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.46.0.0/16"},
			}},
		},
		{
			name: "valid with multiple remote clusters",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.46.0.0/16"},
				{NextHop: "192.168.64.20", ClusterNetwork: "10.48.0.0/24", ServiceNetwork: "10.49.0.0/16"},
			}},
		},
		{
			name: "valid when no remote clusters",
			c2cc: C2CC{},
		},
		{
			name: "invalid nextHop",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "not-an-ip", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.46.0.0/16"},
			}},
			wantErr: `c2cc.remoteClusters[0].nextHop "not-an-ip" is not a valid IP address`,
		},
		{
			name: "empty nextHop",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.46.0.0/16"},
			}},
			wantErr: `c2cc.remoteClusters[0].nextHop "" is not a valid IP address`,
		},
		{
			name: "invalid clusterNetwork",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "bad-cidr", ServiceNetwork: "10.46.0.0/16"},
			}},
			wantErr: `c2cc.remoteClusters[0].clusterNetwork "bad-cidr" is not a valid CIDR`,
		},
		{
			name: "invalid serviceNetwork",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "nope"},
			}},
			wantErr: `c2cc.remoteClusters[0].serviceNetwork "nope" is not a valid CIDR`,
		},
		{
			name: "remote clusterNetwork overlaps local cluster network exactly",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.42.0.0/16", ServiceNetwork: "10.46.0.0/16"},
			}},
			wantErr: `c2cc.remoteClusters[0].clusterNetwork "10.42.0.0/16" overlaps with local cluster network "10.42.0.0/16"`,
		},
		{
			name: "remote serviceNetwork overlaps local service network exactly",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.43.0.0/16"},
			}},
			wantErr: `c2cc.remoteClusters[0].serviceNetwork "10.43.0.0/16" overlaps with local service network "10.43.0.0/16"`,
		},
		{
			name: "remote clusterNetwork is subnet of local cluster network",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.42.0.0/24", ServiceNetwork: "10.46.0.0/16"},
			}},
			wantErr: `c2cc.remoteClusters[0].clusterNetwork "10.42.0.0/24" overlaps with local cluster network "10.42.0.0/16"`,
		},
		{
			name: "remote serviceNetwork is supernet of local service network",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.43.0.0/8"},
			}},
			wantErr: `c2cc.remoteClusters[0].serviceNetwork "10.43.0.0/8" overlaps with local service network "10.43.0.0/16"`,
		},
		{
			name: "remote clusterNetwork overlaps local service network",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.43.0.0/24", ServiceNetwork: "10.46.0.0/16"},
			}},
			wantErr: `c2cc.remoteClusters[0].clusterNetwork "10.43.0.0/24" overlaps with local service network "10.43.0.0/16"`,
		},
		{
			name: "remote serviceNetwork overlaps local cluster network",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.42.0.0/24"},
			}},
			wantErr: `c2cc.remoteClusters[0].serviceNetwork "10.42.0.0/24" overlaps with local cluster network "10.42.0.0/16"`,
		},
		{
			name: "overlapping clusterNetworks between remote clusters",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/16", ServiceNetwork: "10.46.0.0/16"},
				{NextHop: "192.168.64.20", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.49.0.0/16"},
			}},
			wantErr: `c2cc.remoteClusters[1].clusterNetwork "10.45.0.0/24" overlaps with c2cc.remoteClusters[0].clusterNetwork "10.45.0.0/16"`,
		},
		{
			name: "overlapping serviceNetworks between remote clusters",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.46.0.0/16"},
				{NextHop: "192.168.64.20", ClusterNetwork: "10.48.0.0/24", ServiceNetwork: "10.46.0.0/24"},
			}},
			wantErr: `c2cc.remoteClusters[1].serviceNetwork "10.46.0.0/24" overlaps with c2cc.remoteClusters[0].serviceNetwork "10.46.0.0/16"`,
		},
		{
			name: "remote cluster clusterNetwork overlaps another remote cluster serviceNetwork",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.46.0.0/16"},
				{NextHop: "192.168.64.20", ClusterNetwork: "10.46.0.0/24", ServiceNetwork: "10.49.0.0/16"},
			}},
			wantErr: `c2cc.remoteClusters[1].clusterNetwork "10.46.0.0/24" overlaps with c2cc.remoteClusters[0].serviceNetwork "10.46.0.0/16"`,
		},
		{
			name: "error reported on correct index",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.46.0.0/16"},
				{NextHop: "192.168.64.20", ClusterNetwork: "10.42.0.0/16", ServiceNetwork: "10.49.0.0/16"},
			}},
			wantErr: `c2cc.remoteClusters[1].clusterNetwork "10.42.0.0/16" overlaps with local cluster network "10.42.0.0/16"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.c2cc.validate(localClusterNetworks, localServiceNetworks)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestC2CC_ValidateDualStack(t *testing.T) {
	localClusterNetworks := []string{"10.42.0.0/16", "fd01::/48"}
	localServiceNetworks := []string{"10.43.0.0/16", "fd02::/112"}

	tests := []struct {
		name    string
		c2cc    C2CC
		wantErr string
	}{
		{
			name: "valid remote cluster with different CIDRs",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "10.45.0.0/24", ServiceNetwork: "10.46.0.0/16"},
			}},
		},
		{
			name: "remote clusterNetwork overlaps local IPv6 cluster network",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "fd01::/48", ServiceNetwork: "fd04::/112"},
			}},
			wantErr: `c2cc.remoteClusters[0].clusterNetwork "fd01::/48" overlaps with local cluster network "fd01::/48"`,
		},
		{
			name: "remote serviceNetwork overlaps local IPv6 service network",
			c2cc: C2CC{RemoteClusters: []RemoteCluster{
				{NextHop: "192.168.64.19", ClusterNetwork: "fd05::/48", ServiceNetwork: "fd02::/112"},
			}},
			wantErr: `c2cc.remoteClusters[0].serviceNetwork "fd02::/112" overlaps with local service network "fd02::/112"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.c2cc.validate(localClusterNetworks, localServiceNetworks)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}
