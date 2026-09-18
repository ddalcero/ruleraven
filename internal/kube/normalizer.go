package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/ddalcero/ruleraven/internal/domain"
)

var ErrSecretResource = errors.New("Secret resources are not supported")

type NormalizerOptions struct {
	ClusterID     string
	AllowedLabels []string
	MaxBytes      int
}

type Normalizer struct {
	clusterID     string
	allowedLabels map[string]struct{}
	maxBytes      int
}

func NewNormalizer(options NormalizerOptions) *Normalizer {
	labels := make(map[string]struct{}, len(options.AllowedLabels))
	for _, label := range options.AllowedLabels {
		labels[label] = struct{}{}
	}
	return &Normalizer{clusterID: options.ClusterID, allowedLabels: labels, maxBytes: options.MaxBytes}
}

func (n *Normalizer) Normalize(object runtime.Object, observedAt time.Time) (domain.Snapshot, error) {
	var snapshot domain.Snapshot
	var err error
	switch typed := object.(type) {
	case *corev1.Pod:
		snapshot = n.normalizePod(typed, observedAt)
	case *appsv1.Deployment:
		snapshot = n.normalizeDeployment(typed, observedAt)
	case *appsv1.StatefulSet:
		snapshot = n.normalizeStatefulSet(typed, observedAt)
	case *appsv1.DaemonSet:
		snapshot = n.normalizeDaemonSet(typed, observedAt)
	case *batchv1.Job:
		snapshot = n.normalizeJob(typed, observedAt)
	case *corev1.Event:
		snapshot = n.normalizeEvent(typed, observedAt)
	case *corev1.Secret:
		return domain.Snapshot{}, ErrSecretResource
	case *unstructured.Unstructured:
		if strings.EqualFold(typed.GetKind(), "Secret") {
			return domain.Snapshot{}, ErrSecretResource
		}
		snapshot, err = n.normalizeUnstructured(typed, observedAt)
	default:
		return domain.Snapshot{}, fmt.Errorf("unsupported Kubernetes resource type %T", object)
	}
	if err != nil {
		return domain.Snapshot{}, err
	}
	hash, err := ContentHash(snapshot)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("fingerprint normalized resource: %w", err)
	}
	snapshot.ContentHash = hash
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("encode normalized resource: %w", err)
	}
	if n.maxBytes <= 0 || len(encoded) > n.maxBytes {
		return domain.Snapshot{}, fmt.Errorf("normalized resource exceeds configured size limit")
	}
	return snapshot, nil
}

func (n *Normalizer) normalizeDeployment(deployment *appsv1.Deployment, observedAt time.Time) domain.Snapshot {
	snapshot := domain.Snapshot{
		Source:     n.source(defaultString(deployment.APIVersion, "apps/v1"), defaultString(deployment.Kind, "Deployment"), &deployment.ObjectMeta),
		ObservedAt: observedAt.UTC(),
		Labels:     n.labels(deployment.Labels),
		Workload: &domain.WorkloadState{
			Desired:     replicas(deployment.Spec.Replicas),
			Current:     deployment.Status.Replicas,
			Ready:       deployment.Status.ReadyReplicas,
			Available:   deployment.Status.AvailableReplicas,
			Updated:     deployment.Status.UpdatedReplicas,
			Unavailable: deployment.Status.UnavailableReplicas,
		},
	}
	for _, condition := range deployment.Status.Conditions {
		snapshot.Conditions = append(snapshot.Conditions, domain.Condition{
			Type: string(condition.Type), Status: string(condition.Status), Reason: condition.Reason,
			LastTransitionTime: condition.LastTransitionTime.Time,
		})
	}
	sortConditions(snapshot.Conditions)
	return snapshot
}

