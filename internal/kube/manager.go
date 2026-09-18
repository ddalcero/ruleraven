package kube

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"
)

type ReconcileResult struct{ RequeueAfter time.Duration }
type ReconcileFunc func(context.Context, ResourceKey) (ReconcileResult, error)
type QueueMetrics interface{ SetQueueDepth(int) }

type ManagerConfig struct {
	Client       kubernetes.Interface
	Namespaces   []string
	Resources    []string
	Workers      int
	ResyncPeriod time.Duration
	Reconcile    ReconcileFunc
	Metrics      QueueMetrics
	OnCacheSync  func()
}

type Manager struct {
	factory     InformerFactory
	handler     *EventHandler
	queue       workqueue.RateLimitingInterface
	workers     int
	reconcile   ReconcileFunc
	metrics     QueueMetrics
	onCacheSync func()
}

type queueAdapter struct {
	queue   workqueue.RateLimitingInterface
	metrics QueueMetrics
}

func (q queueAdapter) Add(key ResourceKey) {
	q.queue.Add(key)
	q.observe()
}

func (q queueAdapter) observe() {
	if q.metrics != nil {
		q.metrics.SetQueueDepth(q.queue.Len())
	}
}

func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Client == nil || config.Reconcile == nil {
		return nil, fmt.Errorf("manager client and reconciler are required")
	}
	if config.Workers <= 0 {
		return nil, fmt.Errorf("manager workers must be positive")
	}
	queue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ruleraven")
	handler := NewEventHandler(config.Namespaces, queueAdapter{queue: queue, metrics: config.Metrics})
	factory, err := NewInformerFactory(config.Client, config.ResyncPeriod, config.Namespaces, config.Resources, handler)
	if err != nil {
		queue.ShutDown()
		return nil, err
	}
	return &Manager{
		factory: factory, handler: handler, queue: queue, workers: config.Workers,
		reconcile: config.Reconcile, metrics: config.Metrics, onCacheSync: config.OnCacheSync,
	}, nil
}

func (m *Manager) Add(key ResourceKey) {
	m.queue.Add(key)
	m.observeQueue()
}

func (m *Manager) Run(ctx context.Context) error {
	m.factory.Start(ctx.Done())
	if !m.factory.WaitForCacheSync(ctx.Done()) {
		m.handler.Stop()
		m.queue.ShutDown()
		if err := ctx.Err(); err != nil {
			return nil
		}
		return fmt.Errorf("Kubernetes informer cache synchronization failed")
	}
	if m.onCacheSync != nil {
		m.onCacheSync()
	}

	var workers sync.WaitGroup
	workers.Add(m.workers)
	for range m.workers {
		go func() {
			defer workers.Done()
			m.runWorker(ctx)
		}()
	}
	<-ctx.Done()
	m.handler.Stop()
	m.queue.ShutDownWithDrain()
	workers.Wait()
	return nil
}

func (m *Manager) runWorker(ctx context.Context) {
	for {
		item, shutdown := m.queue.Get()
		if shutdown {
			return
		}
		m.observeQueue()
		func() {
			defer m.queue.Done(item)
			key, ok := item.(ResourceKey)
			if !ok {
				m.queue.Forget(item)
				return
			}
			result, err := m.reconcile(ctx, key)
			if err != nil {
				if ctx.Err() == nil {
					m.queue.AddRateLimited(key)
					m.observeQueue()
				}
				return
			}
			m.queue.Forget(item)
			if result.RequeueAfter > 0 && ctx.Err() == nil {
				m.queue.AddAfter(key, result.RequeueAfter)
				m.observeQueue()
			}
		}()
	}
}

func (m *Manager) observeQueue() {
	if m.metrics != nil {
		m.metrics.SetQueueDepth(m.queue.Len())
	}
}
