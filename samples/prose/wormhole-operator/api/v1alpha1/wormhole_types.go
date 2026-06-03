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

// WormholeSpec defines the desired state of Wormhole.
type WormholeSpec struct {
	// Destination is where the far mouth opens. Cosmetic; it only ever flows into
	// a ConfigMap and a coordinate reservation.
	Destination string `json:"destination,omitempty"`

	// Throughput is the requested traffic units. When greater than zero and the
	// tunnel is open, the reconcile routes traffic through the referenced relay.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	Throughput int32 `json:"throughput,omitempty"`

	// RelayRef is the name of the cluster-scoped SubspaceRelay to route through.
	RelayRef string `json:"relayRef,omitempty"`

	// Paused stops reconciliation when true. It drives the skip gate.
	// +kubebuilder:default=false
	Paused bool `json:"paused,omitempty"`
}

// WormholeStatus defines the observed state of Wormhole.
type WormholeStatus struct {
	// Phase is a coarse human-facing lifecycle marker.
	Phase string `json:"phase,omitempty"`

	// Charge climbs toward the ignition threshold (88) over successive reconciles.
	Charge int32 `json:"charge,omitempty"`

	// Coordinates is the reserved subspace coordinate id.
	Coordinates string `json:"coordinates,omitempty"`

	// EntryAnchor and ExitAnchor name the two owned Anchor resources.
	EntryAnchor string `json:"entryAnchor,omitempty"`
	ExitAnchor  string `json:"exitAnchor,omitempty"`

	// LinkSession is the id of the most recent subspace link.
	LinkSession string `json:"linkSession,omitempty"`

	// Conditions follow the standard Kubernetes condition convention.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Charge",type=integer,JSONPath=`.status.charge`
// +kubebuilder:printcolumn:name="Destination",type=string,JSONPath=`.spec.destination`

// Wormhole is the Schema for the wormholes API.
type Wormhole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WormholeSpec   `json:"spec,omitempty"`
	Status WormholeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WormholeList contains a list of Wormhole.
type WormholeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Wormhole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Wormhole{}, &WormholeList{})
}
