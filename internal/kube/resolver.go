package kube

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
)

var ErrNotFound = errors.New("Kubernetes resource not found")

type Resolver interface {
	Resolve(context.Context, ResourceKey) (runtime.Object, error)
}

type ClientResolver struct{ client kubernetes.Interface }

func NewResolver(client kubernetes.Interface) (*ClientResolver, error) {
	if client == nil {
		return nil, fmt.Errorf("Kubernetes client is required")
	}
	return &ClientResolver{client: client}, nil
}

func (r *ClientResolver) Resolve(ctx context.Context, key ResourceKey) (runtime.Object, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	options := metav1.GetOptions{}
	var object runtime.Object
	var err error
	switch key.Kind {
	case "Pod":
		object, err = r.client.CoreV1().Pods(key.Namespace).Get(ctx, key.Name, options)
	case "Event":
		object, err = r.client.CoreV1().Events(key.Namespace).Get(ctx, key.Name, options)
	case "Deployment":
		object, err = r.client.AppsV1().Deployments(key.Namespace).Get(ctx, key.Name, options)
	case "StatefulSet":
		object, err = r.client.AppsV1().StatefulSets(key.Namespace).Get(ctx, key.Name, options)
	case "DaemonSet":
		object, err = r.client.AppsV1().DaemonSets(key.Namespace).Get(ctx, key.Name, options)
	case "Job":
		object, err = r.client.BatchV1().Jobs(key.Namespace).Get(ctx, key.Name, options)
	default:
		return nil, fmt.Errorf("unsupported Kubernetes resource kind %q", key.Kind)
	}
	if apierrors.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve %s %s/%s: %w", key.Kind, key.Namespace, key.Name, err)
	}
	metadata, ok := object.(metav1.Object)
	if !ok || string(metadata.GetUID()) != key.UID {
		return nil, ErrNotFound
	}
	return object, nil
}
