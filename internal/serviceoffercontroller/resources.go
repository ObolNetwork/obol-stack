package serviceoffercontroller

// Memory for the Hermes gateway container, here and in the default agent
// (internal/hermes): about 0.5 GiB at rest, more while running tools.
const (
	hermesMemoryRequest = "256Mi"
	hermesMemoryLimit   = "2Gi"
)

// memoryResources is a container's memory request and limit. Every pod
// the controller renders sets both (TestRenderedPodsHaveMemoryLimits), so
// none can grow until the node runs out. No CPU limits.
func memoryResources(request, limit string) map[string]any {
	return map[string]any{
		"requests": map[string]any{"memory": request},
		"limits":   map[string]any{"memory": limit},
	}
}
