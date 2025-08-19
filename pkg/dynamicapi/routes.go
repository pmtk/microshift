package dynamicapi

import (
	"context"
	"errors"
	"fmt"
	"sync"

	routeclient "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	"github.com/openshift/microshift/pkg/components"
	"github.com/openshift/microshift/pkg/config"
	"github.com/openshift/microshift/pkg/controllers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

var _ APIHandler = &RouteHandler{}

type RouteHandler struct {
	routeControllerManager *controllers.OCPRouteControllerManager
	ctxCancel              context.CancelFunc
	cfg                    *config.Config

	running bool
	mu      sync.Mutex
}

func NewRouteHandler() APIHandler {
	return &RouteHandler{}
}

func (h *RouteHandler) IsRunning() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

func (h *RouteHandler) CheckIfNeeded(ctx context.Context, restConfig *rest.Config) (bool, error) {
	routeClient, err := routeclient.NewForConfig(restConfig)
	if err != nil {
		return false, fmt.Errorf("failed to create route client: %v", err)
	}

	routes, err := routeClient.Routes("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list routes: %v", err)
	}

	klog.Infof("DYNAPI: Found %d routes", len(routes.Items))

	return len(routes.Items) > 0, nil
}

func (h *RouteHandler) Prepare(cfg *config.Config) error {
	m, err := controllers.NewRouteControllerManagerErr(cfg)
	if err != nil {
		return err
	}
	h.routeControllerManager = m
	h.cfg = cfg
	return nil
}

func (h *RouteHandler) Start(ctx context.Context) error {
	klog.Infof("Starting RouteHandler")
	h.mu.Lock()
	h.running = true
	h.mu.Unlock()

	// Deploy Router as a Pod
	// TODO: cfg should be copied
	klog.Infof("DYNAPI Deploying Router")
	h.cfg.Ingress.Status = config.StatusManaged
	if err := components.StartIngressController(ctx, h.cfg, h.cfg.KubeConfigPath(config.KubeAdmin)); err != nil {
		klog.Errorf("Failed to start ingress router controller: %v", err)
		return err
	}

	// Start Route Controller Manager
	// Current structure of RCM's Run is not optimal for this.
	// We probably want some kind of internal loop that will keep RCM as expected.
	// Maybe errors might be propagated back to DynamicAPIManager by a channel to let know of fatal errors.

	ctx, ctxCancel := context.WithCancel(ctx)
	h.ctxCancel = ctxCancel
	klog.Infof("DYNAPI Starting Route Controller Manager")
	for {
		if ctx.Err() != nil {
			klog.Infof("DYNAPI Route Controller Manager Context cancelled")
			return nil
		}
		ready, stopped := make(chan struct{}), make(chan struct{})
		err := h.routeControllerManager.Run(ctx, ready, stopped)
		klog.Infof("PMTK RCM Exited: %v", err)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			klog.Errorf("RCM failed to run: %v", err)
		}
	}
}

func (h *RouteHandler) Stop() {
	klog.Infof("DYNAPI Stopping RCM")
	h.ctxCancel()

	// TODO: cfg should be copied
	h.cfg.Ingress.Status = config.StatusRemoved
	klog.Infof("DYNAPI Removing Router")
	if err := components.StartIngressController(context.TODO(), h.cfg, h.cfg.KubeConfigPath(config.KubeAdmin)); err != nil {
		klog.Errorf("Failed to remove Router: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running = false
}
