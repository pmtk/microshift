package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/openshift/microshift/pkg/config"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	deploymentutil "k8s.io/kubectl/pkg/util/deployment"
)

type NamespaceWorkloads struct {
	Deployments  []string `json:"deployments"`
	DaemonSets   []string `json:"daemonsets"`
	StatefulSets []string `json:"statefulsets"`
}

func waitForWorkloads(ctx context.Context, timeout time.Duration, workloads map[string]NamespaceWorkloads) error {
	restConfig, err := clientcmd.BuildConfigFromFlags("", filepath.Join(config.DataDir, "resources", string(config.KubeAdmin), "kubeconfig"))
	if err != nil {
		return fmt.Errorf("failed to create restConfig: %v", err)
	}
	client, err := appsclientv1.NewForConfig(rest.AddUserAgent(restConfig, "healthcheck"))
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	interval := timeout / 30
	if interval < 1*time.Second {
		interval = 1 * time.Second
	}
	klog.Infof("API Server will be queried every %v", interval)

	aeg := &AllErrGroup{}
	for ns, wls := range workloads {
		for _, deploy := range wls.Deployments {
			aeg.Go(func() error { return waitForDeployment(ctx, client, timeout, interval, ns, deploy) })
		}
		for _, ds := range wls.DaemonSets {
			aeg.Go(func() error { return waitForDaemonSet(ctx, client, timeout, interval, ns, ds) })
		}
		for _, sts := range wls.StatefulSets {
			aeg.Go(func() error { return waitForStatefulSet(ctx, client, timeout, interval, ns, sts) })
		}
	}
	errs := aeg.Wait()
	if errs != nil {
		// logPodsAndEvents()
		return errs
	}
	return nil
}

func waitForDaemonSet(ctx context.Context, client *appsclientv1.AppsV1Client, timeout, interval time.Duration, namespace, name string) error {
	klog.Infof("Waiting %v for daemonset/%s in %s", timeout, name, namespace)
	var lastHumanReadableErr error
	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (done bool, err error) {
		ds, err := client.DaemonSets(namespace).Get(ctx, name, v1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				// Resources created by an operator might not exist yet.
				// We allow for full timeout duration to be created and become ready.
				lastHumanReadableErr = fmt.Errorf("daemonset does not exist")
				return false, nil
			}

			if errors.Is(err, syscall.ECONNREFUSED) {
				lastHumanReadableErr = fmt.Errorf("could not connect to API server")
			}

			err = tryTransformErr(err)

			klog.Errorf("Error getting daemonset/%s in %q: %v", name, namespace, err)
			// Ignore errors, give chance until timeout
			return false, nil
		}
		klog.V(3).Infof("Status of daemonset/%s in %s: %+v", name, namespace, ds.Status)

		// Borrowed and adjusted from k8s.io/kubectl/pkg/polymorphichelpers/rollout_status.go
		if ds.Generation > ds.Status.ObservedGeneration {
			lastHumanReadableErr = fmt.Errorf("daemonset's generation not updated yet")
			return false, nil
		}
		if ds.Status.UpdatedNumberScheduled < ds.Status.DesiredNumberScheduled {
			lastHumanReadableErr = fmt.Errorf("pods are not running on desired amount of nodes")
			return false, nil
		}
		if ds.Status.NumberAvailable < ds.Status.DesiredNumberScheduled {
			lastHumanReadableErr = fmt.Errorf("not enough Pods are ready")
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			klog.Errorf("DaemonSet %q in %q namespace didn't become ready in %v: %v", name, namespace, timeout, lastHumanReadableErr)
			return fmt.Errorf("daemonset/%s in %s didn't become ready in %v: %v", name, namespace, timeout, lastHumanReadableErr)
		}
		klog.Errorf("Failed waiting for daemonset/%s in %s: %v", name, namespace, err)
		return err
	}
	klog.Infof("Daemonset/%s in %s is ready", name, namespace)
	return nil
}

