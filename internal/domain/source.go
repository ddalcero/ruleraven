package domain

// Source identifies the observed Kubernetes object. Names are display metadata;
// ClusterID and UID form stable identity.
type Source struct {
	ClusterID       string          `json:"clusterId"`
	APIVersion      string          `json:"apiVersion"`
	Kind            string          `json:"kind"`
	Namespace       string          `json:"namespace,omitempty"`
	Name            string          `json:"name"`
	UID             string          `json:"uid"`
	ResourceVersion string          `json:"resourceVersion,omitempty"`
	Generation      int64           `json:"generation,omitempty"`
	Owner           *OwnerReference `json:"owner,omitempty"`
}

type OwnerReference struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}
