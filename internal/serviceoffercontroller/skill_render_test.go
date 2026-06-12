package serviceoffercontroller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestBuildSkillBundleDeployment_RestrictedPSS(t *testing.T) {
	offer := skillTestOffer(nil)
	dep := buildSkillBundleDeployment(offer)

	podSpec, found, err := unstructured.NestedMap(dep.Object, "spec", "template", "spec")
	if err != nil || !found {
		t.Fatalf("pod spec missing: found=%v err=%v", found, err)
	}

	// Pod-level Restricted PSS — same assertions as the skill catalog /
	// agentidentity httpd renders.
	sc, ok := podSpec["securityContext"].(map[string]any)
	if !ok {
		t.Fatal("pod securityContext missing")
	}
	if sc["runAsNonRoot"] != true {
		t.Errorf("runAsNonRoot = %v, want true", sc["runAsNonRoot"])
	}
	if sc["runAsUser"] != int64(1000) || sc["runAsGroup"] != int64(1000) || sc["fsGroup"] != int64(1000) {
		t.Errorf("uid/gid/fsGroup = %v/%v/%v, want 1000", sc["runAsUser"], sc["runAsGroup"], sc["fsGroup"])
	}
	seccomp, _ := sc["seccompProfile"].(map[string]any)
	if seccomp == nil || seccomp["type"] != "RuntimeDefault" {
		t.Errorf("seccompProfile = %v, want RuntimeDefault", sc["seccompProfile"])
	}

	containers, _ := podSpec["containers"].([]any)
	if len(containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(containers))
	}
	container := containers[0].(map[string]any)
	if container["image"] != "busybox:1.36" {
		t.Errorf("image = %v, want busybox:1.36", container["image"])
	}

	csc, ok := container["securityContext"].(map[string]any)
	if !ok {
		t.Fatal("container securityContext missing")
	}
	if csc["allowPrivilegeEscalation"] != false {
		t.Errorf("allowPrivilegeEscalation = %v, want false", csc["allowPrivilegeEscalation"])
	}
	caps, _ := csc["capabilities"].(map[string]any)
	drop, _ := caps["drop"].([]any)
	if len(drop) != 1 || drop[0] != "ALL" {
		t.Errorf("capabilities.drop = %v, want [ALL]", drop)
	}

	command, _ := container["command"].([]any)
	wantCommand := []any{"httpd", "-f", "-p", "8080", "-h", "/www"}
	if len(command) != len(wantCommand) {
		t.Fatalf("command = %v, want %v", command, wantCommand)
	}
	for i := range wantCommand {
		if command[i] != wantCommand[i] {
			t.Errorf("command[%d] = %v, want %v", i, command[i], wantCommand[i])
		}
	}

	resources, _ := container["resources"].(map[string]any)
	requests, _ := resources["requests"].(map[string]any)
	limits, _ := resources["limits"].(map[string]any)
	if requests["cpu"] != "5m" || requests["memory"] != "8Mi" {
		t.Errorf("requests = %v, want 5m/8Mi", requests)
	}
	if limits["cpu"] != "50m" || limits["memory"] != "32Mi" {
		t.Errorf("limits = %v, want 50m/32Mi", limits)
	}
}

func TestBuildSkillBundleDeployment_VolumesWireBothConfigMaps(t *testing.T) {
	offer := skillTestOffer(nil)
	dep := buildSkillBundleDeployment(offer)

	volumes, found, err := unstructured.NestedSlice(dep.Object, "spec", "template", "spec", "volumes")
	if err != nil || !found || len(volumes) != 2 {
		t.Fatalf("volumes = %v (found=%v err=%v), want 2 entries", volumes, found, err)
	}

	content := volumes[0].(map[string]any)
	if content["name"] != "content" {
		t.Fatalf("volumes[0] = %v, want content", content["name"])
	}
	projected, _ := content["projected"].(map[string]any)
	sources, _ := projected["sources"].([]any)
	if len(sources) != 2 {
		t.Fatalf("projected sources = %d, want 2 (bundle CM + meta CM)", len(sources))
	}
	bundleCM := sources[0].(map[string]any)["configMap"].(map[string]any)
	if bundleCM["name"] != offer.Spec.Skill.BundleConfigMap {
		t.Errorf("bundle source CM = %v, want %s", bundleCM["name"], offer.Spec.Skill.BundleConfigMap)
	}
	bundleItems, _ := bundleCM["items"].([]any)
	if len(bundleItems) != 1 {
		t.Fatalf("bundle items = %v", bundleItems)
	}
	item := bundleItems[0].(map[string]any)
	if item["key"] != monetizeapi.SkillBundleKey || item["path"] != monetizeapi.SkillBundleKey {
		t.Errorf("bundle item = %v, want %s→%s", item, monetizeapi.SkillBundleKey, monetizeapi.SkillBundleKey)
	}
	metaCM := sources[1].(map[string]any)["configMap"].(map[string]any)
	if metaCM["name"] != skillBundleMetaName(offer.Name) {
		t.Errorf("meta source CM = %v, want %s", metaCM["name"], skillBundleMetaName(offer.Name))
	}

	httpdconf := volumes[1].(map[string]any)
	if httpdconf["name"] != "httpdconf" {
		t.Fatalf("volumes[1] = %v, want httpdconf", httpdconf["name"])
	}

	mounts, _, _ := unstructured.NestedSlice(dep.Object, "spec", "template", "spec", "containers")
	container := mounts[0].(map[string]any)
	volumeMounts, _ := container["volumeMounts"].([]any)
	var sawContent, sawConf bool
	for _, vm := range volumeMounts {
		m := vm.(map[string]any)
		switch m["name"] {
		case "content":
			sawContent = m["mountPath"] == "/www" && m["readOnly"] == true
		case "httpdconf":
			sawConf = m["mountPath"] == "/etc/httpd.conf" && m["subPath"] == "httpd.conf"
		}
	}
	if !sawContent {
		t.Error("content volume must be mounted read-only at /www")
	}
	if !sawConf {
		t.Error("httpd.conf must be subPath-mounted at /etc/httpd.conf")
	}
}

