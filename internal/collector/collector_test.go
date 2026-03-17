// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"testing"
	"time"

	"github.com/gosusnp/winston/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func TestCollect_SinglePod(t *testing.T) {
	s, err := store.Open(":memory:")
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	ns := "default"
	podName := "test-pod"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
			},
		},
	}

	pm := &v1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
		},
		Containers: []v1beta1.ContainerMetrics{
			{
				Name: "app",
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
			},
		},
	}

	client := fake.NewSimpleClientset(pod)            //nolint:staticcheck
	metricsClient := metricsfake.NewSimpleClientset() //nolint:staticcheck
	metricsClient.PrependReactor("list", "pods", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, &v1beta1.PodMetricsList{Items: []v1beta1.PodMetrics{*pm}}, nil
	})

	c := New(client, metricsClient, s, 1*time.Second)
	c.collect(ctx)

	rows, err := s.LatestRawPerContainer(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 1)

	row := rows[0]
	assert.Equal(t, ns, row.Namespace)
	assert.Equal(t, podName, row.PodName)
}

func TestCollect_DeploymentOwner(t *testing.T) {
	s, err := store.Open(":memory:")
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	ns := "default"
	podName := "test-pod-abc"
	rsName := "test-pod"
	deployName := "test-deploy"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "ReplicaSet",
					Name: rsName,
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
				},
			},
		},
	}

	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rsName,
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "Deployment",
					Name: deployName,
				},
			},
		},
	}

	pm := &v1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
		},
		Containers: []v1beta1.ContainerMetrics{
			{
				Name: "app",
			},
		},
	}

	client := fake.NewSimpleClientset(pod, rs)        //nolint:staticcheck
	metricsClient := metricsfake.NewSimpleClientset() //nolint:staticcheck
	metricsClient.PrependReactor("list", "pods", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, &v1beta1.PodMetricsList{Items: []v1beta1.PodMetrics{*pm}}, nil
	})

	c := New(client, metricsClient, s, 1*time.Second)
	c.collect(ctx)

	rows, err := s.LatestRawPerContainer(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "Deployment", rows[0].OwnerKind)
	assert.Equal(t, deployName, rows[0].OwnerName)
}

func TestCollect_DirectOwner(t *testing.T) {
	s, err := store.Open(":memory:")
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	ns := "default"
	podName := "test-sts-0"
	stsName := "test-sts"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "StatefulSet",
					Name: stsName,
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
				},
			},
		},
	}

	pm := &v1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
		},
		Containers: []v1beta1.ContainerMetrics{
			{
				Name: "app",
			},
		},
	}

	client := fake.NewSimpleClientset(pod)            //nolint:staticcheck
	metricsClient := metricsfake.NewSimpleClientset() //nolint:staticcheck
	metricsClient.PrependReactor("list", "pods", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, &v1beta1.PodMetricsList{Items: []v1beta1.PodMetrics{*pm}}, nil
	})

	c := New(client, metricsClient, s, 1*time.Second)
	c.collect(ctx)

	rows, err := s.LatestRawPerContainer(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "StatefulSet", rows[0].OwnerKind)
	assert.Equal(t, stsName, rows[0].OwnerName)
}

func TestCollect_NoOwner(t *testing.T) {
	s, err := store.Open(":memory:")
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	ns := "default"
	podName := "test-pod"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
				},
			},
		},
	}

	pm := &v1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
		},
		Containers: []v1beta1.ContainerMetrics{
			{
				Name: "app",
			},
		},
	}

	client := fake.NewSimpleClientset(pod)            //nolint:staticcheck
	metricsClient := metricsfake.NewSimpleClientset() //nolint:staticcheck
	metricsClient.PrependReactor("list", "pods", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, &v1beta1.PodMetricsList{Items: []v1beta1.PodMetrics{*pm}}, nil
	})

	c := New(client, metricsClient, s, 1*time.Second)
	c.collect(ctx)

	rows, err := s.LatestRawPerContainer(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "", rows[0].OwnerKind)
	assert.Equal(t, "", rows[0].OwnerName)
}

func TestCollect_DeletedPod(t *testing.T) {
	s, err := store.Open(":memory:")
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	ns := "default"
	podName := "test-pod"

	pm := &v1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
		},
		Containers: []v1beta1.ContainerMetrics{
			{
				Name: "app",
			},
		},
	}

	client := fake.NewSimpleClientset()               //nolint:staticcheck
	metricsClient := metricsfake.NewSimpleClientset() //nolint:staticcheck
	metricsClient.PrependReactor("list", "pods", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, &v1beta1.PodMetricsList{Items: []v1beta1.PodMetrics{*pm}}, nil
	})

	c := New(client, metricsClient, s, 1*time.Second)
	c.collect(ctx)

	rows, err := s.LatestRawPerContainer(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 0)
}

func TestCollect_ContextCancel(t *testing.T) {
	s, err := store.Open(":memory:")
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	client := fake.NewSimpleClientset()               //nolint:staticcheck
	metricsClient := metricsfake.NewSimpleClientset() //nolint:staticcheck

	c := New(client, metricsClient, s, 1*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
