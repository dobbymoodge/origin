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
	"github.com/openshift/origin/pkg/scheduler/admission/podnodeconstraints/api"
	ktestclient "k8s.io/kubernetes/pkg/client/unversioned/testclient"

	admission "k8s.io/kubernetes/pkg/admission"
	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/auth/user"
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

func TestPodNodeConstraints(tt *testing.T) {
	ns := kapi.NamespaceDefault
	tests := []struct {
		config           *api.PodNodeConstraintsConfig
		pod              *kapi.Pod
		userinfo         user.Info
		reviewResponse   *authorizationapi.SubjectAccessReviewResponse
		expectedResource string
		expectedErrorMsg string
	}{
		// 0: expect unspecified defaults to not error
		{
			config:           emptyConfig(),
			pod:              defaultPod(),
			userinfo:         serviceaccount.UserInfo("", "", ""),
			reviewResponse:   reviewResponse(false, ""),
			expectedResource: "pods/bind",
			expectedErrorMsg: "",
		},
		// 1: expect nodeName to error with user which lacks "pods/bind" access
		{
			config:           testConfig(),
			pod:              nodeNamePod(),
			userinfo:         serviceaccount.UserInfo("", "", ""),
			reviewResponse:   reviewResponse(false, ""),
			expectedResource: "pods/bind",
			expectedErrorMsg: "Binding nodes by nodeName is prohibited by policy for your role",
		},
		// 2: expect nodeSelector to error with user which lacks "pods/bind" access
		{
			config:           testConfig(),
			pod:              nodeSelectorPod(),
			userinfo:         serviceaccount.UserInfo("", "", ""),
			reviewResponse:   reviewResponse(false, ""),
			expectedResource: "pods/bind",
			expectedErrorMsg: "Node selection by label(s) [bogus] is prohibited by policy for your role",
		},
		// 3: expect empty nodeSelector to succeed
		{
			config:           testConfig(),
			pod:              emptyNodeSelectorPod(),
			userinfo:         serviceaccount.UserInfo("", "", ""),
			reviewResponse:   reviewResponse(false, ""),
			expectedResource: "pods/bind",
			expectedErrorMsg: "",
		},
		// 4: expect nodeSelector to succeed with user that has "pods/bind" access
		{
			config:           testConfig(),
			pod:              nodeSelectorPod(),
			userinfo:         serviceaccount.UserInfo("openshift-infra", "daemonset-controller", ""),
			reviewResponse:   reviewResponse(true, ""),
			expectedResource: "pods/bind",
			expectedErrorMsg: "",
		},
		// 5: expect nodeName to succeed with user that has "pods/bind" access
		{
			config:           testConfig(),
			pod:              nodeNamePod(),
			userinfo:         serviceaccount.UserInfo("openshift-infra", "daemonset-controller", ""),
			reviewResponse:   reviewResponse(true, ""),
			expectedResource: "pods/bind",
			expectedErrorMsg: "",
		},
		// 6: expect nodeName to fail with user that lacks "pods/bind" access
		{
			config:           testConfig(),
			pod:              nodeNamePod(),
			userinfo:         serviceaccount.UserInfo("herpy", "derpy", ""),
			reviewResponse:   reviewResponse(false, ""),
			expectedResource: "pods/bind",
			expectedErrorMsg: "Binding nodes by nodeName is prohibited by policy for your role",
		},
	}
	for ii, tc := range tests {
		var expectedError error
		client := fakeClient(tc.expectedResource, tc.reviewResponse)
		prc := NewPodNodeConstraints(tc.config)
		prc.(oadmission.WantsOpenshiftClient).SetOpenshiftClient(client)
		pod := tc.pod
		attrs := admission.NewAttributesRecord(pod, kapi.Kind("Pod"), ns, "test", kapi.Resource("pods"), "", admission.Create, tc.userinfo)
		if tc.expectedErrorMsg != "" {
			expectedError = admission.NewForbidden(attrs, fmt.Errorf(tc.expectedErrorMsg))
		}
		err := prc.Admit(attrs)
		switch {
		case expectedError == nil && err == nil:
			// continue
		case expectedError != nil && err != nil && err.Error() != expectedError.Error():
			tt.Errorf("%d: expected error %q, got: %q", ii, expectedError.Error(), err.Error())
		case expectedError == nil && err != nil:
			tt.Errorf("%d: expected no error, got: %q", ii, err.Error())
		case expectedError != nil && err == nil:
			tt.Errorf("%d: expected error %q, no error recieved", ii, expectedError.Error())
		}
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
