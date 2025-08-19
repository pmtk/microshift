package dynamicapi

import (
	"context"

	"github.com/openshift/microshift/pkg/config"
	"k8s.io/client-go/rest"
)

type APIHandler interface {
	Prepare(config *config.Config) error
	Start(ctx context.Context) error
	Stop()
	IsRunning() bool

	CheckIfNeeded(ctx context.Context, restConfig *rest.Config) (bool, error)
}