func TestBuildSkillBundleService_SelectorMatchesDeploymentLabels(t *testing.T) {
	offer := skillTestOffer(nil)
	svc := buildSkillBundleService(offer)
	dep := buildSkillBundleDeployment(offer)

	selector, _, _ := unstructured.NestedMap(svc.Object, "spec", "selector")
	podLabels, _, _ := unstructured.NestedMap(dep.Object, "spec", "template", "metadata", "labels")
	if len(selector) == 0 || len(selector) != len(podLabels) {
		t.Fatalf("selector = %v, pod labels = %v", selector, podLabels)
	}
	for k, v := range selector {
		if podLabels[k] != v {
			t.Errorf("selector[%s] = %v, pod label = %v", k, v, podLabels[k])
		}
	}

	if svc.GetName() != monetizeapi.SkillBundleWorkloadName(offer.Name) {
		t.Errorf("service name = %q, want %q", svc.GetName(), monetizeapi.SkillBundleWorkloadName(offer.Name))
	}
	ports, _, _ := unstructured.NestedSlice(svc.Object, "spec", "ports")
	if len(ports) != 1 {
		t.Fatalf("ports = %v", ports)
	}
	port := ports[0].(map[string]any)
	if port["port"] != int64(8080) || port["targetPort"] != int64(8080) {
		t.Errorf("port = %v, want 8080→8080", port)
	}
	if svcType, _, _ := unstructured.NestedString(svc.Object, "spec", "type"); svcType != "ClusterIP" {
		t.Errorf("service type = %q, want ClusterIP", svcType)
	}
}

func TestBuildSkillBundleMetaConfigMap_Content(t *testing.T) {
	offer := skillTestOffer(nil)
	cm, err := buildSkillBundleMetaConfigMap(offer)
	if err != nil {
		t.Fatalf("buildSkillBundleMetaConfigMap: %v", err)
	}

	if cm.GetName() != skillBundleMetaName(offer.Name) {
		t.Errorf("name = %q, want %q", cm.GetName(), skillBundleMetaName(offer.Name))
	}
	if cm.GetNamespace() != offer.Namespace {
		t.Errorf("namespace = %q, want %q", cm.GetNamespace(), offer.Namespace)
	}
	owners := cm.GetOwnerReferences()
	if len(owners) != 1 || owners[0].Kind != monetizeapi.ServiceOfferKind || owners[0].Name != offer.Name {
		t.Errorf("ownerReferences = %+v, want single ServiceOffer/%s owner", owners, offer.Name)
	}

	httpdConf, _, _ := unstructured.NestedString(cm.Object, "data", "httpd.conf")
	if !strings.Contains(httpdConf, ".tar.gz:application/gzip") || !strings.Contains(httpdConf, ".json:application/json") {
		t.Errorf("httpd.conf = %q, want gzip + json MIME entries", httpdConf)
	}

	skillJSON, _, _ := unstructured.NestedString(cm.Object, "data", "skill.json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(skillJSON), &doc); err != nil {
		t.Fatalf("skill.json is not valid JSON: %v\n%s", err, skillJSON)
	}
	wants := map[string]string{
		"name":        "buy-x402",
		"version":     "0.1.0",
		"sha256":      skillTestBundleHash(),
		"displayName": "Buy x402",
		"offer":       offer.Name,
		"namespace":   offer.Namespace,
	}
	for key, want := range wants {
		if doc[key] != want {
			t.Errorf("skill.json[%s] = %v, want %q", key, doc[key], want)
		}
	}
}

func TestSkillBundleMetaName_RespectsK8sNameLimit(t *testing.T) {
	long := strings.Repeat("a", 300)
	if wn := monetizeapi.SkillBundleWorkloadName(long); len(wn) > 63 {
		t.Errorf("workload name length = %d, want <= 63 (Service name / app label limit)", len(wn))
	}
	name := skillBundleMetaName(long)
	if len(name) > 253 {
		t.Errorf("meta name length = %d, want <= 253", len(name))
	}
	if name != skillBundleMetaName(long) {
		t.Error("meta name must be deterministic")
	}
	if short := skillBundleMetaName("buy-x402"); short != monetizeapi.SkillBundleWorkloadName("buy-x402")+"-meta" {
		t.Errorf("short names must equal SkillBundleWorkloadName+\"-meta\", got %q", short)
	}
}
