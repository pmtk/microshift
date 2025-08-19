package dynamicapi

import (
	"context"
	"fmt"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/openshift-kube-apiserver/admission/dynamicapi"

	"github.com/openshift/microshift/pkg/config"
)

func NewDynamicAPIManager(cfg *config.Config) (*DynamicAPIManager, error) {
	services := make(map[schema.GroupResource]APIHandler)

	routeHandler := NewRouteHandler()
	if err := routeHandler.Prepare(cfg); err != nil {
		return nil, err
	}
	// It should probably also be triggered by Ingress object
	services[schema.GroupResource{Group: "route.openshift.io", Resource: "routes"}] = routeHandler

	return &DynamicAPIManager{
		cfg:      cfg,
		Services: services,
	}, nil
}

type DynamicAPIManager struct {
	cfg *config.Config

	Services map[schema.GroupResource]APIHandler
}

func (s *DynamicAPIManager) Name() string { return "dynamicapi-manager" }
func (s *DynamicAPIManager) Dependencies() []string {
	return []string{
		"kube-apiserver",
		// RouteControllerManager depends on this,
		// but ultimately if all API needs to be registered for
		// dynamic approach, then DynamicAPIManager will rely on
		// openshift-crd-manager anyway.
		"openshift-crd-manager",
	}
}

func (s *DynamicAPIManager) Run(ctx context.Context, ready chan<- struct{}, stopped chan<- struct{}) error {
	defer close(stopped)
	close(ready)

	if err := s.startupCheck(ctx); err != nil {
		return err
	}

	go func() {
		for ntfy := range dynamicapi.NTFY {
			klog.Infof("DYNAPI ntfy: %v", ntfy)
			handler, ok := s.Services[ntfy.GR]
			if !ok {
				klog.Errorf("DYNAPI no handler found for %v", ntfy.GR)
				continue
			}

			switch ntfy.Operation {
			case admission.Delete:
				if handler.IsRunning() {
					handler.Stop()
				}
			case admission.Create:
				if handler.IsRunning() {
					klog.Infof("DYNAPI handler for %v is already running", ntfy.GR)
				} else {
					go handler.Start(ctx)
				}
			}
		}
	}()
	return nil
}

func (s *DynamicAPIManager) startupCheck(ctx context.Context) error {
	kubeconfigPath := filepath.Join(config.DataDir, "resources", string(config.KubeAdmin), "kubeconfig")
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig from %s: %v", kubeconfigPath, err)
	}

	for gr, handler := range s.Services {
		needed, err := handler.CheckIfNeeded(ctx, restConfig)
		if err != nil {
			return fmt.Errorf("failed to check if handler %v is needed: %v", handler, err)
		}
		if needed {
			klog.Infof("DYNAPI: Handler for %v is needed", gr)
			go handler.Start(ctx)
		}
	}
	return nil
}
