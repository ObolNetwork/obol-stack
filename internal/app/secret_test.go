package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetSecret_ValuesOnlyOnStdin(t *testing.T) {
	env := newTestEnv(t, true)
	env.writeDeployment(t, "dvtee", "a", map[string]string{
		"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"),
		"values.yaml":   "a: 1\n",
	})

	tmp := t.TempDir()
	envFile := filepath.Join(tmp, "monitoring.env")
	tokenFile := filepath.Join(tmp, "token")

	const secretValue = "s3cr3t-value-must-not-leak"

	if err := os.WriteFile(envFile, []byte("# comment\n\nPROM_TOKEN="+secretValue+"\n  OTHER=a=b\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(tokenFile, []byte(secretValue+"-file"), 0o600); err != nil {
		t.Fatal(err)
	}

	calls := useFakeTools(t, func(tool, args string, _ []string, _ []byte) (string, error) {
		switch {
		case strings.HasPrefix(args, "get namespace"):
			return "", errors.New(`Error from server (NotFound): namespaces "dvtee-a" not found`)
		case strings.HasPrefix(args, "create namespace"), strings.HasPrefix(args, "apply"):
			return "", nil
		}

		return "", errors.New("unexpected: " + args)
	})

	err := SetSecret(env.cfg, env.u, "dvtee/a", "obol-monitoring", SecretSetOptions{
		EnvFiles: []string{envFile},
		Files:    []string{"token=" + tokenFile},
	})
	if err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	var applied []byte

	for _, c := range *calls {
		if strings.Contains(c.args, secretValue) {
			t.Fatalf("secret value in argv: %s", c.args)
		}

		if strings.HasPrefix(c.args, "apply --server-side") {
			applied = c.stdin
		}
	}

	if !hasCall(*calls, "kubectl", "create namespace dvtee-a") {
		t.Error("missing namespace not created")
	}

	var manifest struct {
		Metadata struct{ Name, Namespace string }
		Data     map[string]string
	}
	if err := json.Unmarshal(applied, &manifest); err != nil {
		t.Fatalf("manifest on stdin: %v", err)
	}

	if manifest.Metadata.Name != "obol-monitoring" || manifest.Metadata.Namespace != "dvtee-a" {
		t.Errorf("metadata = %+v", manifest.Metadata)
	}

	decode := func(k string) string {
		b, _ := base64.StdEncoding.DecodeString(manifest.Data[k])
		return string(b)
	}

	if decode("PROM_TOKEN") != secretValue || decode("OTHER") != "a=b" || decode("token") != secretValue+"-file" {
		t.Errorf("data keys = %v", manifest.Data)
	}

	out := env.stdout.String()
	if strings.Contains(out, secretValue) {
		t.Fatalf("secret value printed:\n%s", out)
	}

	if !strings.Contains(out, "OTHER, PROM_TOKEN, token") {
		t.Errorf("keys not listed:\n%s", out)
	}
}

func TestReadSecretData_ErrorsDoNotEchoContent(t *testing.T) {
	tmp := t.TempDir()
	bad := filepath.Join(tmp, "bad.env")

	if err := os.WriteFile(bad, []byte("GOOD=1\nthis-line-is-secret-material\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := readSecretData(SecretSetOptions{EnvFiles: []string{bad}})
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v", err)
	}

	if strings.Contains(err.Error(), "secret-material") {
		t.Errorf("error echoes file content: %v", err)
	}

	if _, err := readSecretData(SecretSetOptions{}); err == nil {
		t.Error("empty sources accepted")
	}
}

func TestListSecrets_NamesAndKeysOnly(t *testing.T) {
	env := newTestEnv(t, true)
	env.writeDeployment(t, "dvtee", "a", map[string]string{
		"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"),
		"values.yaml":   "a: 1\n",
	})
	calls := useFakeTools(t, func(tool, args string, _ []string, _ []byte) (string, error) {
		return dvPodSecrets, nil
	})

	if err := ListSecrets(env.cfg, env.u, "dvtee/a"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains((*calls)[0].args, "go-template=") || strings.Contains((*calls)[0].args, "-o json") {
		t.Errorf("secrets must be listed with a keys-only template: %s", (*calls)[0].args)
	}

	out := env.stdout.String()
	if !strings.Contains(out, "dvtee-a-dv-pod-enr-key (Opaque): charon-enr-private-key, enr  [key material]") ||
		!strings.Contains(out, "obol-monitoring (Opaque): token") || strings.Contains(out, "sh.helm.release") {
		t.Errorf("output:\n%s", out)
	}
}
