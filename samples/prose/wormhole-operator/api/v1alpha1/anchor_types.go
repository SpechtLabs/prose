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
)

// AnchorSpec defines the desired state of Anchor.
type AnchorSpec struct {
	// End is which mouth this anchor pins.
	// +kubebuilder:validation:Enum=entry;exit
	End string `json:"end"`

	// Coordinates is the reserved subspace coordinate, stamped by the owning Wormhole.
	Coordinates string `json:"coordinates,omitempty"`

	// TargetStability is the stability percentage the anchor climbs toward.
	// +kubebuilder:default=100
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	TargetStability int32 `json:"targetStability,omitempty"`
}

// AnchorStatus defines the observed state of Anchor.
type AnchorStatus struct {
	// Phase is a coarse human-facing lifecycle marker.
	Phase string `json:"phase,omitempty"`

	// Stability climbs toward TargetStability over successive reconciles.
	Stability int32 `json:"stability,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="End",type=string,JSONPath=`.spec.end`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Stability",type=integer,JSONPath=`.status.stability`

// Anchor is the Schema for the anchors API.
type Anchor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AnchorSpec   `json:"spec,omitempty"`
	Status AnchorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AnchorList contains a list of Anchor.
type AnchorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Anchor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Anchor{}, &AnchorList{})
}
