package tee

import (
	"testing"
)

func TestParseCoCoRuntime(t *testing.T) {
	tests := []struct {
		input string
		want  CoCoRuntimeClass
		ok    bool
	}{
		{"kata-qemu-coco-dev", RuntimeQEMUCoCoDev, true},
		{"kata-qemu-snp", RuntimeQEMUSNP, true},
		{"kata-qemu-tdx", RuntimeQEMUTDX, true},
		{"none", "", true},
		{"", "", true},
		{"kata-qemu-sev", "", false},
		{"docker", "", false},
	}

	for _, tt := range tests {
		got, err := ParseCoCoRuntime(tt.input)
		if tt.ok && err != nil {
			t.Errorf("ParseCoCoRuntime(%q) unexpected error: %v", tt.input, err)
		} else if !tt.ok && err == nil {
			t.Errorf("ParseCoCoRuntime(%q) expected error, got %q", tt.input, got)
		} else if tt.ok && got != tt.want {
			t.Errorf("ParseCoCoRuntime(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidCoCoRuntimes(t *testing.T) {
	runtimes := ValidCoCoRuntimes()
	if len(runtimes) != 3 {
		t.Errorf("expected 3 runtime classes, got %d", len(runtimes))
	}
	// Verify each is parseable.
	for _, r := range runtimes {
		got, err := ParseCoCoRuntime(string(r))
		if err != nil {
			t.Errorf("ParseCoCoRuntime(%q) failed: %v", r, err)
		}

		if got != r {
			t.Errorf("round-trip mismatch: %q != %q", got, r)
		}
	}
}

func TestInstallCoCo_DryRun(t *testing.T) {
	cmd, err := InstallCoCo(t.Context(), &CoCoInstallOpts{
		HelmBin: "/usr/bin/helm",
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("DryRun failed: %v", err)
	}

	// Should contain the expected helm command.
	if cmd == "" {
		t.Fatal("expected non-empty command string")
	}

	for _, want := range []string{
		CoCoChartOCI,
		CoCoChartVersion,
		"k8sDistribution=k3s",
		CoCoNamespace,
		"--create-namespace",
	} {
		if !contains(cmd, want) {
			t.Errorf("dry-run command missing %q:\n  %s", want, cmd)
		}
	}
}

func TestUninstallCoCo_DryRun(t *testing.T) {
	cmd, err := UninstallCoCo(t.Context(), &CoCoInstallOpts{
		HelmBin: "/usr/bin/helm",
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("DryRun failed: %v", err)
	}

	for _, want := range []string{"uninstall", CoCoReleaseName, CoCoNamespace} {
		if !contains(cmd, want) {
			t.Errorf("dry-run command missing %q:\n  %s", want, cmd)
		}
	}
}

func TestCheckKVM(t *testing.T) {
	// Just verify it doesn't panic — the result depends on hardware.
	result := checkKVM()
	t.Logf("KVM available: %v", result)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}

	return false
}
