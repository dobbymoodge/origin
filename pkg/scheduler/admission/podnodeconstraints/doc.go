/*
Package podnodeconstraints contains the PodNodeConstraints admission
control plugin. This plugin allows administrators to prohibit the use
of the NodeName and NodeSelector attributes in pod specs. This enables
policy to prevent pod requests from influencing which nodes new pods
are scheduled on.

Configuration

The plugin is configured via a PodNodeConstraintsConfig object in the
origin and kubernetes Master configs:

admissionConfig:
    pluginConfig:
      PodNodeConstraints:
        configuration:
          apiVersion: v1
          kind: PodNodeConstraintsConfig
          prohibitNodeTargeting: true
...
kubernetesMasterConfig:
  admissionConfig:
    pluginConfig:
      PodNodeConstraints:
        configuration:
          apiVersion: v1
          kind: PodNodeConstraintsConfig
          prohibitNodeTargeting: true
*/

package podnodeconstraints
