// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"log"
	"time"

	"github.com/gosusnp/winston/internal/store"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/metrics/pkg/client/clientset/versioned"
)

type Collector struct {
	client        kubernetes.Interface
	metricsClient versioned.Interface
	store         *store.Store
	interval      time.Duration
}

func New(
	client kubernetes.Interface,
	metricsClient versioned.Interface,
	store *store.Store,
	interval time.Duration,
) *Collector {
	return &Collector{
		client:        client,
		metricsClient: metricsClient,
		store:         store,
		interval:      interval,
	}
}

func (c *Collector) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	log.Printf("Starting collector with interval %v", c.interval)

	// Initial collect
	c.collect(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect(ctx)
		}
	}
}

func (c *Collector) collect(ctx context.Context) {
	now := time.Now()
	podMetricsList, err := c.metricsClient.MetricsV1beta1().PodMetricses(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("Error listing pod metrics: %v", err)
		return
	}

	for _, pm := range podMetricsList.Items {
		pod, err := c.client.CoreV1().Pods(pm.Namespace).Get(ctx, pm.Name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			log.Printf("Error getting pod %s/%s: %v", pm.Namespace, pm.Name, err)
			continue
		}

		ownerKind, ownerName := c.resolveOwner(ctx, pod)

		for _, cm := range pm.Containers {
			// Find matching container spec and status
			var containerSpec *corev1.Container
			for i := range pod.Spec.Containers {
				if pod.Spec.Containers[i].Name == cm.Name {
					containerSpec = &pod.Spec.Containers[i]
					break
				}
			}

			if containerSpec == nil {
				continue
			}

			var restartCount int64
			var lastTerminationReason string
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Name == cm.Name {
					restartCount = int64(cs.RestartCount)
					if cs.LastTerminationState.Terminated != nil {
						lastTerminationReason = cs.LastTerminationState.Terminated.Reason
					}
					break
				}
			}

			cpuM := cm.Usage.Cpu().MilliValue()
			memB := cm.Usage.Memory().Value()

			meta := store.PodMeta{
				Namespace:             pod.Namespace,
				PodName:               pod.Name,
				ContainerName:         cm.Name,
				OwnerKind:             ownerKind,
				OwnerName:             ownerName,
				CPURequestM:           containerSpec.Resources.Requests.Cpu().MilliValue(),
				CPULimitM:             containerSpec.Resources.Limits.Cpu().MilliValue(),
				MemRequestB:           containerSpec.Resources.Requests.Memory().Value(),
				MemLimitB:             containerSpec.Resources.Limits.Memory().Value(),
				FirstSeenAt:           now.Unix(),
				LastSeenAt:            now.Unix(),
				LastTerminationReason: lastTerminationReason,
			}

			podID, err := c.store.UpsertPodMetadata(ctx, meta)
			if err != nil {
				log.Printf("Error upserting pod metadata for %s/%s/%s: %v", pod.Namespace, pod.Name, cm.Name, err)
				continue
			}

			err = c.store.InsertRawMetric(ctx, podID, now.Unix(), cpuM, memB, restartCount)
			if err != nil {
				log.Printf("Error inserting raw metric for podID %d: %v", podID, err)
				continue
			}
		}
	}
}

func (c *Collector) resolveOwner(ctx context.Context, pod *corev1.Pod) (string, string) {
	if len(pod.OwnerReferences) == 0 {
		return "", ""
	}

	owner := pod.OwnerReferences[0]
	kind := owner.Kind
	name := owner.Name

	if kind == "ReplicaSet" {
		rs, err := c.client.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && len(rs.OwnerReferences) > 0 {
			if rs.OwnerReferences[0].Kind == "Deployment" {
				return "Deployment", rs.OwnerReferences[0].Name
			}
		}
	}

	if kind == "Job" {
		job, err := c.client.BatchV1().Jobs(pod.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && len(job.OwnerReferences) > 0 {
			if job.OwnerReferences[0].Kind == "CronJob" {
				return "CronJob", job.OwnerReferences[0].Name
			}
		}
	}

	return kind, name
}