func (n *Normalizer) normalizeStatefulSet(statefulSet *appsv1.StatefulSet, observedAt time.Time) domain.Snapshot {
	desired := replicas(statefulSet.Spec.Replicas)
	snapshot := domain.Snapshot{
		Source:     n.source(defaultString(statefulSet.APIVersion, "apps/v1"), defaultString(statefulSet.Kind, "StatefulSet"), &statefulSet.ObjectMeta),
		ObservedAt: observedAt.UTC(),
		Labels:     n.labels(statefulSet.Labels),
		Workload: &domain.WorkloadState{
			Desired:     desired,
			Current:     statefulSet.Status.CurrentReplicas,
			Ready:       statefulSet.Status.ReadyReplicas,
			Available:   statefulSet.Status.AvailableReplicas,
			Updated:     statefulSet.Status.UpdatedReplicas,
			Unavailable: nonnegativeDifference(desired, statefulSet.Status.ReadyReplicas),
		},
	}
	for _, condition := range statefulSet.Status.Conditions {
		snapshot.Conditions = append(snapshot.Conditions, domain.Condition{
			Type: string(condition.Type), Status: string(condition.Status), Reason: condition.Reason,
			LastTransitionTime: condition.LastTransitionTime.Time,
		})
	}
	sortConditions(snapshot.Conditions)
	return snapshot
}

func (n *Normalizer) normalizeDaemonSet(daemonSet *appsv1.DaemonSet, observedAt time.Time) domain.Snapshot {
	snapshot := domain.Snapshot{
		Source:     n.source(defaultString(daemonSet.APIVersion, "apps/v1"), defaultString(daemonSet.Kind, "DaemonSet"), &daemonSet.ObjectMeta),
		ObservedAt: observedAt.UTC(),
		Labels:     n.labels(daemonSet.Labels),
		Workload: &domain.WorkloadState{
			Desired:     daemonSet.Status.DesiredNumberScheduled,
			Current:     daemonSet.Status.CurrentNumberScheduled,
			Ready:       daemonSet.Status.NumberReady,
			Available:   daemonSet.Status.NumberAvailable,
			Updated:     daemonSet.Status.UpdatedNumberScheduled,
			Unavailable: daemonSet.Status.NumberUnavailable,
		},
	}
	for _, condition := range daemonSet.Status.Conditions {
		snapshot.Conditions = append(snapshot.Conditions, domain.Condition{
			Type: string(condition.Type), Status: string(condition.Status), Reason: condition.Reason,
			LastTransitionTime: condition.LastTransitionTime.Time,
		})
	}
	sortConditions(snapshot.Conditions)
	return snapshot
}

func (n *Normalizer) normalizeJob(job *batchv1.Job, observedAt time.Time) domain.Snapshot {
	backoffLimit := int32(6)
	if job.Spec.BackoffLimit != nil {
		backoffLimit = *job.Spec.BackoffLimit
	}
	snapshot := domain.Snapshot{
		Source:     n.source(defaultString(job.APIVersion, "batch/v1"), defaultString(job.Kind, "Job"), &job.ObjectMeta),
		ObservedAt: observedAt.UTC(),
		Labels:     n.labels(job.Labels),
		Job: &domain.JobState{
			Active: job.Status.Active, Succeeded: job.Status.Succeeded,
			Failed: job.Status.Failed, BackoffLimit: backoffLimit,
		},
	}
	for _, condition := range job.Status.Conditions {
		snapshot.Conditions = append(snapshot.Conditions, domain.Condition{
			Type: string(condition.Type), Status: string(condition.Status), Reason: condition.Reason,
			LastTransitionTime: condition.LastTransitionTime.Time,
		})
	}
	sortConditions(snapshot.Conditions)
	return snapshot
}

func (n *Normalizer) normalizeEvent(event *corev1.Event, observedAt time.Time) domain.Snapshot {
	lastObserved := event.EventTime.Time
	if lastObserved.IsZero() {
		lastObserved = event.LastTimestamp.Time
	}
	return domain.Snapshot{
		Source:     n.source(defaultString(event.APIVersion, "v1"), defaultString(event.Kind, "Event"), &event.ObjectMeta),
		ObservedAt: observedAt.UTC(),
		Labels:     n.labels(event.Labels),
		Event: &domain.EventState{
			Type:         event.Type,
			Reason:       event.Reason,
			Message:      RedactText(event.Message),
			RegardingUID: string(event.InvolvedObject.UID),
			Count:        event.Count,
			LastObserved: lastObserved,
		},
	}
}

