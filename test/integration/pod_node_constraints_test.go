//  +build integration

package integration

import (
	"fmt"
	"github.com/golang/glog"
	"testing"
	"time"

	"k8s.io/kubernetes/pkg/admission"
	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/extensions"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/util/intstr"
	watchapi "k8s.io/kubernetes/pkg/watch"

	"github.com/openshift/origin/pkg/client"
	policy "github.com/openshift/origin/pkg/cmd/admin/policy"
	configapi "github.com/openshift/origin/pkg/cmd/server/api"
	"github.com/openshift/origin/pkg/cmd/server/bootstrappolicy"
	projectapi "github.com/openshift/origin/pkg/project/api"
	pluginapi "github.com/openshift/origin/pkg/scheduler/admission/podnodeconstraints/api"
	testutil "github.com/openshift/origin/test/util"
	testserver "github.com/openshift/origin/test/util/server"
)

func testPodNodeConstraintsPod(nodeName string, nodeSelector *map[string]string) *kapi.Pod {
	pod := &kapi.Pod{}
	pod.Name = "testpod"
	pod.Spec.RestartPolicy = kapi.RestartPolicyNever
	if len(nodeName) > 0 {
		pod.Spec.NodeName = nodeName
	}
	if len(*nodeSelector) > 0 {
		pod.Spec.NodeSelector = *nodeSelector
	}
	pod.Spec.Containers = []kapi.Container{
		{
			Name:  "container",
			Image: "test/image",
		},
	}
	return pod
}

func testPodNodeConstraintsExpectedError(errorMsg string) error {
	attrs := admission.NewAttributesRecord(testPodNodeConstraintsPod("", &map[string]string{}), kapi.Kind("Pod"), testutil.Namespace(), "test", kapi.Resource("pods"), "", admission.Create, nil)
	return admission.NewForbidden(attrs, fmt.Errorf(errorMsg))
}

func TestPodNodeConstraintsAdmissionPluginDefaults(t *testing.T) {
	config := &pluginapi.PodNodeConstraintsConfig{}
	kclient := setupClusterAdminPodNodeConstraintsTest(t, config)
	nodeSelector := &map[string]string{"bogus": "frank"}
	_, err := kclient.Pods(testutil.Namespace()).Create(testPodNodeConstraintsPod("nodename.example.com", nodeSelector))
	if err != nil {
		t.Fatalf("Unexpected: %v", err)
	}
}

func TestPodNodeConstraintsAdmissionPluginProhibitNodeTargeting(t *testing.T) {
	config := &pluginapi.PodNodeConstraintsConfig{
		ProhibitNodeTargeting: true,
	}
	ns := "test-project"
	oclient, kclient := setupUserPodNodeConstraintsTest(t, config, ns, "derples")
	expectedError := testPodNodeConstraintsExpectedError("Binding pods to particular nodes is prohibited by policy for your role")
	nodeSelector := &map[string]string{"bogus": "frank"}
	projectRequest := &projectapi.ProjectRequest{}
	projectRequest.SetNamespace(ns)
	projectRequest.Name = kapi.SimpleNameGenerator.GenerateName(ns)
	proj, err := oclient.ProjectRequests().Create(projectRequest)
	glog.Infof("proj: %#v", proj)
	checkErr(t, err)
	_, err = kclient.Pods(ns).Create(testPodNodeConstraintsPod("nodename.example.com", nodeSelector))
	if err == nil {
		t.Fatalf("Expected error %q, no error received", expectedError.Error())
	}
	if err.Error() != expectedError.Error() {
		t.Errorf("expected error %q, got: %q", expectedError.Error(), err.Error())
	}
}

