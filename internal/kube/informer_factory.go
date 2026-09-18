package kube

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// InformerFactory exposes only lifecycle operations needed by Manager.
type InformerFactory interface {
	Start(<-chan struct{})
	WaitForCacheSync(<-chan struct{}) bool
}

type sharedInformerFactory struct {
	factories []informers.SharedInformerFactory
}

var supportedResources = map[string]func(informers.SharedInformerFactory) cache.SharedIndexInformer{
	"pods": func(factory informers.SharedInformerFactory) cache.SharedIndexInformer {
		return factory.Core().V1().Pods().Informer()
	},
	"events": func(factory informers.SharedInformerFactory) cache.SharedIndexInformer {
		return factory.Core().V1().Events().Informer()
	},
	"deployments": func(factory informers.SharedInformerFactory) cache.SharedIndexInformer {
		return factory.Apps().V1().Deployments().Informer()
	},
	"statefulsets": func(factory informers.SharedInformerFactory) cache.SharedIndexInformer {
		return factory.Apps().V1().StatefulSets().Informer()
	},
	"daemonsets": func(factory informers.SharedInformerFactory) cache.SharedIndexInformer {
		return factory.Apps().V1().DaemonSets().Informer()
	},
	"jobs": func(factory informers.SharedInformerFactory) cache.SharedIndexInformer {
		return factory.Batch().V1().Jobs().Informer()
	},
}

func NewInformerFactory(client kubernetes.Interface, resync time.Duration, namespaces, resources []string, handler cache.ResourceEventHandler) (InformerFactory, error) {
	if client == nil {
		return nil, fmt.Errorf("Kubernetes client is required")
	}
	if handler == nil {
		return nil, fmt.Errorf("resource event handler is required")
	}
	selected, err := normalizeResources(resources)
	if err != nil {
		return nil, err
	}
	namespaces = normalizeNamespaces(namespaces)
	factories := make([]informers.SharedInformerFactory, 0, max(1, len(namespaces)))
	if len(namespaces) == 0 {
		factories = append(factories, informers.NewSharedInformerFactory(client, resync))
	} else {
		for _, namespace := range namespaces {
			factories = append(factories, informers.NewSharedInformerFactoryWithOptions(client, resync, informers.WithNamespace(namespace)))
		}
	}
	for _, factory := range factories {
		for _, resource := range selected {
			if _, err := supportedResources[resource](factory).AddEventHandler(handler); err != nil {
				return nil, fmt.Errorf("register Kubernetes %s informer handler: %w", resource, err)
			}
		}
	}
	return &sharedInformerFactory{factories: factories}, nil
}

func (f *sharedInformerFactory) Start(stop <-chan struct{}) {
	for _, factory := range f.factories {
		factory.Start(stop)
	}
}

func (f *sharedInformerFactory) WaitForCacheSync(stop <-chan struct{}) bool {
	for _, factory := range f.factories {
		for _, synced := range factory.WaitForCacheSync(stop) {
			if !synced {
				return false
			}
		}
	}
	return true
}

func normalizeResources(resources []string) ([]string, error) {
	if len(resources) == 0 {
		return nil, fmt.Errorf("at least one Kubernetes resource must be configured")
	}
	seen := make(map[string]struct{}, len(resources))
	selected := make([]string, 0, len(resources))
	for _, resource := range resources {
		resource = strings.ToLower(strings.TrimSpace(resource))
		if _, supported := supportedResources[resource]; !supported {
			return nil, fmt.Errorf("unsupported Kubernetes resource %q", resource)
		}
		if _, duplicate := seen[resource]; duplicate {
			return nil, fmt.Errorf("duplicate Kubernetes resource %q", resource)
		}
		seen[resource] = struct{}{}
		selected = append(selected, resource)
	}
	sort.Strings(selected)
	return selected, nil
}

func normalizeNamespaces(namespaces []string) []string {
	seen := make(map[string]struct{}, len(namespaces))
	result := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		namespace = strings.TrimSpace(namespace)
		if namespace == "" {
			continue
		}
		if _, duplicate := seen[namespace]; duplicate {
			continue
		}
		seen[namespace] = struct{}{}
		result = append(result, namespace)
	}
	sort.Strings(result)
	return result
}