func waitForDeployment(ctx context.Context, client *appsclientv1.AppsV1Client, timeout, interval time.Duration, namespace, name string) error {
	klog.Infof("Waiting %v for deployment/%s in %s", timeout, name, namespace)

	var lastHumanReadableErr error

	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		deployment, err := client.Deployments(namespace).Get(ctx, name, v1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				// Resources created by an operator might not exist yet.
				// We allow for full timeout duration to be created and become ready.
				lastHumanReadableErr = fmt.Errorf("deployment does not exist")
				return false, nil
			}

			if strings.Contains(err.Error(), "would exceed context deadline") {
				return false, context.DeadlineExceeded
			}

			// 'client rate limiter Wait returned an error: context deadline exceeded' -> drop the wrapping errors
			if errors.Is(err, context.DeadlineExceeded) {
				return false, context.DeadlineExceeded
			}

			if errors.Is(err, syscall.ECONNREFUSED) {
				err = fmt.Errorf("could not connect to API server")
			}
			klog.Errorf("Could not GET deployment %q in %q: %v", name, namespace, err)
			// client rate limiter Wait returned an error
			lastHumanReadableErr = fmt.Errorf("could not GET the deployment: %w", err)
			// Ignore errors, give chance until timeout
			return false, nil
		}
		klog.V(3).Infof("Status of deployment/%s in %s: %+v", name, namespace, deployment.Status)

		// Borrowed and adjusted from k8s.io/kubectl/pkg/polymorphichelpers/rollout_status.go
		if deployment.Generation > deployment.Status.ObservedGeneration {
			return false, nil
		}
		cond := deploymentutil.GetDeploymentCondition(deployment.Status, appsv1.DeploymentProgressing)
		if cond != nil && cond.Reason == deploymentutil.TimedOutReason {
			return false, fmt.Errorf("deployment exceeded its progress deadline") // returning error is "fatal", stops the retries
		}
		if deployment.Spec.Replicas != nil && deployment.Status.UpdatedReplicas < *deployment.Spec.Replicas {
			lastHumanReadableErr = fmt.Errorf("not enough updated replicas compared to desired amount")
			return false, nil
		}
		if deployment.Status.Replicas > deployment.Status.UpdatedReplicas {
			lastHumanReadableErr = fmt.Errorf("old replicas still exist")
			return false, nil
		}
		if deployment.Status.AvailableReplicas < deployment.Status.UpdatedReplicas {
			lastHumanReadableErr = fmt.Errorf("not all replicas are ready")
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			klog.Errorf("Deployment/%s in %s didn't become ready in %v: %v", name, namespace, timeout, lastHumanReadableErr)
			return fmt.Errorf("deployment/%s in %s didn't become ready in %v: %v", name, namespace, timeout, lastHumanReadableErr)
		}
		klog.Errorf("Failed waiting for deployment/%s in %s: %v", name, namespace, err)
		return err
	}
	klog.Infof("Deployment/%s in %s is ready", name, namespace)
	return nil
}

func waitForStatefulSet(ctx context.Context, client *appsclientv1.AppsV1Client, timeout, interval time.Duration, namespace, name string) error {
	klog.Infof("Waiting %v for statefulset/%s in %s", timeout, name, namespace)
	var lastHumanReadableErr error
	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (done bool, err error) {
		sts, err := client.StatefulSets(namespace).Get(ctx, name, v1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				// Resources created by an operator might not exist yet.
				// We allow for full timeout duration to be created and become ready.
				lastHumanReadableErr = fmt.Errorf("statefulset does not exist")
				return false, nil
			}

			err = tryTransformErr(err)

			klog.Errorf("Error getting statefulset/%s in %s: %v", name, namespace, err)
			// Ignore errors, give chance until timeout
			return false, nil
		}
		klog.V(3).Infof("Status of statefulset/%s in %s: %+v", name, namespace, sts.Status)

		// Borrowed and adjusted from k8s.io/kubectl/pkg/polymorphichelpers/rollout_status.go
		if sts.Status.ObservedGeneration == 0 || sts.Generation > sts.Status.ObservedGeneration {
			lastHumanReadableErr = fmt.Errorf("generation not updated")
			return false, nil
		}
		if sts.Spec.Replicas != nil && sts.Status.ReadyReplicas < *sts.Spec.Replicas {
			lastHumanReadableErr = fmt.Errorf("not enough ready replicas")
			return false, nil
		}
		if sts.Spec.UpdateStrategy.Type == appsv1.RollingUpdateStatefulSetStrategyType && sts.Spec.UpdateStrategy.RollingUpdate != nil {
			if sts.Spec.Replicas != nil && sts.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
				if sts.Status.UpdatedReplicas < (*sts.Spec.Replicas - *sts.Spec.UpdateStrategy.RollingUpdate.Partition) {
					lastHumanReadableErr = fmt.Errorf("not enough replicas rolled out")
					return false, nil
				}
			}
			return true, nil
		}
		if sts.Status.UpdateRevision != sts.Status.CurrentRevision {
			lastHumanReadableErr = fmt.Errorf("revision not updated")
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			klog.Errorf("Statefulset/%s in %s didn't become ready in %v: %v", name, namespace, timeout, lastHumanReadableErr)
			return fmt.Errorf("statefulset/%s in %s didn't become ready in %v: %v", name, namespace, timeout, lastHumanReadableErr)
		}
		klog.Errorf("Failed waiting for statefulset/%s in %s: %v", name, namespace, err)
		return err
	}
	klog.Infof("StatefulSet/%s in %s is ready", name, namespace)
	return nil
}

func tryTransformErr(err error) error {
	if strings.Contains(err.Error(), "would exceed context deadline") {
		return context.DeadlineExceeded
	}

	// 'client rate limiter Wait returned an error: context deadline exceeded' -> drop the wrapping errors
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}

	return err
}
