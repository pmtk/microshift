package dynamicapi

import (
	"context"
	"io"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"

	"k8s.io/klog/v2"
)

var (
	NTFY = make(chan bool, 1)
)

const PluginName = "microshift.io/DynamicAPI"

func Register(plugins *admission.Plugins) {
	klog.Infof("PMTK Registering DynamicAPI admission plugin")
	plugins.Register(PluginName, func(config io.Reader) (admission.Interface, error) {
		klog.Infof("PMTK DynamicAPI factory: config is nil? : %v", config == nil)
		// pluginConfig, err := readConfig(config)
		// if err != nil {
		// 	klog.Infof("PMTK DynamicAPI factory: readConfig error: %v", err)
		// 	return nil, err
		// }
		return newDynamicAPI()
	})
}

type dynamicAPI struct {
	*admission.Handler

	routes int
}

var _ admission.ValidationInterface = &dynamicAPI{}

func (d *dynamicAPI) Validate(ctx context.Context, a admission.Attributes, o admission.ObjectInterfaces) (err error) {
	klog.Infof("PMTK DynamicAPI Validate: routes: %d", d.routes)
	klog.Infof("PMTK DynamicAPI Validate: attributes: Name: %s, Namespace: %s, Operation: %s, Resource: %s", a.GetName(), a.GetNamespace(), a.GetOperation(), a.GetResource())

	if a.GetResource().GroupResource() != (schema.GroupResource{Group: "route.openshift.io", Resource: "routes"}) {
		klog.Info("PMTK DynamicAPI Validate: not a route")
		return nil
	}

	switch a.GetOperation() {
	case admission.Create:
		klog.Infof("PMTK DynamicAPI Validate: Create")
		NTFY <- true
		d.routes++
	case admission.Update:
		klog.Infof("PMTK DynamicAPI Validate: Update")
	case admission.Delete:
		klog.Infof("PMTK DynamicAPI Validate: Delete")
		d.routes--
	case admission.Connect:
		klog.Infof("PMTK DynamicAPI Validate: Connect")
	}

	// klog.Infof("PMTK DynamicAPI Validate: objectInterfaces: %v", o)
	return nil
}

func newDynamicAPI() (*dynamicAPI, error) {
	return &dynamicAPI{
		Handler: admission.NewHandler(admission.Create, admission.Update, admission.Delete, admission.Connect),
	}, nil
}