func TestPodNodeConstraintsAdmissionPluginWithDaemonSet(t *testing.T) {
	config := &pluginapi.PodNodeConstraintsConfig{}
	ns := kapi.NamespaceDefault
	kclient := setupClusterAdminPodNodeConstraintsTest(t, config)

	node := &kapi.Node{}
	node.Labels = map[string]string{"foo": "bar"}
	node.Name = "mynode"
	node.Status = kapi.NodeStatus{
		Conditions: []kapi.NodeCondition{
			{
				Type:   kapi.NodeReady,
				Status: kapi.ConditionTrue,
			},
		},
	}
	_, err := kclient.Nodes().Create(node)
	checkErr(t, err)

	dsTemplate := newValidDaemonSet()

	_, err = kclient.Extensions().DaemonSets(ns).Create(dsTemplate)
	checkErr(t, err)

	podWatch, err := kclient.Pods(ns).Watch(kapi.ListOptions{FieldSelector: fields.Everything(), ResourceVersion: "0"})
	checkErr(t, err)
	defer podWatch.Stop()
	for {
		select {
		case e := <-podWatch.ResultChan():
			if e.Type == watchapi.Added {
				pod, ok := e.Object.(*kapi.Pod)
				if !ok {
					continue
				}
				if pod.Labels["a"] == "b" {
					return
				}
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out")
		}
	}
}

func TestPodNodeConstraintsAdmissionPluginWithDaemonSetProhibitNodeTargeting(t *testing.T) {
	config := &pluginapi.PodNodeConstraintsConfig{
		ProhibitNodeTargeting: true,
	}
	ns := kapi.NamespaceDefault
	kclient := setupClusterAdminPodNodeConstraintsTest(t, config)

	node := &kapi.Node{}
	node.Labels = map[string]string{"foo": "bar"}
	node.Name = "mynode"
	node.Status = kapi.NodeStatus{
		Conditions: []kapi.NodeCondition{
			{
				Type:   kapi.NodeReady,
				Status: kapi.ConditionTrue,
			},
		},
	}
	_, err := kclient.Nodes().Create(node)
	checkErr(t, err)

	dsTemplate := newValidDaemonSet()

	_, err = kclient.Extensions().DaemonSets(ns).Create(dsTemplate)
	checkErr(t, err)

	podWatch, err := kclient.Pods(ns).Watch(kapi.ListOptions{FieldSelector: fields.Everything(), ResourceVersion: "0"})
	checkErr(t, err)
	defer podWatch.Stop()
	for {
		select {
		case e := <-podWatch.ResultChan():
			if e.Type == watchapi.Added {
				pod, ok := e.Object.(*kapi.Pod)
				if !ok {
					continue
				}
				if pod.Labels["a"] == "b" {
					return
				}
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out")
		}
	}
}

func newValidDaemonSet() *extensions.DaemonSet {
	return &extensions.DaemonSet{
		ObjectMeta: kapi.ObjectMeta{
			Name:      "foo",
			Namespace: kapi.NamespaceDefault,
		},
		Spec: extensions.DaemonSetSpec{
			Selector: &unversioned.LabelSelector{},
			Template: kapi.PodTemplateSpec{
				ObjectMeta: kapi.ObjectMeta{
					Labels: map[string]string{"a": "b"},
				},
				Spec: kapi.PodSpec{
					Containers: []kapi.Container{
						{
							Name:            "test",
							Image:           "test_image",
							ImagePullPolicy: kapi.PullIfNotPresent,
						},
					},
					RestartPolicy: kapi.RestartPolicyAlways,
					DNSPolicy:     kapi.DNSClusterFirst,
				},
			},
			UpdateStrategy: extensions.DaemonSetUpdateStrategy{
				Type: extensions.RollingUpdateDaemonSetStrategyType,
				RollingUpdate: &extensions.RollingUpdateDaemonSet{
					MaxUnavailable: intstr.FromInt(1),
				},
			},
			UniqueLabelKey: "foo-label",
		},
	}
}

func setupClusterAdminPodNodeConstraintsTest(t *testing.T, pluginConfig *pluginapi.PodNodeConstraintsConfig) kclient.Interface {
	masterConfig, err := testserver.DefaultMasterOptions()
	if err != nil {
		t.Fatalf("error creating config: %v", err)
	}
	masterConfig.KubernetesMasterConfig.AdmissionConfig.PluginConfig = map[string]configapi.AdmissionPluginConfig{
		"PodNodeConstraints": {
			Configuration: pluginConfig,
		},
	}
	kubeConfigFile, err := testserver.StartConfiguredMaster(masterConfig)
	if err != nil {
		t.Fatalf("error starting server: %v", err)
	}
	kubeClient, err := testutil.GetClusterAdminKubeClient(kubeConfigFile)
	if err != nil {
		t.Fatalf("error getting client: %v", err)
	}
	ns := &kapi.Namespace{}
	ns.Name = testutil.Namespace()
	_, err = kubeClient.Namespaces().Create(ns)
	if err != nil {
		t.Fatalf("error creating namespace: %v", err)
	}
	if err := testserver.WaitForServiceAccounts(kubeClient, testutil.Namespace(), []string{bootstrappolicy.DefaultServiceAccountName}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	return kubeClient
}

func setupUserPodNodeConstraintsTest(t *testing.T, pluginConfig *pluginapi.PodNodeConstraintsConfig, namespace string, user string) (*client.Client, *kclient.Client) {
	masterConfig, err := testserver.DefaultMasterOptions()
	if err != nil {
		t.Fatalf("error creating config: %v", err)
	}
	masterConfig.KubernetesMasterConfig.AdmissionConfig.PluginConfig = map[string]configapi.AdmissionPluginConfig{
		"PodNodeConstraints": {
			Configuration: pluginConfig,
		},
	}
	kubeConfigFile, err := testserver.StartConfiguredMaster(masterConfig)
	if err != nil {
		t.Fatalf("error starting server: %v", err)
	}
	clusterAdminClient, err := testutil.GetClusterAdminClient(kubeConfigFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	clusterAdminClientConfig, err := testutil.GetClusterAdminClientConfig(kubeConfigFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	userClient, userkubeClient, _, err := testutil.GetClientForUser(*clusterAdminClientConfig, user)
	if err != nil {
		t.Fatalf("error getting user/kube client: %v", err)
	}
	kubeClient, err := testutil.GetClusterAdminKubeClient(kubeConfigFile)
	if err != nil {
		t.Fatalf("error getting kube client: %v", err)
	}
	ns := &kapi.Namespace{}
	ns.Name = namespace
	_, err = kubeClient.Namespaces().Create(ns)
	if err != nil {
		t.Fatalf("error creating namespace: %v", err)
	}
	if err := testserver.WaitForServiceAccounts(kubeClient, testutil.Namespace(), []string{bootstrappolicy.DefaultServiceAccountName}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	addUser := &policy.RoleModificationOptions{
		RoleNamespace:       namespace,
		RoleName:            bootstrappolicy.AdminRoleName,
		RoleBindingAccessor: policy.NewClusterRoleBindingAccessor(clusterAdminClient),
		Users:               []string{user},
	}
	if err := addUser.AddRole(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return userClient, userkubeClient
}
