package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

const (
	contentHashSchema = "ruleraven.snapshot.v1\x00"
	incidentKeySchema = "ruleraven.incident.v1\x00"
)

type keyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type canonicalSource struct {
	ClusterID  string                 `json:"clusterId"`
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	UID        string                 `json:"uid"`
	Generation int64                  `json:"generation"`
	Owner      *domain.OwnerReference `json:"owner,omitempty"`
}

type canonicalSnapshot struct {
	Source     canonicalSource         `json:"source"`
	Labels     []keyValue              `json:"labels,omitempty"`
	Conditions []domain.Condition      `json:"conditions,omitempty"`
	Containers []domain.ContainerState `json:"containers,omitempty"`
	Workload   *domain.WorkloadState   `json:"workload,omitempty"`
	Job        *domain.JobState        `json:"job,omitempty"`
	Event      *canonicalEvent         `json:"event,omitempty"`
}

type canonicalEvent struct {
	Type         string `json:"type"`
	Reason       string `json:"reason"`
	Message      string `json:"message,omitempty"`
	RegardingUID string `json:"regardingUid,omitempty"`
}

// ContentHash returns a versioned SHA-256 fingerprint of material snapshot facts.
func ContentHash(snapshot domain.Snapshot) (string, error) {
	canonical := canonicalSnapshot{
		Source: canonicalSource{
			ClusterID: snapshot.Source.ClusterID, APIVersion: snapshot.Source.APIVersion,
			Kind: snapshot.Source.Kind, UID: snapshot.Source.UID,
			Generation: snapshot.Source.Generation, Owner: snapshot.Source.Owner,
		},
		Workload: snapshot.Workload,
		Job:      snapshot.Job,
	}
	for key, value := range snapshot.Labels {
		canonical.Labels = append(canonical.Labels, keyValue{Key: key, Value: value})
	}
	sort.Slice(canonical.Labels, func(i, j int) bool { return canonical.Labels[i].Key < canonical.Labels[j].Key })

	canonical.Conditions = append(canonical.Conditions, snapshot.Conditions...)
	// Transition timestamps are intentionally removed; rule outputs capture
	// duration boundaries rather than making every timestamp a new snapshot.
	for i := range canonical.Conditions {
		canonical.Conditions[i].LastTransitionTime = time.Time{}
	}
	sort.Slice(canonical.Conditions, func(i, j int) bool {
		a, b := canonical.Conditions[i], canonical.Conditions[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Status != b.Status {
			return a.Status < b.Status
		}
		if a.Reason != b.Reason {
			return a.Reason < b.Reason
		}
		return a.Message < b.Message
	})

	canonical.Containers = append(canonical.Containers, snapshot.Containers...)
	sort.Slice(canonical.Containers, func(i, j int) bool {
		a, b := canonical.Containers[i], canonical.Containers[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.State != b.State {
			return a.State < b.State
		}
		return a.Reason < b.Reason
	})
	if snapshot.Event != nil {
		canonical.Event = &canonicalEvent{Type: snapshot.Event.Type, Reason: snapshot.Event.Reason, Message: snapshot.Event.Message, RegardingUID: snapshot.Event.RegardingUID}
	}

	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte(contentHashSchema), encoded...))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// IncidentKey returns a stable key based only on cluster and immutable source UID.
func IncidentKey(source domain.Source) string {
	digest := sha256.Sum256([]byte(incidentKeySchema + source.ClusterID + "\x00" + source.UID))
	return "incident:v1:" + hex.EncodeToString(digest[:])
}
