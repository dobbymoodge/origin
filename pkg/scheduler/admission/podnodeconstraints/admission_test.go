package podnodeconstraints

import (
	"bytes"
	"fmt"
	"testing"

	_ "github.com/openshift/origin/pkg/api/install"
	authorizationapi "github.com/openshift/origin/pkg/authorization/api"
	"github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/client/testclient"
	oadmission "github.com/openshift/origin/pkg/cmd/server/admission"
	deployapi "github.com/openshift/origin/pkg/deploy/api"
	"github.com/openshift/origin/pkg/scheduler/admission/podnodeconstraints/api"

	admission "k8s.io/kubernetes/pkg/admission"
	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/auth/user"
	ktestclient "k8s.io/kubernetes/pkg/client/unversioned/testclient"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/serviceaccount"
)

func emptyConfig() *api.PodNodeConstraintsConfig {
	return &api.PodNodeConstraintsConfig{}
}

func testConfig() *api.PodNodeConstraintsConfig {
	return &api.PodNodeConstraintsConfig{
		NodeSelectorLabelBlacklist: []string{
			"bogus",
		},
	}
}

func defaultPod() *kapi.Pod {
	pod := &kapi.Pod{}
	return pod
}

func pod(ns bool) runtime.Object {
	pod := &kapi.Pod{}
	if ns {
		pod.Spec.NodeSelector = map[string]string{"bogus": "frank"}
	}
	return pod
}

func nodeNameNodeSelectorPod() *kapi.Pod {
	pod := &kapi.Pod{}
	pod.Spec.NodeName = "frank"
	pod.Spec.NodeSelector = map[string]string{"bogus": "frank"}
	return pod
}

func nodeNamePod() *kapi.Pod {
	pod := &kapi.Pod{}
	pod.Spec.NodeName = "frank"
	return pod
}

func nodeSelectorPod() *kapi.Pod {
	pod := &kapi.Pod{}
	pod.Spec.NodeSelector = map[string]string{"bogus": "frank"}
	return pod
}

func emptyNodeSelectorPod() *kapi.Pod {
	pod := &kapi.Pod{}
	pod.Spec.NodeSelector = map[string]string{}
	return pod
}

func nodeSelectorPodTemplateSpec(ns bool) *kapi.PodTemplateSpec {
	pts := &kapi.PodTemplateSpec{}
	if ns {
		pts.Spec.NodeSelector = map[string]string{"bogus": "frank"}
	}
	return pts
}

func replicationController(ns bool) runtime.Object {
	rc := &kapi.ReplicationController{}
	rc.Spec.Template = nodeSelectorPodTemplateSpec(ns)
	return rc
}

func deployment(ns bool) runtime.Object {
	d := &extensions.Deployment{}
	d.Spec.Template = *nodeSelectorPodTemplateSpec(ns)
	return d
}

func replicaSet(ns bool) runtime.Object {
	rs := &extensions.ReplicaSet{}
	rs.Spec.Template = nodeSelectorPodTemplateSpec(ns)
	return rs
}

func job(ns bool) runtime.Object {
	j := &extensions.Job{}
	j.Spec.Template = *nodeSelectorPodTemplateSpec(ns)
	return j
}

func resourceQuota() runtime.Object {
	rq := &kapi.ResourceQuota{}
	return rq
}

func deploymentConfig(ns bool) runtime.Object {
	dc := &deployapi.DeploymentConfig{}
	dc.Spec.Template = nodeSelectorPodTemplateSpec(ns)
	return dc
}

