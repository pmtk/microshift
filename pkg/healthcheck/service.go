package healthcheck

import (
	"context"
	"fmt"

	"github.com/coreos/go-systemd/v22/dbus"
	"k8s.io/klog/v2"
)

func NewSystemd(ctx context.Context) (*Systemd, error) {
	conn, err := dbus.NewWithContext(ctx)
	if err != nil {
		klog.ErrorS(err, "Failed to create connection to systemd")
		return nil, err
	}
	return &Systemd{connection: conn}, nil
}

type Systemd struct {
	connection *dbus.Conn
}

func (s *Systemd) Close() {
	s.connection.Close()
}

func (s *Systemd) IsServiceEnabled(ctx context.Context, service string) (bool, error) {
	if s.connection == nil {
		return false, fmt.Errorf("struct not initialized")
	}

	props, err := s.connection.GetAllPropertiesContext(ctx, service)
	if err != nil {
		klog.ErrorS(err, "Failed to get properties of service", "service", service)
		return false, err
	}

	klog.InfoS("microshift svc props", "props", props)

	state, ok := props["UnitFileState"]
	if !ok {
		return false, fmt.Errorf("could not find 'UnitFileState' in service properties")
	}

	return state == "enabled", nil
}

func (s *Systemd) IsServiceActiveAndNotFailed(ctx context.Context, service string) (bool, error) {
	if s.connection == nil {
		return false, fmt.Errorf("struct not initialized")
	}

	props, err := s.connection.GetAllPropertiesContext(ctx, service)
	if err != nil {
		klog.ErrorS(err, "Failed to get properties of service", "service", service)
		return false, err
	}

	activeState, ok := props["ActiveState"]
	if !ok {
		return false, fmt.Errorf("could not find 'ActiveState' in service properties")
	}

	if activeState == "failed" {
		return false, fmt.Errorf("service %s has failed", service)
	}

	return activeState == "active", nil
}