func nonnegativeDifference(left, right int32) int32 {
	if left <= right {
		return 0
	}
	return left - right
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func replicas(value *int32) int32 {
	if value == nil {
		return 1
	}
	return *value
}

func (n *Normalizer) normalizePod(pod *corev1.Pod, observedAt time.Time) domain.Snapshot {
	snapshot := domain.Snapshot{
		Source:     n.source(pod.APIVersion, pod.Kind, &pod.ObjectMeta),
		ObservedAt: observedAt.UTC(),
		Labels:     n.labels(pod.Labels),
	}
	if snapshot.Source.APIVersion == "" {
		snapshot.Source.APIVersion = "v1"
	}
	if snapshot.Source.Kind == "" {
		snapshot.Source.Kind = "Pod"
	}
	for _, condition := range pod.Status.Conditions {
		snapshot.Conditions = append(snapshot.Conditions, domain.Condition{
			Type: string(condition.Type), Status: string(condition.Status), Reason: condition.Reason,
			LastTransitionTime: condition.LastTransitionTime.Time,
		})
	}
	for _, status := range pod.Status.ContainerStatuses {
		state, reason, exitCode := containerState(status.State)
		snapshot.Containers = append(snapshot.Containers, domain.ContainerState{
			Name: status.Name, Ready: status.Ready, RestartCount: status.RestartCount,
			State: state, Reason: reason, ExitCode: exitCode,
		})
	}
	sortConditions(snapshot.Conditions)
	sort.Slice(snapshot.Containers, func(i, j int) bool { return snapshot.Containers[i].Name < snapshot.Containers[j].Name })
	return snapshot
}

func (n *Normalizer) normalizeUnstructured(object *unstructured.Unstructured, observedAt time.Time) (domain.Snapshot, error) {
	snapshot := domain.Snapshot{
		Source:     n.source(object.GetAPIVersion(), object.GetKind(), object),
		ObservedAt: observedAt.UTC(),
		Labels:     n.labels(object.GetLabels()),
	}
	conditions, found, err := unstructured.NestedSlice(object.Object, "status", "conditions")
	if err != nil {
		return domain.Snapshot{}, errors.New("read normalized resource conditions")
	}
	if found {
		for _, raw := range conditions {
			condition, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			item := domain.Condition{
				Type:   stringField(condition, "type"),
				Status: stringField(condition, "status"),
				Reason: stringField(condition, "reason"),
			}
			if value := stringField(condition, "lastTransitionTime"); value != "" {
				if parsed, parseErr := time.Parse(time.RFC3339, value); parseErr == nil {
					item.LastTransitionTime = parsed
				}
			}
			snapshot.Conditions = append(snapshot.Conditions, item)
		}
	}
	sortConditions(snapshot.Conditions)
	return snapshot, nil
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func (n *Normalizer) source(apiVersion, kind string, metadata metav1.Object) domain.Source {
	source := domain.Source{
		ClusterID:       n.clusterID,
		APIVersion:      apiVersion,
		Kind:            kind,
		Namespace:       metadata.GetNamespace(),
		Name:            metadata.GetName(),
		UID:             string(metadata.GetUID()),
		ResourceVersion: metadata.GetResourceVersion(),
		Generation:      metadata.GetGeneration(),
	}
	owners := metadata.GetOwnerReferences()
	for i := range owners {
		owner := owners[i]
		if source.Owner == nil || owner.Controller != nil && *owner.Controller {
			source.Owner = &domain.OwnerReference{
				APIVersion: owner.APIVersion,
				Kind:       owner.Kind,
				Name:       owner.Name,
				UID:        string(owner.UID),
			}
		}
		if owner.Controller != nil && *owner.Controller {
			break
		}
	}
	return source
}

func (n *Normalizer) labels(values map[string]string) map[string]string {
	result := make(map[string]string)
	for key, value := range values {
		if _, ok := n.allowedLabels[key]; ok && !IsSensitiveKey(key) {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func sortConditions(conditions []domain.Condition) {
	sort.Slice(conditions, func(i, j int) bool {
		if conditions[i].Type != conditions[j].Type {
			return conditions[i].Type < conditions[j].Type
		}
		if conditions[i].Status != conditions[j].Status {
			return conditions[i].Status < conditions[j].Status
		}
		return conditions[i].Reason < conditions[j].Reason
	})
}

func containerState(state corev1.ContainerState) (string, string, int32) {
	switch {
	case state.Waiting != nil:
		return "waiting", state.Waiting.Reason, 0
	case state.Terminated != nil:
		return "terminated", state.Terminated.Reason, state.Terminated.ExitCode
	case state.Running != nil:
		return "running", "", 0
	default:
		return "unknown", "", 0
	}
}
