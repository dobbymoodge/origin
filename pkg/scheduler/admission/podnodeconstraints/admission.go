package podnodeconstraints

import (
	"fmt"
	"io"
	"reflect"

	admission "k8s.io/kubernetes/pkg/admission"
	kapi "k8s.io/kubernetes/pkg/api"
	kapierrors "k8s.io/kubernetes/pkg/api/errors"
	"k8s.io/kubernetes/pkg/api/unversioned"
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
		Handler: admission.NewHandler(admission.Create, admission.Update),
	}
}

type podNodeConstraints struct {
	*admission.Handler
	client client.Interface
	config *api.PodNodeConstraintsConfig
}

var resourcesToAdmit = map[unversioned.GroupResource]unversioned.GroupKind{
	kapi.Resource("pods"):                   kapi.Kind("Pod"),
	kapi.Resource("replicationcontrollers"): kapi.Kind("ReplicationController"),
	extensions.Resource("deployments"):      extensions.Kind("Deployment"),
	extensions.Resource("replicasets"):      extensions.Kind("ReplicaSet"),
	extensions.Resource("jobs"):             extensions.Kind("Job"),
	deployapi.Resource("deploymentconfigs"): deployapi.Kind("DeploymentConfig"),
}

func shouldAdmitResource(resource unversioned.GroupResource, kind unversioned.GroupKind) (bool, error) {
	expectedKind, shouldAdmit := resourcesToAdmit[resource]
	if !shouldAdmit {
		return false, nil
	}
	if expectedKind != kind {
		return false, fmt.Errorf("Unexpected resource kind %v for resource %v", &kind, &resource)
	}
	return true, nil
}

var _ = oadmission.WantsOpenshiftClient(&podNodeConstraints{})
var _ = oadmission.Validator(&podNodeConstraints{})

func readConfig(reader io.Reader) (*api.PodNodeConstraintsConfig, error) {
	if reader == nil || reflect.ValueOf(reader).IsNil() {
		return nil, nil
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
	case o.config == nil,
		attr.GetSubresource() != "":
		return nil
	}
	shouldAdmit, err := shouldAdmitResource(attr.GetResource(), attr.GetKind())
	if err != nil {
		return err
	}
	if !shouldAdmit {
		return nil
	}
	// Only check Create operation on pods
	if attr.GetResource() == kapi.Resource("pods") && attr.GetOperation() != admission.Create {
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
	matchingLabels := []string{}
	// nodeSelector blacklist filter
	if len(ps.NodeSelector) > 0 {
		for nodeSelectorLabel := range ps.NodeSelector {
			for _, blacklistLabel := range o.config.NodeSelectorLabelBlacklist {
				if blacklistLabel == nodeSelectorLabel {
					matchingLabels = append(matchingLabels, blacklistLabel)
				}
			}
		}
	}
	// nodeName constraint
	if len(ps.NodeName) > 0 || len(matchingLabels) > 0 {
		allow, err := o.checkPodsBindAccess(attr)
		if err != nil {
			return err
		}
		if allow != nil && !allow.Allowed {
			switch {
			case len(ps.NodeName) > 0 && len(matchingLabels) == 0:
				return admission.NewForbidden(attr, fmt.Errorf("node selection by nodeName is prohibited by policy for your role"))
			case len(ps.NodeName) == 0 && len(matchingLabels) > 0:
				return admission.NewForbidden(attr, fmt.Errorf("node selection by label(s) %v is prohibited by policy for your role", matchingLabels))
			case len(ps.NodeName) > 0 && len(matchingLabels) > 0:
				return admission.NewForbidden(attr, fmt.Errorf("node selection by nodeName and label(s) %v is prohibited by policy for your role", matchingLabels))
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