func TestPodNodeConstraints(t *testing.T) {
	ns := kapi.NamespaceDefault
	tests := []struct {
		config           *api.PodNodeConstraintsConfig
		resource         runtime.Object
		kind             unversioned.GroupKind
		groupresource    unversioned.GroupResource
		userinfo         user.Info
		reviewResponse   *authorizationapi.SubjectAccessReviewResponse
		expectedResource string
		expectedErrorMsg string
	}{
		// 0: expect unspecified defaults to not error
		{
			config:           emptyConfig(),
			resource:         defaultPod(),
			userinfo:         serviceaccount.UserInfo("", "", ""),
			reviewResponse:   reviewResponse(false, ""),
			expectedResource: "pods/binding",
			expectedErrorMsg: "",
		},
		// 1: expect nodeSelector to error with user which lacks "pods/binding" access
		{
			config:           testConfig(),
			resource:         nodeSelectorPod(),
			userinfo:         serviceaccount.UserInfo("", "", ""),
			reviewResponse:   reviewResponse(false, ""),
			expectedResource: "pods/binding",
			expectedErrorMsg: "node selection by label(s) [bogus] is prohibited by policy for your role",
		},
		// 2: expect nodeName to fail with user that lacks "pods/binding" access
		{
			config:           testConfig(),
			resource:         nodeNamePod(),
			userinfo:         serviceaccount.UserInfo("herpy", "derpy", ""),
			reviewResponse:   reviewResponse(false, ""),
			expectedResource: "pods/binding",
			expectedErrorMsg: "node selection by nodeName is prohibited by policy for your role",
		},
		// 3: expect nodeName and nodeSelector to fail with user that lacks "pods/binding" access
		{
			config:           testConfig(),
			resource:         nodeNameNodeSelectorPod(),
			userinfo:         serviceaccount.UserInfo("herpy", "derpy", ""),
			reviewResponse:   reviewResponse(false, ""),
			expectedResource: "pods/binding",
			expectedErrorMsg: "node selection by nodeName and label(s) [bogus] is prohibited by policy for your role",
		},
		// 4: expect nodeSelector to succeed with user that has "pods/binding" access
		{
			config:           testConfig(),
			resource:         nodeSelectorPod(),
			userinfo:         serviceaccount.UserInfo("openshift-infra", "daemonset-controller", ""),
			reviewResponse:   reviewResponse(true, ""),
			expectedResource: "pods/binding",
			expectedErrorMsg: "",
		},
		// 5: expect nodeName to succeed with user that has "pods/binding" access
		{
			config:           testConfig(),
			resource:         nodeNamePod(),
			userinfo:         serviceaccount.UserInfo("openshift-infra", "daemonset-controller", ""),
			reviewResponse:   reviewResponse(true, ""),
			expectedResource: "pods/binding",
			expectedErrorMsg: "",
		},
		// 6: expect nil config to bypass admission
		{
			config:           nil,
			resource:         defaultPod(),
			userinfo:         serviceaccount.UserInfo("", "", ""),
			reviewResponse:   reviewResponse(false, ""),
			expectedResource: "pods/binding",
			expectedErrorMsg: "",
		},
	}
	for i, tc := range tests {
		var expectedError error
		client := fakeClient("pods/binding", tc.reviewResponse)
		prc := NewPodNodeConstraints(tc.config)
		prc.(oadmission.WantsOpenshiftClient).SetOpenshiftClient(client)
		attrs := admission.NewAttributesRecord(tc.resource, kapi.Kind("Pod"), ns, "test", kapi.Resource("pods"), "", admission.Create, tc.userinfo)
		if tc.expectedErrorMsg != "" {
			expectedError = admission.NewForbidden(attrs, fmt.Errorf(tc.expectedErrorMsg))
		}
		err := prc.Admit(attrs)
		checkAdmitError(t, err, expectedError, fmt.Sprintf("%d", i))
	}
}

func TestPodNodeConstraintsPodUpdate(t *testing.T) {
	ns := kapi.NamespaceDefault
	var expectedError error
	prc := NewPodNodeConstraints(testConfig())
	client := fakeClient("pods/binding", reviewResponse(false, ""))
	prc.(oadmission.WantsOpenshiftClient).SetOpenshiftClient(client)
	attrs := admission.NewAttributesRecord(nodeNamePod(), kapi.Kind("Pod"), ns, "test", kapi.Resource("pods"), "", admission.Update, serviceaccount.UserInfo("", "", ""))
	err := prc.Admit(attrs)
	checkAdmitError(t, err, expectedError, "PodUpdate")
}

func TestPodNodeConstraintsNonHandledResources(t *testing.T) {
	ns := kapi.NamespaceDefault
	var expectedError error
	prc := NewPodNodeConstraints(testConfig())
	client := fakeClient("pods/binding", reviewResponse(false, ""))
	prc.(oadmission.WantsOpenshiftClient).SetOpenshiftClient(client)
	attrs := admission.NewAttributesRecord(resourceQuota(), kapi.Kind("ResourceQuota"), ns, "test", kapi.Resource("resourcequotas"), "", admission.Create, serviceaccount.UserInfo("", "", ""))
	err := prc.Admit(attrs)
	checkAdmitError(t, err, expectedError, "ResourceQuotaTest")
}

