package api

import (
	"k8s.io/kubernetes/pkg/api/unversioned"
)

// PodNodeConstraintsConfig is the configuration for the pod node name
// and node selector constraint plug-in. It contains a boolean to
// allow or prohibit the use of nodeName and nodeSelector fields in
// pod requests.
type PodNodeConstraintsConfig struct {
	unversioned.TypeMeta
	// ProhibitNodeTargeting determines if policy allows targeting specific nodes via nodeName or nodeSelector in the pod spec.
	ProhibitNodeTargeting bool
}
