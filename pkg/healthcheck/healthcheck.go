package healthcheck

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

func MicroShiftHealthcheck(ctx context.Context) error {
	systemd, err := NewSystemd(ctx)
	if err != nil {
		return err
	}
	defer systemd.Close()

	if isEnabled, err := systemd.IsServiceEnabled(ctx, "microshift.service"); err != nil {
		return err
	} else if !isEnabled {
		klog.Info("microshift.service is not enabled")
		return nil
	}
	klog.Info("microshift.service is enabled")

	if err := wait.PollUntilContextTimeout(ctx, time.Second, time.Minute, true, func(ctx context.Context) (done bool, err error) {
		return systemd.IsServiceActiveAndNotFailed(ctx, "microshift.service")
	}); err != nil {
		return err
	}

	return nil
}
