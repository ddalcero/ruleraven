package kube

import (
	"fmt"
	"strings"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/ddalcero/ruleraven/internal/domain"
)

// ResourceKey is the immutable queue payload produced by informer callbacks.
// It contains identity only; workers fetch current state after dequeueing.
type ResourceKey struct {
	Kind      string
	Namespace string
	Name      string
	UID       string
}

func (k ResourceKey) Source(clusterID string) domain.Source {
	return domain.Source{ClusterID: clusterID, Kind: k.Kind, Namespace: k.Namespace, Name: k.Name, UID: k.UID}
}

type KeyQueue interface{ Add(ResourceKey) }

type EventHandler struct {
	namespaces map[string]struct{}
	queue      KeyQueue
	accepting  chan struct{}
	stopOnce   sync.Once
}

func NewEventHandler(namespaces []string, queue KeyQueue) *EventHandler {
	allowed := make(map[string]struct{}, len(namespaces))
	for _, namespace := range namespaces {
		if value := strings.TrimSpace(namespace); value != "" {
			allowed[value] = struct{}{}
		}
	}
	return &EventHandler{namespaces: allowed, queue: queue, accepting: make(chan struct{})}
}

func (h *EventHandler) OnAdd(object any, _ bool) { h.enqueue(object) }
func (h *EventHandler) OnUpdate(_, current any)  { h.enqueue(current) }
func (h *EventHandler) OnDelete(object any) {
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok {
		object = tombstone.Obj
	}
	h.enqueue(object)
}

func (h *EventHandler) Stop() {
	h.stopOnce.Do(func() { close(h.accepting) })
}

func (h *EventHandler) enqueue(object any) {
	select {
	case <-h.accepting:
		return
	default:
	}
	if h.queue == nil {
		return
	}
	metadata, ok := object.(metav1.Object)
	if !ok || metadata.GetName() == "" || metadata.GetUID() == "" {
		return
	}
	if len(h.namespaces) > 0 {
		if _, watched := h.namespaces[metadata.GetNamespace()]; !watched {
			return
		}
	}
	kind := objectKind(object)
	if kind == "" {
		return
	}
	h.queue.Add(ResourceKey{Kind: kind, Namespace: metadata.GetNamespace(), Name: metadata.GetName(), UID: string(metadata.GetUID())})
}

func objectKind(object any) string {
	if runtimeObject, ok := object.(runtime.Object); ok {
		if kind := runtimeObject.GetObjectKind().GroupVersionKind().Kind; kind != "" {
			return kind
		}
	}
	switch object.(type) {
	case *corev1.Pod:
		return "Pod"
	case *corev1.Event:
		return "Event"
	case *appsv1.Deployment:
		return "Deployment"
	case *appsv1.StatefulSet:
		return "StatefulSet"
	case *appsv1.DaemonSet:
		return "DaemonSet"
	case *batchv1.Job:
		return "Job"
	default:
		return ""
	}
}

func (k ResourceKey) Validate() error {
	if k.Kind == "" || k.Name == "" || k.UID == "" {
		return fmt.Errorf("resource kind, name, and UID are required")
	}
	return nil
}