func TestPodNodeConstraintsResources(t *testing.T) {
	ns := kapi.NamespaceDefault
	testconfigs := []struct {
		config         *api.PodNodeConstraintsConfig
		userinfo       user.Info
		reviewResponse *authorizationapi.SubjectAccessReviewResponse
	}{
		{
			config:         testConfig(),
			userinfo:       serviceaccount.UserInfo("", "", ""),
			reviewResponse: reviewResponse(false, ""),
		},
	}
	testresources := []struct {
		resource      func(bool) runtime.Object
		kind          unversioned.GroupKind
		groupresource unversioned.GroupResource
		prefix        string
	}{
		// {
		// 	resource:      pod,
		// 	kind:          kapi.Kind("Pod"),
		// 	groupresource: kapi.Resource("pods"),
		// 	prefix:        "Pod",
		// },
		{
			resource:      replicationController,
			kind:          kapi.Kind("ReplicationController"),
			groupresource: kapi.Resource("replicationcontrollers"),
			prefix:        "ReplicationController",
		},
		{
			resource:      deployment,
			kind:          extensions.Kind("Deployment"),
			groupresource: extensions.Resource("deployments"),
			prefix:        "Deployment",
		},
		{
			resource:      replicaSet,
			kind:          extensions.Kind("ReplicaSet"),
			groupresource: extensions.Resource("replicasets"),
			prefix:        "ReplicaSet",
		},
		{
			resource:      job,
			kind:          extensions.Kind("Job"),
			groupresource: extensions.Resource("jobs"),
			prefix:        "Job",
		},
		{
			resource:      deploymentConfig,
			kind:          deployapi.Kind("DeploymentConfig"),
			groupresource: deployapi.Resource("deploymentconfigs"),
			prefix:        "DeploymentConfig",
		},
	}
	testparams := []struct {
		nodeselector     bool
		expectedErrorMsg string
		prefix           string
	}{
		{
			nodeselector:     true,
			expectedErrorMsg: "node selection by label(s) [bogus] is prohibited by policy for your role",
			prefix:           "with nodeSelector",
		},
		{
			nodeselector:     false,
			expectedErrorMsg: "",
			prefix:           "without nodeSelector",
		},
	}
	testops := []struct {
		operation admission.Operation
	}{
		{
			operation: admission.Create,
		},
		{
			operation: admission.Update,
		},
	}
	for _, tc := range testconfigs {
		for _, tr := range testresources {
			for _, tp := range testparams {
				for _, top := range testops {
					var expectedError error
					client := fakeClient("pods/binding", tc.reviewResponse)
					prc := NewPodNodeConstraints(tc.config)
					prc.(oadmission.WantsOpenshiftClient).SetOpenshiftClient(client)
					attrs := admission.NewAttributesRecord(tr.resource(tp.nodeselector), tr.kind, ns, "test", tr.groupresource, "", top.operation, tc.userinfo)
					if tp.expectedErrorMsg != "" {
						expectedError = admission.NewForbidden(attrs, fmt.Errorf(tp.expectedErrorMsg))
					}
					prefix := fmt.Sprintf("%s; %s; %s", tr.prefix, tp.prefix, top.operation)
					err := prc.Admit(attrs)
					checkAdmitError(t, err, expectedError, prefix)
				}
			}
		}
	}
}

func checkAdmitError(t *testing.T, err error, expectedError error, prefix string) {
	switch {
	case expectedError == nil && err == nil:
		// continue
	case expectedError != nil && err != nil && err.Error() != expectedError.Error():
		t.Errorf("%s: expected error %q, got: %q", prefix, expectedError.Error(), err.Error())
	case expectedError == nil && err != nil:
		t.Errorf("%s: expected no error, got: %q", prefix, err.Error())
	case expectedError != nil && err == nil:
		t.Errorf("%s: expected error %q, no error recieved", prefix, expectedError.Error())
	}
}

func fakeClient(expectedResource string, reviewResponse *authorizationapi.SubjectAccessReviewResponse) client.Interface {
	emptyResponse := &authorizationapi.SubjectAccessReviewResponse{}

	fake := &testclient.Fake{}
	fake.AddReactor("create", "localsubjectaccessreviews", func(action ktestclient.Action) (handled bool, ret runtime.Object, err error) {
		review, ok := action.(ktestclient.CreateAction).GetObject().(*authorizationapi.LocalSubjectAccessReview)
		if !ok {
			return true, emptyResponse, fmt.Errorf("unexpected object received: %#v", review)
		}
		if review.Action.Resource != expectedResource {
			return true, emptyResponse, fmt.Errorf("unexpected resource received: %s. expected: %s",
				review.Action.Resource, expectedResource)
		}
		return true, reviewResponse, nil
	})
	return fake
}

func reviewResponse(allowed bool, msg string) *authorizationapi.SubjectAccessReviewResponse {
	return &authorizationapi.SubjectAccessReviewResponse{
		Allowed: allowed,
		Reason:  msg,
	}
}

func TestReadConfig(t *testing.T) {
	configStr := `apiVersion: v1
kind: PodNodeConstraintsConfig
nodeSelectorLabelBlacklist:
  - bogus
  - foo
`
	buf := bytes.NewBufferString(configStr)
	config, err := readConfig(buf)
	if err != nil {
		t.Fatalf("unexpected error reading config: %v", err)
	}
	if len(config.NodeSelectorLabelBlacklist) == 0 {
		t.Fatalf("NodeSelectorLabelBlacklist didn't take specified value")
	}
}
