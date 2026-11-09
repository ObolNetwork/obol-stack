package serviceoffercontroller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// Every container the controller renders sets a memory request and limit.
func TestRenderedPodsHaveMemoryLimits(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	agent.Spec.Model = "qwen3.5:9b"

	objs, err := agentManifests(agent, "litellm-key", "api-key")
	if err != nil {
		t.Fatal(err)
	}

	objs = append(objs, remoteSignerManifests(agent)...)
	objs = append(objs,
		buildStaticSiteDeployment("hash", nil),
		buildAgentIdentityRegistrationDeployment(&monetizeapi.AgentIdentity{}, "hash"),
	)

	checked := 0
	for _, o := range objs {
		if o.GetKind() != "Deployment" && o.GetKind() != "StatefulSet" {
			continue
		}

		for _, field := range []string{"initContainers", "containers"} {
			list, _, _ := unstructured.NestedSlice(o.Object, "spec", "template", "spec", field)
			for _, c := range list {
				cm := c.(map[string]any)
				req, _, _ := unstructured.NestedString(cm, "resources", "requests", "memory")
				lim, _, _ := unstructured.NestedString(cm, "resources", "limits", "memory")
				if req == "" || lim == "" {
					t.Errorf("%s %s: container %s has memory request %q, limit %q; want both", o.GetKind(), o.GetName(), cm["name"], req, lim)
				}
				checked++
			}
		}
	}

	if checked < 6 {
		t.Errorf("checked %d containers, expected the agent, its seeds, the signer and both httpd sites", checked)
	}
}
