package kube_test

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	ravenkube "github.com/ddalcero/ruleraven/internal/kube"
)

func TestInformerFactoryWatchesOnlySelectedResourcesWithoutMutation(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "watched", Name: "pod", UID: types.UID("pod-uid")}})
	queue := &recordingQueue{}
	handler := ravenkube.NewEventHandler([]string{"watched"}, queue)
	factory, err := ravenkube.NewInformerFactory(client, 0, []string{"watched"}, []string{"pods"}, handler)
	if err != nil {
		t.Fatalf("NewInformerFactory: %v", err)
	}
	stop := make(chan struct{})
	factory.Start(stop)
	if !factory.WaitForCacheSync(stop) {
		close(stop)
		t.Fatal("cache did not synchronize")
	}
	close(stop)

	deadline := time.Now().Add(time.Second)
	for len(queue.values()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := queue.values(); len(got) != 1 || got[0].Kind != "Pod" || got[0].Namespace != "watched" {
		t.Fatalf("queued keys = %#v", got)
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource != "pods" {
			t.Fatalf("unexpected watched resource action: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
		switch action.GetVerb() {
		case "list", "watch", "get":
		default:
			t.Fatalf("mutation action observed: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
}
