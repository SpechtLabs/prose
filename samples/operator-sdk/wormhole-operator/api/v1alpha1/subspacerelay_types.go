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

// SubspaceRelaySpec defines the desired state of SubspaceRelay.
type SubspaceRelaySpec struct {
	// Band is a cosmetic frequency label, for example "C-137".
	Band string `json:"band,omitempty"`

	// Bandwidth is the total throughput units this relay can carry before it
	// saturates.
	// +kubebuilder:default=100
	// +kubebuilder:validation:Minimum=0
	Bandwidth int32 `json:"bandwidth,omitempty"`
}

// SubspaceRelayStatus defines the observed state of SubspaceRelay.
type SubspaceRelayStatus struct {
	// Phase is Online, Saturated, or Offline.
	Phase string `json:"phase,omitempty"`

	// ConnectedWormholes is how many Wormholes route through this relay.
	ConnectedWormholes int32 `json:"connectedWormholes,omitempty"`

	// ConsumedBandwidth is the summed throughput of connected Wormholes.
	ConsumedBandwidth int32 `json:"consumedBandwidth,omitempty"`

	// Saturated is true when ConsumedBandwidth exceeds Spec.Bandwidth.
	Saturated bool `json:"saturated,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Band",type=string,JSONPath=`.spec.band`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Connected",type=integer,JSONPath=`.status.connectedWormholes`

// SubspaceRelay is the Schema for the subspacerelays API. It is cluster-scoped:
// Wormholes in any namespace route through one relay by name.
type SubspaceRelay struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubspaceRelaySpec   `json:"spec,omitempty"`
	Status SubspaceRelayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SubspaceRelayList contains a list of SubspaceRelay.
type SubspaceRelayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubspaceRelay `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SubspaceRelay{}, &SubspaceRelayList{})
}
