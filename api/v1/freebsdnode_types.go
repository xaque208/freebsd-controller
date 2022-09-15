/*
Copyright 2022.

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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// FreeBSDNodeSpec defines the desired state of FreeBSDNode
type FreeBSDNodeSpec struct {
	Domain    string   `json:"domain,omitempty"`
	ASN       string   `json:"asn,omitempty"`
	JailCIDRs []string `json:"jailCIDR,omitempty"`
}

// FreeBSDNodeStatus defines the observed state of FreeBSDNode
type FreeBSDNodeStatus struct {
	Version string `json:"version,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// FreeBSDNode is the Schema for the freebsdnodes API
type FreeBSDNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FreeBSDNodeSpec   `json:"spec,omitempty"`
	Status FreeBSDNodeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FreeBSDNodeList contains a list of FreeBSDNode
type FreeBSDNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FreeBSDNode `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FreeBSDNode{}, &FreeBSDNodeList{})
}
