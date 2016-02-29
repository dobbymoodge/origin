package podnodeconstraints

import (
	"fmt"
	"io"
	"reflect"

	admission "k8s.io/kubernetes/pkg/admission"
	kapi "k8s.io/kubernetes/pkg/api"
	kapierrors "k8s.io/kubernetes/pkg/api/errors"
	"k8s.io/kubernetes/pkg/apis/extensions"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/util/sets"

	authorizationapi "github.com/openshift/origin/pkg/authorization/api"
	"github.com/openshift/origin/pkg/client"
	oadmission "github.com/openshift/origin/pkg/cmd/server/admission"
	configlatest "github.com/openshift/origin/pkg/cmd/server/api/latest"
	deployapi "github.com/openshift/origin/pkg/deploy/api"
	"github.com/openshift/origin/pkg/scheduler/admission/podnodeconstraints/api"
)

func init() {
	admission.RegisterPlugin("PodNodeConstraints", func(c clientset.Interface, config io.Reader) (admission.Interface, error) {
		pluginConfig, err := readConfig(config)
		if err != nil {
			return nil, err
		}
		return NewPodNodeConstraints(pluginConfig), nil
	})
}

// NewPodNodeConstraints creates a new admission plugin to prevent objects that contain pod templates
// from containing node bindings by name or selector based on role permissions.
func NewPodNodeConstraints(config *api.PodNodeConstraintsConfig) admission.Interface {
	return &podNodeConstraints{
		config:  config,
		Handler: admission.NewHandler(admission.Create),
	}
}

type podNodeConstraints struct {
	*admission.Handler
	client client.Interface
	config *api.PodNodeConstraintsConfig
}

var _ = oadmission.WantsOpenshiftClient(&podNodeConstraints{})
var _ = oadmission.Validator(&podNodeConstraints{})

func readConfig(reader io.Reader) (*api.PodNodeConstraintsConfig, error) {
	if reader == nil || reflect.ValueOf(reader).IsNil() {
		return &api.PodNodeConstraintsConfig{}, nil
	}

	obj, err := configlatest.ReadYAML(reader)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	config, ok := obj.(*api.PodNodeConstraintsConfig)
	if !ok {
		return nil, fmt.Errorf("unexpected config object: %#v", obj)
	}
	// No validation needed since config is just list of strings
	return config, nil
}

func (o *podNodeConstraints) Admit(attr admission.Attributes) error {
	switch {
	// we want CREATE on primary resources we care about
	case o.config == nil,
		attr.GetSubresource() != "",
		attr.GetOperation() != admission.Create:
		return nil
	}
	switch attr.GetResource() {
	case kapi.Resource("pods"),
		kapi.Resource("replicationcontrollers"),
		extensions.Resource("deployments"),
		extensions.Resource("replicasets"),
		extensions.Resource("jobs"),
		deployapi.Resource("deploymentconfigs"):
		// These are the only types we care about
	default:
		return nil
	}
	ps, err := o.getPodSpec(attr)
	if err == nil {
		return o.admitPodSpec(attr, ps)
	}
	return err
}

// extract the PodSpec from the pod templates for each object we care about
func (o *podNodeConstraints) getPodSpec(attr admission.Attributes) (kapi.PodSpec, error) {
	switch r := attr.GetObject().(type) {
	case *kapi.Pod:
		return r.Spec, nil
	case *kapi.ReplicationController:
		return r.Spec.Template.Spec, nil
	case *extensions.Deployment:
		return r.Spec.Template.Spec, nil
	case *extensions.ReplicaSet:
		return r.Spec.Template.Spec, nil
	case *extensions.Job:
		return r.Spec.Template.Spec, nil
	case *deployapi.DeploymentConfig:
		return r.Spec.Template.Spec, nil
	}
	return kapi.PodSpec{}, kapierrors.NewInternalError(fmt.Errorf("No PodSpec available for supplied admission attribute"))
}

// validate PodSpec if NodeName or NodeSelector are specified
func (o *podNodeConstraints) admitPodSpec(attr admission.Attributes, ps kapi.PodSpec) error {
	lbls := []string{}
	// nodeSelector blacklist filter
	if len(ps.NodeSelector) > 0 {
		for nslbl := range ps.NodeSelector {
			for _, bllbl := range o.config.NodeSelectorLabelBlacklist {
				if bllbl == nslbl {
					lbls = append(lbls, bllbl)
				}
			}
		}
	}
	// nodeName constraint
	if len(ps.NodeName) > 0 || len(lbls) > 0 {
		allow, err := o.checkPodsBindAccess(attr)
		if err != nil {
			return err
		}
		if allow != nil && !allow.Allowed {
			switch {
			case len(ps.NodeName) > 0, len(lbls) == 0:
				return admission.NewForbidden(attr, fmt.Errorf("node selection by nodeName is prohibited by policy for your role"))
			case len(ps.NodeName) == 0, len(lbls) > 0:
				return admission.NewForbidden(attr, fmt.Errorf("node selection by label(s) %v is prohibited by policy for your role", lbls))
			case len(ps.NodeName) > 0, len(lbls) > 0:
				return admission.NewForbidden(attr, fmt.Errorf("node selection by nodeName and label(s) %v is prohibited by policy for your role", lbls))
			}
		}
	}
	return nil
}

func (o *podNodeConstraints) SetOpenshiftClient(c client.Interface) {
	o.client = c
}

func (o *podNodeConstraints) Validate() error {
	if o.client == nil {
		return fmt.Errorf("PodNodeConstraints needs an Openshift client")
	}
	return nil
}

// build LocalSubjectAccessReview struct to validate role via checkAccess
func (o *podNodeConstraints) checkPodsBindAccess(attr admission.Attributes) (*authorizationapi.SubjectAccessReviewResponse, error) {
	sar := &authorizationapi.LocalSubjectAccessReview{
		Action: authorizationapi.AuthorizationAttributes{
			Verb:         "create",
			Resource:     "pods/binding",
			ResourceName: attr.GetName(),
		},
		User:   attr.GetUserInfo().GetName(),
		Groups: sets.NewString(attr.GetUserInfo().GetGroups()...),
	}
	return o.client.LocalSubjectAccessReviews(attr.GetNamespace()).Create(sar)
}
