package kube

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type queueMetricsSpy struct {
	mu     sync.Mutex
	depths []int
}

func (s *queueMetricsSpy) SetQueueDepth(depth int) {
	s.mu.Lock()
	s.depths = append(s.depths, depth)
	s.mu.Unlock()
}

func (s *queueMetricsSpy) sawPositiveDepth() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, depth := range s.depths {
		if depth > 0 {
			return true
		}
	}
	return false
}

func TestManagerMarksCacheSyncAndEmitsQueueDepth(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "watched", UID: "pod-1"}})
	metrics := &queueMetricsSpy{}
	synced := make(chan struct{})
	reconciled := make(chan struct{}, 1)
	manager, err := NewManager(ManagerConfig{
		Client: client, Namespaces: []string{"watched"}, Resources: []string{"pods"}, Workers: 1,
		Metrics: metrics, OnCacheSync: func() { close(synced) },
		Reconcile: func(context.Context, ResourceKey) (ReconcileResult, error) {
			reconciled <- struct{}{}
			return ReconcileResult{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	select {
	case <-synced:
	case <-time.After(5 * time.Second):
		t.Fatal("cache did not synchronize")
	}
	select {
	case <-reconciled:
	case <-time.After(5 * time.Second):
		t.Fatal("queued object was not reconciled")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !metrics.sawPositiveDepth() {
		t.Fatalf("queue depths = %v, want a positive observation", metrics.depths)
	}
}
