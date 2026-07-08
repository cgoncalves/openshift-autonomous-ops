/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ApplicationIntentSpec defines the desired state of ApplicationIntent
type ApplicationIntentSpec struct {
	// target identifies the deployment to manage
	// +required
	Target TargetRef `json:"target"`

	// objectives define the SLA goals
	// +required
	// +kubebuilder:validation:MinItems=1
	Objectives []Objective `json:"objectives"`

	// constraints define scaling and resource boundaries
	// +required
	Constraints Constraints `json:"constraints"`

	// autoApprove controls whether AI recommendations are applied without human approval
	// +kubebuilder:default=false
	// +optional
	AutoApprove bool `json:"autoApprove,omitempty"`
}

type TargetRef struct {
	// +kubebuilder:validation:MinLength=1
	Deployment string `json:"deployment"`

	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// +kubebuilder:validation:Enum=Latency;Availability;Throughput
type ObjectiveType string

const (
	ObjectiveLatency      ObjectiveType = "Latency"
	ObjectiveAvailability ObjectiveType = "Availability"
	ObjectiveThroughput   ObjectiveType = "Throughput"
)

type Objective struct {
	// +required
	Type ObjectiveType `json:"type"`

	// metric name (e.g., "p99", "p95", "error_rate")
	// +optional
	Metric string `json:"metric,omitempty"`

	// target value as string (e.g., "100ms", "99.9%", "1000rps")
	// +required
	Target string `json:"target"`
}

type Constraints struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +optional
	MinReplicas int32 `json:"minReplicas,omitempty"`

	// +kubebuilder:validation:Minimum=1
	MaxReplicas int32 `json:"maxReplicas"`

	// +optional
	MaxCPUPerPod string `json:"maxCPUPerPod,omitempty"`

	// +optional
	MaxMemoryPerPod string `json:"maxMemoryPerPod,omitempty"`
}

// +kubebuilder:validation:Enum=Analyzing;PendingApproval;Applying;Active;Fulfilled;Adapting;Degraded;Error
type IntentPhase string

const (
	PhaseAnalyzing       IntentPhase = "Analyzing"
	PhasePendingApproval IntentPhase = "PendingApproval"
	PhaseApplying        IntentPhase = "Applying"
	PhaseActive          IntentPhase = "Active"
	PhaseFulfilled       IntentPhase = "Fulfilled"
	PhaseAdapting        IntentPhase = "Adapting"
	PhaseDegraded        IntentPhase = "Degraded"
	PhaseError           IntentPhase = "Error"
)

// ApplicationIntentStatus defines the observed state of ApplicationIntent.
type ApplicationIntentStatus struct {
	// +optional
	Phase IntentPhase `json:"phase,omitempty"`

	// +optional
	Approved bool `json:"approved,omitempty"`

	// +optional
	Recommendation *Recommendation `json:"recommendation,omitempty"`

	// +optional
	Fulfillment *Fulfillment `json:"fulfillment,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`
}

type Recommendation struct {
	// Human-readable summary of the analysis and recommendation
	// +optional
	Summary string `json:"summary,omitempty"`

	// When the recommendation was generated
	// +optional
	GeneratedAt *metav1.Time `json:"generatedAt,omitempty"`

	// Generated K8s resource manifests as raw JSON
	// +optional
	Resources []ResourceManifest `json:"resources,omitempty"`
}

type ResourceManifest struct {
	// +required
	APIVersion string `json:"apiVersion"`

	// +required
	Kind string `json:"kind"`

	// +required
	Name string `json:"name"`

	// Raw YAML/JSON manifest
	// +required
	Manifest string `json:"manifest"`
}

type Fulfillment struct {
	// +optional
	State string `json:"state,omitempty"`

	// +optional
	CurrentReplicas int32 `json:"currentReplicas,omitempty"`

	// +optional
	LastChecked *metav1.Time `json:"lastChecked,omitempty"`

	// +optional
	DegradedSince *metav1.Time `json:"degradedSince,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.target.deployment`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Approved",type=boolean,JSONPath=`.status.approved`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.fulfillment.currentReplicas`

// ApplicationIntent is the Schema for the applicationintents API
type ApplicationIntent struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ApplicationIntent
	// +required
	Spec ApplicationIntentSpec `json:"spec"`

	// status defines the observed state of ApplicationIntent
	// +optional
	Status ApplicationIntentStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ApplicationIntentList contains a list of ApplicationIntent
type ApplicationIntentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ApplicationIntent `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ApplicationIntent{}, &ApplicationIntentList{})
		return nil
	})
}
