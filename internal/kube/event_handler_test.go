package kube_test

import (
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	ravenkube "github.com/ddalcero/ruleraven/internal/kube"
)

type recordingQueue struct {
	mu   sync.Mutex
	keys []ravenkube.ResourceKey
}

func (q *recordingQueue) Add(key ravenkube.ResourceKey) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.keys = append(q.keys, key)
}
func (q *recordingQueue) values() []ravenkube.ResourceKey {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]ravenkube.ResourceKey(nil), q.keys...)
}

func TestEventHandlerFiltersNamespacesBeforeEnqueue(t *testing.T) {
	queue := &recordingQueue{}
	handler := ravenkube.NewEventHandler([]string{"watched"}, queue)
	watched := &corev1.Pod{TypeMeta: metav1.TypeMeta{Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Namespace: "watched", Name: "yes", UID: types.UID("yes-uid")}}
	ignored := &corev1.Pod{TypeMeta: metav1.TypeMeta{Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Namespace: "ignored", Name: "no", UID: types.UID("no-uid")}}

	handler.OnAdd(ignored, false)
	handler.OnUpdate(ignored, ignored.DeepCopy())
	handler.OnAdd(watched, false)
	handler.OnDelete(cache.DeletedFinalStateUnknown{Key: "watched/yes", Obj: watched})

	keys := queue.values()
	if len(keys) != 2 {
		t.Fatalf("queued keys = %#v, want two watched keys", keys)
	}
	for _, key := range keys {
		if key.Namespace != "watched" || key.UID != "yes-uid" || key.Kind != "Pod" {
			t.Fatalf("queued key = %#v", key)
		}
	}
}

func TestEventHandlerStopsAcceptingWork(t *testing.T) {
	queue := &recordingQueue{}
	handler := ravenkube.NewEventHandler(nil, queue)
	handler.Stop()
	handler.OnAdd(&corev1.Pod{TypeMeta: metav1.TypeMeta{Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Name: "late", UID: "uid"}}, false)
	if got := len(queue.values()); got != 0 {
		t.Fatalf("queued keys after Stop = %d, want 0", got)
	}
}
