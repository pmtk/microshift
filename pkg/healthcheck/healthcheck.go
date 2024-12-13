package healthcheck

import (
	"context"
	"encoding/json"
	"time"

	"k8s.io/klog/v2"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/kustomize/kyaml/resid"
)

func MicroShiftHealthcheck(ctx context.Context, timeout time.Duration) error {
	GetWorkloadsFromAManifest()
	return nil
	if enabled, err := microshiftServiceShouldBeOk(ctx, timeout); err != nil {
		printPrerunLog()
		return err
	} else if !enabled {
		return nil
	}

	workloads, err := getCoreMicroShiftWorkloads()
	if err != nil {
		return err
	}

	if err := waitForWorkloads(ctx, timeout, workloads); err != nil {
		return err
	}

	klog.Info("MicroShift is ready")

	return nil
}

func CustomWorkloadHealthcheck(ctx context.Context, timeout time.Duration, definition string) error {
	workloads := map[string]NamespaceWorkloads{}

	err := json.Unmarshal([]byte(definition), &workloads)
	if err != nil {
		return err
	}
	klog.V(2).Infof("Deserialized '%s' into %+v", definition, workloads)

	if err := waitForWorkloads(ctx, timeout, workloads); err != nil {
		return err
	}
	klog.Info("Workloads are ready")
	return nil
}

func EasyCustomWorkloadHealthcheck(ctx context.Context, timeout time.Duration, namespace string, deployments, daemonsets, statefulsets []string) error {
	workloads := map[string]NamespaceWorkloads{
		namespace: {
			Deployments:  deployments,
			DaemonSets:   daemonsets,
			StatefulSets: statefulsets,
		},
	}

	if err := waitForWorkloads(ctx, timeout, workloads); err != nil {
		return err
	}
	klog.Info("Workloads are ready")
	return nil
}

type NW map[string]NamespaceWorkloads

func (nw NW) AddDeployment(name, namespace string) {
	if existing, ok := nw[namespace]; ok {
		existing.Deployments = append(existing.Deployments, name)
		nw[namespace] = existing
	} else {
		nw[namespace] = NamespaceWorkloads{
			Deployments: []string{name},
		}
	}
}

func (nw NW) AddDaemonSet(name, namespace string) {
	if existing, ok := nw[namespace]; ok {
		existing.DaemonSets = append(existing.DaemonSets, name)
		nw[namespace] = existing
	} else {
		nw[namespace] = NamespaceWorkloads{
			DaemonSets: []string{name},
		}
	}
}

func (nw NW) AddStatefulSet(name, namespace string) {
	if existing, ok := nw[namespace]; ok {
		existing.StatefulSets = append(existing.StatefulSets, name)
		nw[namespace] = existing
	} else {
		nw[namespace] = NamespaceWorkloads{
			StatefulSets: []string{name},
		}
	}
}

func GetWorkloadsFromAManifest() (NW, error) {
	kOpts := krusty.MakeDefaultOptions()
	k := krusty.MakeKustomizer(kOpts)
	resmap, err := k.Run(filesys.MakeFsOnDisk(), "/usr/lib/microshift/manifests.d/000-microshift-multus/")
	if err != nil {
		return nil, err
	}

	wlds := NW{}

	for _, resource := range resmap.Resources() {
		rid := resource.CurId()
		klog.Infof("name:%s  namespace:%s", rid.Name, rid.Namespace)

		if rid.Gvk.Equals(resid.Gvk{Group: "apps", Version: "v1", Kind: "Deployment"}) {
			wlds.AddDeployment(rid.Name, rid.Namespace)
		}

		if rid.Gvk.Equals(resid.Gvk{Group: "apps", Version: "v1", Kind: "DaemonSet"}) {
			wlds.AddDaemonSet(rid.Name, rid.Namespace)
		}

		if rid.Gvk.Equals(resid.Gvk{Group: "apps", Version: "v1", Kind: "StatefulSet"}) {
			wlds.AddStatefulSet(rid.Name, rid.Namespace)
		}

		annotations := resource.GetAnnotations("healthcheck.microshift.io")
		if val, ok := annotations["healthcheck.microshift.io"]; ok {
			klog.Infof("MICROSHIFT HEALTHCHECK ANNOTATION: %s", val)

			err := json.Unmarshal([]byte(val), &wlds)
			if err != nil {
				klog.Infof("json unmarshal err: %v", err)
				return nil, err
			}
		}
	}

	klog.Infof("workloads: %#v", wlds)
	return wlds, nil
}
