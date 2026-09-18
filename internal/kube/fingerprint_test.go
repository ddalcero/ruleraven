package kube_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
	"github.com/ddalcero/ruleraven/internal/kube"
)

func baselineSnapshot() domain.Snapshot {
	return domain.Snapshot{
		Source:     domain.Source{ClusterID: "cluster-a", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "api-abc", UID: "uid-1", ResourceVersion: "10", Generation: 4, Owner: &domain.OwnerReference{Kind: "Deployment", Name: "api", UID: "owner-1"}},
		ObservedAt: time.Unix(100, 0),
		Labels:     map[string]string{"app": "api", "team": "platform"},
		Conditions: []domain.Condition{{Type: "Ready", Status: "False", Reason: "ContainersNotReady"}, {Type: "PodScheduled", Status: "True"}},
		Containers: []domain.ContainerState{{Name: "sidecar", Ready: true, State: "running"}, {Name: "api", RestartCount: 5, State: "waiting", Reason: "CrashLoopBackOff"}},
		Event:      &domain.EventState{Type: "Warning", Reason: "BackOff", Message: "back-off restarting container", Count: 7, LastObserved: time.Unix(100, 0)},
	}
}

func TestContentHashIgnoresOrderAndVolatileFields(t *testing.T) {
	want, err := kube.ContentHash(baselineSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	for seed := int64(0); seed < 100; seed++ {
		candidate := baselineSnapshot()
		candidate.ObservedAt = time.Unix(1000+seed, 0)
		candidate.Source.ResourceVersion = "different"
		candidate.Event.Count = int32(seed + 100)
		candidate.Event.LastObserved = time.Unix(2000+seed, 0)
		rng := rand.New(rand.NewSource(seed))
		rng.Shuffle(len(candidate.Conditions), func(i, j int) {
			candidate.Conditions[i], candidate.Conditions[j] = candidate.Conditions[j], candidate.Conditions[i]
		})
		rng.Shuffle(len(candidate.Containers), func(i, j int) {
			candidate.Containers[i], candidate.Containers[j] = candidate.Containers[j], candidate.Containers[i]
		})
		got, err := kube.ContentHash(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("seed %d hash = %q, want %q", seed, got, want)
		}
	}
}

func TestContentHashChangesForMaterialFacts(t *testing.T) {
	base, err := kube.ContentHash(baselineSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		mutate func(*domain.Snapshot)
	}{
		{"condition status", func(s *domain.Snapshot) { s.Conditions[0].Status = "True" }},
		{"waiting reason", func(s *domain.Snapshot) { s.Containers[1].Reason = "ImagePullBackOff" }},
		{"generation", func(s *domain.Snapshot) { s.Source.Generation++ }},
		{"owner UID", func(s *domain.Snapshot) { s.Source.Owner.UID = "owner-2" }},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			candidate := baselineSnapshot()
			tt.mutate(&candidate)
			got, err := kube.ContentHash(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if got == base {
				t.Fatalf("material change retained hash %q", got)
			}
		})
	}
}

func TestIncidentKeyUsesClusterAndUID(t *testing.T) {
	first := domain.Source{ClusterID: "cluster-a", Kind: "Pod", Namespace: "default", Name: "same", UID: "uid-1"}
	if kube.IncidentKey(first) == kube.IncidentKey(domain.Source{ClusterID: "cluster-b", Kind: "Pod", Namespace: "default", Name: "same", UID: "uid-1"}) {
		t.Fatal("cluster ID did not affect incident key")
	}
	if kube.IncidentKey(first) == kube.IncidentKey(domain.Source{ClusterID: "cluster-a", Kind: "Pod", Namespace: "default", Name: "same", UID: "uid-2"}) {
		t.Fatal("source UID did not affect incident key")
	}
	if kube.IncidentKey(first) != kube.IncidentKey(domain.Source{ClusterID: "cluster-a", Kind: "Pod", Namespace: "renamed", Name: "renamed", UID: "uid-1"}) {
		t.Fatal("mutable display identity affected incident key")
	}
}
