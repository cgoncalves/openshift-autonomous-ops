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
	// target identifies the deployment to scale
	// +required
	Target TargetRef `json:"target"`

	// sla defines the service level agreement
	// +required
	SLA SLASpec `json:"sla"`

	// constraints defines scaling boundaries
	// +required
	Constraints ConstraintsSpec `json:"constraints"`
}

type TargetRef struct {
	// deployment name
	// +kubebuilder:validation:MinLength=1
	Deployment string `json:"deployment"`

	// namespace of the deployment (defaults to the intent's namespace)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

type SLASpec struct {
	// p99Latency target in milliseconds
	// +kubebuilder:validation:Minimum=1
	P99LatencyMs int64 `json:"p99LatencyMs"`
}

type ConstraintsSpec struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	MinReplicas int32 `json:"minReplicas"`

	// +kubebuilder:validation:Minimum=1
	MaxReplicas int32 `json:"maxReplicas"`
}

// IntentState represents the current state of the intent
// +kubebuilder:validation:Enum=Fulfilled;Degraded;Scaling;Unknown
type IntentState string

const (
	StateFulfilled IntentState = "Fulfilled"
	StateDegraded  IntentState = "Degraded"
	StateScaling   IntentState = "Scaling"
	StateUnknown   IntentState = "Unknown"
)

// ApplicationIntentStatus defines the observed state of ApplicationIntent.
type ApplicationIntentStatus struct {
	// state of the intent: Fulfilled, Degraded, Scaling, Unknown
	// +optional
	State IntentState `json:"state,omitempty"`

	// currentP99Ms is the observed p99 latency in milliseconds
	// +optional
	CurrentP99Ms int64 `json:"currentP99Ms,omitempty"`

	// currentReplicas is the current replica count
	// +optional
	CurrentReplicas int32 `json:"currentReplicas,omitempty"`

	// message describes the current state
	// +optional
	Message string `json:"message,omitempty"`

	// lastUpdated timestamp
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.target.deployment`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="P99ms",type=integer,JSONPath=`.status.currentP99Ms`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.currentReplicas`
// +kubebuilder:printcolumn:name="SLA",type=integer,JSONPath=`.spec.sla.p99LatencyMs`

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
