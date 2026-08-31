package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DDNSRecordSpec defines the desired state of DDNSRecord
type DDNSRecordSpec struct {
	// Provider is the ddns-updater provider name, e.g. "ovh", "cloudflare",
	// "duckdns". Any provider supported by the running ddns-updater is
	// accepted: the operator passes the name through verbatim rather than
	// validating it against a built-in list, so a provider added upstream
	// works without an operator release. An unrecognised name is reported as
	// a warning Event, not a rejection.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9._-]*$`
	Provider string `json:"provider"`

	// Domain is the base domain (e.g., example.com)
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

	// Config holds the provider-specific settings, merged verbatim into the
	// ddns-updater config entry. Keys and value types are whatever the
	// provider documents (see the ddns-updater docs/ directory), e.g.
	// {"zone_identifier": "abc", "proxied": true, "ttl": 300}.
	//
	// Never put credentials here — the whole entry lands in a ConfigMap.
	// Use configFrom for anything secret.
	//
	// The reserved keys provider, domain, host, ip_version and ipv6_suffix
	// are owned by the fields above and rejected here.
	// +optional
	Config map[string]apiextensionsv1.JSON `json:"config,omitempty"`

	// ConfigFrom injects provider settings from Secrets. Each item sets one
	// key of the ddns-updater config entry to the value of a Secret key, so
	// credentials never appear in the DDNSRecord itself.
	// +optional
	// +listType=map
	// +listMapKey=name
	ConfigFrom []ConfigFromSource `json:"configFrom,omitempty"`
}

// ConfigFromSource maps a Secret key onto one provider setting
type ConfigFromSource struct {
	// Name is the key to set in the ddns-updater config entry, e.g.
	// "token", "app_key", "password".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9._-]*$`
	Name string `json:"name"`

	// SecretKeyRef selects the Secret key holding the value
	// +kubebuilder:validation:Required
	SecretKeyRef SecretKeySelector `json:"secretKeyRef"`
}

// SecretKeySelector selects a single key of a Secret
type SecretKeySelector struct {
	// Name of the secret
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Key within the secret's data
	// +kubebuilder:validation:Required
	Key string `json:"key"`

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

	// ObservedGeneration is the generation of the DDNSRecord spec that was
	// last reconciled. If it matches metadata.generation, the spec has been
	// fully processed and the controller can skip redundant work.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

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
