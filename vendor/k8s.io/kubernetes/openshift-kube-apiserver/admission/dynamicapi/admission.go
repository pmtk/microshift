package dynamicapi

import (
	"context"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"

	"k8s.io/klog/v2"
)

type DynamicAPINotification struct {
	GR        schema.GroupResource
	Operation admission.Operation
}

var (
	NTFY = make(chan DynamicAPINotification)
)

const PluginName = "microshift.io/DynamicAPI"

func Register(plugins *admission.Plugins) {
	klog.Infof("DYNAPI Registering DynamicAPI admission plugin")
	plugins.Register(PluginName, func(config io.Reader) (admission.Interface, error) {
		return newDynamicAPI()
	})
}

type dynamicAPI struct {
	*admission.Handler
}

var _ admission.ValidationInterface = &dynamicAPI{}

func (d *dynamicAPI) Validate(ctx context.Context, a admission.Attributes, o admission.ObjectInterfaces) (err error) {
	gr := a.GetResource().GroupResource()
	if !strings.Contains(gr.Group, "openshift.io") {
		return nil
	}

	klog.Infof("DYNAPI DynamicAPI Validate: attributes: Name: %s, Namespace: %s, Operation: %s, Resource: %s", a.GetName(), a.GetNamespace(), a.GetOperation(), a.GetResource())

	// This is not necessarily the best way of communicating but it's easier in context of MicroShift code.
	// It can block if something on the other end doesn't receive constantly and journal will fill with errors:
	//   Internal error occurred: admission plugin \"microshift.io/DynamicAPI\" failed to complete validation in 13s
	// DynamicAPIManager could be in other repository, imported here and called directly.
	NTFY <- DynamicAPINotification{GR: gr, Operation: a.GetOperation()}
	return nil
}

func newDynamicAPI() (*dynamicAPI, error) {
	return &dynamicAPI{
		Handler: admission.NewHandler(admission.Create, admission.Update, admission.Delete, admission.Connect),
	}, nil
}
