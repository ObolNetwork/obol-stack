package openclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestSkillsDir(t *testing.T) {
	tests := []struct {
		name      string
		configDir string
		id        string
		wantSuffix string
	}{
		{
			name:       "default instance",
			configDir:  "/home/user/.config/obol",
			id:         "default",
			wantSuffix: "applications/openclaw/default/skills",
		},
		{
			name:       "petname instance",
			configDir:  "/home/user/.config/obol",
			id:         "happy-otter",
			wantSuffix: "applications/openclaw/happy-otter/skills",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{ConfigDir: tt.configDir}
			got := SkillsDir(cfg, tt.id)
			if !strings.HasSuffix(got, tt.wantSuffix) {
				t.Errorf("SkillsDir() = %q, want suffix %q", got, tt.wantSuffix)
			}
		})
	}
}

func TestSkillsNotRemoteCapable(t *testing.T) {
	// "skills" must NOT be in remoteCapableCommands.
	// This ensures SkillsCLI routes through kubectl exec (into the pod),
	// not via port-forward (which requires --url/--token support).
	if remoteCapableCommands["skills"] {
		t.Error("'skills' should NOT be remote-capable; must route through kubectl exec")
	}
}

func TestSkillsRemove_MissingDeployment(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	err := SkillsRemove(cfg, "nonexistent", "some-skill")
	if err == nil {
		t.Fatal("expected error for missing deployment")
	}
	if !strings.Contains(err.Error(), "deployment not found") {
		t.Errorf("error = %q, want containing 'deployment not found'", err.Error())
	}
}

func TestSkillsRemove_SkillNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	// Create deployment directory but no skills
	deployDir := filepath.Join(tmpDir, "applications", appName, "test-id")
	if err := os.MkdirAll(deployDir, 0755); err != nil {
		t.Fatal(err)
	}

	err := SkillsRemove(cfg, "test-id", "nonexistent-skill")
	if err == nil {
		t.Fatal("expected error for missing skill")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want containing 'not found'", err.Error())
	}
}

func TestSkillsRemove_LastSkill(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}
	id := "test-remove"

	// Create deployment + one skill
	skillsDir := filepath.Join(tmpDir, "applications", appName, id, "skills")
	skillPath := filepath.Join(skillsDir, "my-skill")
	if err := os.MkdirAll(skillPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillPath, "SKILL.md"), []byte("---\nname: my-skill\n---\n# My Skill\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Remove the only skill — should succeed without attempting sync
	err := SkillsRemove(cfg, id, "my-skill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Skill directory should be gone
	if _, err := os.Stat(skillPath); !os.IsNotExist(err) {
		t.Error("skill directory should be removed")
	}

	// Skills dir itself should still exist (just empty)
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatalf("failed to read skills dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("skills dir should be empty, got %d entries", len(entries))
	}
}

func TestSkillsRemove_WithRemainingSkills(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}
	id := "test-multi"

	// Create deployment + two skills
	skillsDir := filepath.Join(tmpDir, "applications", appName, id, "skills")
	for _, name := range []string{"skill-a", "skill-b"} {
		dir := filepath.Join(skillsDir, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Remove skill-a — should succeed but sync will fail (no cluster).
	// The removal itself should complete; sync failure is expected in tests.
	err := SkillsRemove(cfg, id, "skill-a")

	// skill-a should be removed regardless of sync outcome
	if _, statErr := os.Stat(filepath.Join(skillsDir, "skill-a")); !os.IsNotExist(statErr) {
		t.Error("skill-a directory should be removed")
	}

	// skill-b should still exist
	if _, statErr := os.Stat(filepath.Join(skillsDir, "skill-b")); os.IsNotExist(statErr) {
		t.Error("skill-b directory should still exist")
	}

	// Sync will fail without a cluster — that's expected
	if err != nil && !strings.Contains(err.Error(), "cluster not running") &&
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSkillsAdd_MissingDeployment(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	err := SkillsAdd(cfg, "nonexistent", "some/skill")
	if err == nil {
		t.Fatal("expected error for missing deployment")
	}
	if !strings.Contains(err.Error(), "deployment not found") {
		t.Errorf("error = %q, want containing 'deployment not found'", err.Error())
	}
}

func TestSkillsAdd_CreatesManagedDir(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}
	id := "test-add"

	// Create deployment directory (but not skills subdir)
	deployDir := filepath.Join(tmpDir, "applications", appName, id)
	if err := os.MkdirAll(deployDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Clear PATH so findClawHub fails fast (no network access needed)
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	// SkillsAdd will fail at findClawHub, but the managed dir
	// should have been created before the binary lookup.
	_ = SkillsAdd(cfg, id, "some/skill")

	skillsDir := SkillsDir(cfg, id)
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		t.Error("skills directory should be created even if clawhub is not available")
	}
}

func TestFindClawHub_ReturnsErrorWhenMissing(t *testing.T) {
	// Save and clear PATH to ensure neither clawhub nor npx is found
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	_, _, err := findClawHub()
	if err == nil {
		t.Fatal("expected error when clawhub and npx are not in PATH")
	}
	if !strings.Contains(err.Error(), "clawhub not found") {
		t.Errorf("error = %q, want containing 'clawhub not found'", err.Error())
	}
}

func TestSkillsDirStructure(t *testing.T) {
	// Verify the skills dir is nested inside the deployment dir
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	skillsDir := SkillsDir(cfg, "my-instance")
	deployDir := deploymentPath(cfg, "my-instance")

	if !strings.HasPrefix(skillsDir, deployDir) {
		t.Errorf("skills dir %q should be under deployment dir %q", skillsDir, deployDir)
	}

	// Should end with /skills
	if filepath.Base(skillsDir) != "skills" {
		t.Errorf("skills dir should end with 'skills', got %q", filepath.Base(skillsDir))
	}
}
