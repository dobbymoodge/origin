package v1_test

import (
	"testing"

	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/util/sets"

	"github.com/openshift/origin/pkg/scheduler/admission/podnodeconstraints/api"
	versioned "github.com/openshift/origin/pkg/scheduler/admission/podnodeconstraints/api/v1"
)

func TestConversions(t *testing.T) {
	input := &versioned.PodNodeConstraintsConfig{
		NodeSelectorLabelBlacklist: []string{"test"},
	}
	expected := api.PodNodeConstraintsConfig{
		NodeSelectorLabelBlacklist: sets.NewString([]string{"test"}...),
	}
	output := &api.PodNodeConstraintsConfig{}
	err := kapi.Scheme.Convert(input, output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !kapi.Semantic.DeepEqual(&output, &expected) {
		t.Errorf("unexpected conversion; Expected %+v; Got %+v", expected, output)
	}
}
