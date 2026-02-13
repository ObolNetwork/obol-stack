package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnableHelmfileTLS(t *testing.T) {
	// Use the actual embedded helmfile content for the test.
	// This ensures the string patterns match the real file.
	srcPath := filepath.Join("..", "embed", "infrastructure", "helmfile.yaml")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("failed to read source helmfile: %v", err)
	}

	// Write to a temp file
	tmpDir := t.TempDir()
	helmfilePath := filepath.Join(tmpDir, "helmfile.yaml")
	if err := os.WriteFile(helmfilePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Run the patching function
	if err := enableHelmfileTLS(helmfilePath); err != nil {
		t.Fatalf("enableHelmfileTLS failed: %v", err)
	}

	// Read patched content
	patched, err := os.ReadFile(helmfilePath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(patched)

	// Verify Patch 1: TLS enabled
	if strings.Contains(content, "enabled: false  # TLS termination disabled for local dev") {
		t.Error("Patch 1 failed: TLS still disabled")
	}
	if !strings.Contains(content, "enabled: true  # TLS termination via mkcert") {
		t.Error("Patch 1 failed: TLS enabled marker not found")
	}

	// Verify Patch 2: websecure Gateway listener added
	if !strings.Contains(content, "protocol: HTTPS") {
		t.Error("Patch 2 failed: HTTPS protocol not found")
	}
	if !strings.Contains(content, "obol-stack-tls") {
		t.Error("Patch 2 failed: certificateRefs not found")
	}

	// Verify Patch 3: websecure parentRef added to HTTPRoutes
	if !strings.Contains(content, "sectionName: websecure") {
		t.Error("Patch 3 failed: websecure sectionName not found in routes")
	}

	// Count exact occurrences (use "\n" boundary to avoid substring matching)
	// "sectionName: web\n" matches only the web refs, not websecure
	webCount := strings.Count(content, "sectionName: web\n")
	websecureCount := strings.Count(content, "sectionName: websecure\n")
	if webCount != websecureCount {
		t.Errorf("Patch 3: web refs (%d) != websecure refs (%d)", webCount, websecureCount)
	}
	if websecureCount < 2 {
		t.Errorf("Patch 3: expected at least 2 websecure refs, got %d", websecureCount)
	}

	// Verify the patched content is valid YAML structure (basic check)
	// Each websecure parentRef should appear after a web parentRef
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.Contains(line, "sectionName: websecure") {
			// Should be preceded by a line with "namespace: traefik"
			if i < 1 || !strings.Contains(lines[i-1], "namespace: traefik") {
				t.Errorf("Patch 3: websecure sectionName at line %d not preceded by namespace: traefik", i+1)
			}
		}
	}

	// Verify idempotency — running again should be a no-op
	if err := enableHelmfileTLS(helmfilePath); err != nil {
		t.Fatalf("second enableHelmfileTLS call failed: %v", err)
	}
	patched2, err := os.ReadFile(helmfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(patched2) != content {
		t.Error("enableHelmfileTLS is not idempotent — second call changed the file")
	}
}
