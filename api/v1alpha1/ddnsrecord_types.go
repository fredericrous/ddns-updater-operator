package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DDNSRecordSpec defines the desired state of DDNSRecord
type DDNSRecordSpec struct {
	// Provider is the DDNS provider (currently only OVH supported)
	// +kubebuilder:validation:Required
	// +kubebuilder:default="ovh"
	// +kubebuilder:validation:Enum=ovh
	Provider string `json:"provider"`

	// Domain is the base domain (e.g., daddyshome.fr)
	// +kubebuilder:validation:Required
	Domain string `json:"domain"`

	// Host is the subdomain or @ for root
	// +kubebuilder:validation:Required
	Host string `json:"host"`

	// IPVersion specifies which IP version to update
	// ipv4_and_ipv6 creates two entries (one for each) since ddns-updater doesn't support both in one
	// +kubebuilder:default="ipv4"
	// +kubebuilder:validation:Enum=ipv4;ipv6;ipv4_or_ipv6;ipv4_and_ipv6
	IPVersion string `json:"ipVersion,omitempty"`

	// IPv6Suffix is the IPv6 interface identifier suffix to use instead of the auto-detected one.
	// This is useful when the LoadBalancer has a static IPv6 suffix different from the node's SLAAC address.
	// Format: "0:0:0:0:0:0:0:166/64" or "::166/64" for suffix 166 with /64 prefix
	// If empty, the raw public IPv6 address obtained is used.
	// +optional
	IPv6Suffix string `json:"ipv6Suffix,omitempty"`

	// ProviderConfig contains provider-specific settings
	// +kubebuilder:validation:Required
	ProviderConfig OVHProviderConfig `json:"providerConfig"`
}

// OVHProviderConfig contains OVH-specific configuration
type OVHProviderConfig struct {
	// Mode is the OVH API mode
	// +kubebuilder:default="api"
	Mode string `json:"mode,omitempty"`

	// CredentialsRef references the secret containing OVH credentials
	// Secret must contain: OVH_APPLICATION_KEY, OVH_APPLICATION_SECRET, OVH_CONSUMER_KEY
	// +kubebuilder:validation:Required
	CredentialsRef SecretReference `json:"credentialsRef"`
}

// SecretReference references a secret in a namespace
type SecretReference struct {
	// Name of the secret
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the secret (defaults to the DDNSRecord namespace)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// DDNSRecordStatus defines the observed state of DDNSRecord
type DDNSRecordStatus struct {
	// Ready indicates if the record has been synced to the ConfigMap
	Ready bool `json:"ready,omitempty"`

	// LastSyncedAt is the timestamp of the last successful sync
	LastSyncedAt *metav1.Time `json:"lastSyncedAt,omitempty"`

	// Conditions represent the current state of the DDNSRecord
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ddns
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.provider`
// +kubebuilder:printcolumn:name="Domain",type=string,JSONPath=`.spec.domain`
// +kubebuilder:printcolumn:name="Host",type=string,JSONPath=`.spec.host`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DDNSRecord is the Schema for the ddnsrecords API
type DDNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DDNSRecordSpec   `json:"spec,omitempty"`
	Status DDNSRecordStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DDNSRecordList contains a list of DDNSRecord
type DDNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DDNSRecord `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DDNSRecord{}, &DDNSRecordList{})
}
