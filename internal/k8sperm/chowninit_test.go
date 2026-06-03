package k8sperm

import (
	"strings"
	"testing"
)

func TestChownCommand(t *testing.T) {
	tests := []struct {
		name  string
		uid   int64
		gid   int64
		paths []string
		want  string
	}{
		{name: "single", uid: 10000, gid: 10000, paths: []string{"/data"}, want: "chown -R 10000:10000 /data"},
		{name: "multi", uid: 1000, gid: 1000, paths: []string{"/data", "/state"}, want: "chown -R 1000:1000 /data /state"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChownCommand(tt.uid, tt.gid, tt.paths); got != tt.want {
				t.Errorf("ChownCommand = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRootChownInitContainer(t *testing.T) {
	c := RootChownInitContainer("init-perms", "example/image:tag", 10000, 10000, []ChownMount{
		{Name: "data", MountPath: "/data"},
	})

	if c["name"] != "init-perms" {
		t.Errorf("name = %v, want init-perms", c["name"])
	}
	if c["image"] != "example/image:tag" {
		t.Errorf("image = %v, want example/image:tag", c["image"])
	}
	if c["imagePullPolicy"] != "IfNotPresent" {
		t.Errorf("imagePullPolicy = %v, want IfNotPresent", c["imagePullPolicy"])
	}

	sc, ok := c["securityContext"].(map[string]any)
	if !ok {
		t.Fatal("securityContext missing")
	}
	// Must run as root to chown a volume the provisioner owns as a different UID.
	if sc["runAsUser"] != int64(0) {
		t.Errorf("runAsUser = %v, want 0", sc["runAsUser"])
	}
	if sc["runAsGroup"] != int64(0) {
		t.Errorf("runAsGroup = %v, want 0", sc["runAsGroup"])
	}
	if sc["runAsNonRoot"] != false {
		t.Errorf("runAsNonRoot = %v, want false", sc["runAsNonRoot"])
	}

	cmd, ok := c["command"].([]any)
	if !ok || len(cmd) != 3 {
		t.Fatalf("command = %v, want [sh -ec <script>]", c["command"])
	}
	if cmd[0] != "sh" || cmd[1] != "-ec" {
		t.Errorf("command prefix = %v %v, want sh -ec", cmd[0], cmd[1])
	}
	script, _ := cmd[2].(string)
	if !strings.Contains(script, "chown -R 10000:10000 /data") {
		t.Errorf("command script = %q, want chown -R 10000:10000 /data", script)
	}

	mounts, ok := c["volumeMounts"].([]any)
	if !ok || len(mounts) != 1 {
		t.Fatalf("volumeMounts = %v, want 1 entry", c["volumeMounts"])
	}
	m := mounts[0].(map[string]any)
	if m["name"] != "data" || m["mountPath"] != "/data" {
		t.Errorf("volumeMount = %v, want {name:data, mountPath:/data}", m)
	}
}

func TestRootChownInitContainer_MultiMount(t *testing.T) {
	c := RootChownInitContainer("init-perms", "img", 1000, 1000, []ChownMount{
		{Name: "data", MountPath: "/data"},
		{Name: "state", MountPath: "/state"},
	})
	cmd := c["command"].([]any)
	script := cmd[2].(string)
	if !strings.Contains(script, "/data") || !strings.Contains(script, "/state") {
		t.Errorf("command must chown all mounts, got %q", script)
	}
	mounts := c["volumeMounts"].([]any)
	if len(mounts) != 2 {
		t.Errorf("volumeMounts = %d, want 2", len(mounts))
	}
}
