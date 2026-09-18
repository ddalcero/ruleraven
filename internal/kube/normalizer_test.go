package kube_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/ddalcero/ruleraven/internal/kube"
)

func fixturePath(name string) string {
	return filepath.Join("..", "..", "test", "testdata", "kube", name)
}

func loadPod(t *testing.T, name string) *corev1.Pod {
	t.Helper()
	data, err := os.ReadFile(fixturePath(name))
	if err != nil {
		t.Fatal(err)
	}
	var pod corev1.Pod
	if err := yaml.Unmarshal(data, &pod); err != nil {
		t.Fatal(err)
	}
	return &pod
}

func loadWorkload(t *testing.T, index int, output any) {
	t.Helper()
	data, err := os.ReadFile(fixturePath("workloads.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	documents := strings.Split(string(data), "\n---\n")
	if index >= len(documents) {
		t.Fatalf("workload fixture index %d does not exist", index)
	}
	if err := yaml.Unmarshal([]byte(documents[index]), output); err != nil {
		t.Fatal(err)
	}
}

func assertNoLeak(t *testing.T, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"LEAK_SENTINEL_DO_NOT_COPY", "private-values", "secretKeyRef", "managedFields", "command", "unsafe-label"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestNormalizePod(t *testing.T) {
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", AllowedLabels: []string{"app", "team"}, MaxBytes: 4096})
	snapshot, err := normalizer.Normalize(loadPod(t, "pod-crashloop.yaml"), time.Unix(100, 0))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if snapshot.Source.UID != "pod-uid" || snapshot.Source.Owner == nil || snapshot.Source.Owner.UID != "owner-uid" {
		t.Fatalf("unexpected identity: %#v", snapshot.Source)
	}
	if len(snapshot.Labels) != 2 || snapshot.Labels["app"] != "api" {
		t.Fatalf("unexpected labels: %#v", snapshot.Labels)
	}
	if len(snapshot.Conditions) != 2 || snapshot.Conditions[0].Type != "PodScheduled" || snapshot.Conditions[1].Type != "Ready" {
		t.Fatalf("conditions not sorted: %#v", snapshot.Conditions)
	}
	if len(snapshot.Containers) != 2 || snapshot.Containers[0].Name != "api" || snapshot.Containers[0].Reason != "CrashLoopBackOff" || snapshot.Containers[0].RestartCount != 5 {
		t.Fatalf("unexpected containers: %#v", snapshot.Containers)
	}
	assertNoLeak(t, snapshot)
}

func TestNormalizeDeployment(t *testing.T) {
	var deployment appsv1.Deployment
	loadWorkload(t, 0, &deployment)
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", AllowedLabels: []string{"app"}, MaxBytes: 4096})
	snapshot, err := normalizer.Normalize(&deployment, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if snapshot.Workload == nil || snapshot.Workload.Desired != 5 || snapshot.Workload.Current != 4 || snapshot.Workload.Ready != 2 || snapshot.Workload.Available != 2 || snapshot.Workload.Updated != 3 || snapshot.Workload.Unavailable != 3 {
		t.Fatalf("unexpected workload state: %#v", snapshot.Workload)
	}
	if len(snapshot.Conditions) != 2 || snapshot.Conditions[0].Type != "Available" || snapshot.Conditions[1].Type != "Progressing" {
		t.Fatalf("conditions not sorted: %#v", snapshot.Conditions)
	}
	assertNoLeak(t, snapshot)
}

func TestNormalizeStatefulSet(t *testing.T) {
	var statefulSet appsv1.StatefulSet
	loadWorkload(t, 1, &statefulSet)
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", AllowedLabels: []string{"app"}, MaxBytes: 4096})
	snapshot, err := normalizer.Normalize(&statefulSet, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if snapshot.Workload == nil || snapshot.Workload.Desired != 3 || snapshot.Workload.Current != 2 || snapshot.Workload.Ready != 1 || snapshot.Workload.Available != 1 || snapshot.Workload.Updated != 2 || snapshot.Workload.Unavailable != 2 {
		t.Fatalf("unexpected workload state: %#v", snapshot.Workload)
	}
	assertNoLeak(t, snapshot)
}

func TestNormalizeDaemonSet(t *testing.T) {
	var daemonSet appsv1.DaemonSet
	loadWorkload(t, 2, &daemonSet)
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", AllowedLabels: []string{"app"}, MaxBytes: 4096})
	snapshot, err := normalizer.Normalize(&daemonSet, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if snapshot.Workload == nil || snapshot.Workload.Desired != 6 || snapshot.Workload.Current != 5 || snapshot.Workload.Ready != 4 || snapshot.Workload.Available != 3 || snapshot.Workload.Updated != 5 || snapshot.Workload.Unavailable != 3 {
		t.Fatalf("unexpected workload state: %#v", snapshot.Workload)
	}
	if snapshot.Source.Kind != "DaemonSet" || snapshot.Source.UID != "daemonset-uid" {
		t.Fatalf("unexpected identity: %#v", snapshot.Source)
	}
	assertNoLeak(t, snapshot)
}

func TestNormalizeJob(t *testing.T) {
	data, err := os.ReadFile(fixturePath("job-failed.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var job batchv1.Job
	if err := yaml.Unmarshal(data, &job); err != nil {
		t.Fatal(err)
	}
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", AllowedLabels: []string{"app"}, MaxBytes: 4096})
	snapshot, err := normalizer.Normalize(&job, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if snapshot.Job == nil || snapshot.Job.Active != 0 || snapshot.Job.Succeeded != 1 || snapshot.Job.Failed != 4 || snapshot.Job.BackoffLimit != 4 {
		t.Fatalf("unexpected job state: %#v", snapshot.Job)
	}
	if len(snapshot.Conditions) != 2 || snapshot.Conditions[0].Type != "Complete" || snapshot.Conditions[1].Type != "Failed" {
		t.Fatalf("conditions not sorted: %#v", snapshot.Conditions)
	}
	assertNoLeak(t, snapshot)
}

func TestNormalizeEvent(t *testing.T) {
	data, err := os.ReadFile(fixturePath("warning-event.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var event corev1.Event
	if err := yaml.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", AllowedLabels: []string{"app"}, MaxBytes: 4096})
	snapshot, err := normalizer.Normalize(&event, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if snapshot.Event == nil || snapshot.Event.Type != "Warning" || snapshot.Event.Reason != "BackOff" || snapshot.Event.RegardingUID != "pod-uid" || snapshot.Event.Count != 7 {
		t.Fatalf("unexpected event state: %#v", snapshot.Event)
	}
	if !strings.Contains(snapshot.Event.Message, "[REDACTED]") {
		t.Fatalf("event message was not redacted: %q", snapshot.Event.Message)
	}
	assertNoLeak(t, snapshot)
}

func TestNormalizeGenericUnstructuredAllowlistsIdentityAndConditions(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.io/v1",
		"kind":       "Widget",
		"metadata": map[string]any{
			"name": "widget-a", "namespace": "default", "uid": "widget-uid",
			"labels":      map[string]any{"app": "widget", "secret-token": "LEAK_SENTINEL_DO_NOT_COPY", "unsafe-label": "LEAK_SENTINEL_DO_NOT_COPY"},
			"annotations": map[string]any{"token": "LEAK_SENTINEL_DO_NOT_COPY"},
		},
		"spec": map[string]any{"password": "LEAK_SENTINEL_DO_NOT_COPY"},
		"status": map[string]any{
			"credential": "LEAK_SENTINEL_DO_NOT_COPY",
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "False", "reason": "Reconciling", "message": "LEAK_SENTINEL_DO_NOT_COPY"},
				map[string]any{"type": "Accepted", "status": "True"},
			},
		},
	}}
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", AllowedLabels: []string{"app", "secret-token"}, MaxBytes: 4096})
	snapshot, err := normalizer.Normalize(object, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if snapshot.Source.Kind != "Widget" || snapshot.Source.UID != "widget-uid" || snapshot.Labels["app"] != "widget" {
		t.Fatalf("unexpected generic identity: %#v", snapshot)
	}
	if len(snapshot.Conditions) != 2 || snapshot.Conditions[0].Type != "Accepted" || snapshot.Conditions[1].Reason != "Reconciling" {
		t.Fatalf("unexpected generic conditions: %#v", snapshot.Conditions)
	}
	assertNoLeak(t, snapshot)
}

func TestNormalizeRejectsSecrets(t *testing.T) {
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", MaxBytes: 4096})
	objects := []struct {
		name   string
		object any
	}{
		{name: "typed", object: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "LEAK_SENTINEL_DO_NOT_COPY"}, Data: map[string][]byte{"token": []byte("LEAK_SENTINEL_DO_NOT_COPY")}}},
		{name: "unstructured", object: &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret", "data": map[string]any{"token": "LEAK_SENTINEL_DO_NOT_COPY"}}}},
	}
	for _, tt := range objects {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			switch object := tt.object.(type) {
			case *corev1.Secret:
				_, err = normalizer.Normalize(object, time.Time{})
			case *unstructured.Unstructured:
				_, err = normalizer.Normalize(object, time.Time{})
			}
			if !errors.Is(err, kube.ErrSecretResource) {
				t.Fatalf("Normalize() error = %v, want ErrSecretResource", err)
			}
			if strings.Contains(err.Error(), "LEAK_SENTINEL_DO_NOT_COPY") {
				t.Fatalf("error leaked secret: %v", err)
			}
		})
	}
}

func TestNormalizeEnforcesMaximumSize(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.io/v1", "kind": "Widget",
		"metadata": map[string]any{"name": strings.Repeat("x", 200), "uid": "widget-uid"},
	}}
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", MaxBytes: 128})
	_, err := normalizer.Normalize(object, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("Normalize() error = %v, want size limit", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("x", 20)) {
		t.Fatalf("size error leaked object identity: %v", err)
	}
}

func TestNormalizeErrorsDoNotLeakInput(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.io/v1", "kind": "Widget",
		"status": map[string]any{"conditions": "LEAK_SENTINEL_DO_NOT_COPY"},
	}}
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", MaxBytes: 4096})
	_, err := normalizer.Normalize(object, time.Time{})
	if err == nil {
		t.Fatal("Normalize() error = nil, want malformed conditions error")
	}
	if strings.Contains(err.Error(), "LEAK_SENTINEL_DO_NOT_COPY") {
		t.Fatalf("error leaked input: %v", err)
	}
}

func TestNormalizeRejectsUnsupportedTypedResource(t *testing.T) {
	normalizer := kube.NewNormalizer(kube.NormalizerOptions{ClusterID: "cluster-a", MaxBytes: 4096})
	_, err := normalizer.Normalize(&corev1.ConfigMap{}, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "unsupported Kubernetes resource type") {
		t.Fatalf("Normalize() error = %v", err)
	}
}
