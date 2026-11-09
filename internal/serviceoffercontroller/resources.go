package serviceoffercontroller

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
)

// Memory for the Hermes gateway container, here and in the default agent
// (internal/hermes): about 0.5 GiB at rest, more while running tools.
const (
	hermesMemoryRequest = "256Mi"
	hermesMemoryLimit   = "2Gi"
)

// resourcesKey is a container's "resources" field.
const resourcesKey = "resources"

// memoryResources is a container's memory request and limit. Every pod
// the controller renders sets both (TestRenderedPodsHaveMemoryLimits), so
// none can grow until the node runs out. No CPU limits.
func memoryResources(request, limit string) map[string]any {
	r := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(request)},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(limit)},
	}

	out, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&r)
	if err != nil {
		panic(err) // a ResourceRequirements always converts
	}

	return out
}
