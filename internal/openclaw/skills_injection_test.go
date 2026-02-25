package openclaw

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestSkillsVolumePath(t *testing.T) {
	cfg := &config.Config{DataDir: "/data/obol"}
	got := skillsVolumePath(cfg, "default")
	want := filepath.Join("/data/obol", "openclaw-default", "openclaw-data", ".openclaw", "skills")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStageDefaultSkills(t *testing.T) {
	deploymentDir := t.TempDir()

	// stageDefaultSkills should create skills/ and populate it
	stageDefaultSkills(deploymentDir)

	skillsDir := filepath.Join(deploymentDir, "skills")
	if _, err := os.Stat(skillsDir); err != nil {
		t.Fatalf("skills dir not created: %v", err)
	}

	// Verify all expected skills were staged
	for _, skill := range []string{"distributed-validators", "ethereum-networks", "ethereum-local-wallet", "obol-stack", "addresses", "wallets"} {
		skillMD := filepath.Join(skillsDir, skill, "SKILL.md")
		if _, err := os.Stat(skillMD); err != nil {
			t.Errorf("%s/SKILL.md not staged: %v", skill, err)
		}
	}
}

func TestStageDefaultSkillsSkipsExisting(t *testing.T) {
	deploymentDir := t.TempDir()

	// Pre-create skills directory with custom content
	skillsDir := filepath.Join(deploymentDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	marker := filepath.Join(skillsDir, "custom-marker.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// stageDefaultSkills should skip because skills/ already exists
	stageDefaultSkills(deploymentDir)

	// Marker file should still be there
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("custom marker removed: %v", err)
	}

	// And no embedded skills should have been written (directory was pre-existing)
	if _, err := os.Stat(filepath.Join(skillsDir, "ethereum-networks", "SKILL.md")); err == nil {
		t.Errorf("embedded skills should NOT have been staged into existing directory")
	}
}

func TestInjectSkillsToVolume(t *testing.T) {
	deploymentDir := t.TempDir()
	dataDir := t.TempDir()
	cfg := &config.Config{DataDir: dataDir}

	// Stage skills first
	stageDefaultSkills(deploymentDir)

	// Inject to volume
	injectSkillsToVolume(cfg, "test-inject", deploymentDir)

	// Verify skills landed in the volume path
	volumePath := skillsVolumePath(cfg, "test-inject")
	for _, skill := range []string{"distributed-validators", "ethereum-networks", "ethereum-local-wallet", "obol-stack", "addresses", "wallets"} {
		skillMD := filepath.Join(volumePath, skill, "SKILL.md")
		if _, err := os.Stat(skillMD); err != nil {
			t.Errorf("%s/SKILL.md not injected to volume: %v", skill, err)
		}
	}

	// Verify scripts and references are also injected
	for _, sub := range []string{
		"ethereum-networks/scripts/rpc.py",
		"ethereum-local-wallet/scripts/signer.py",
		"obol-stack/scripts/kube.py",
		"distributed-validators/references/api-examples.md",
	} {
		path := filepath.Join(volumePath, sub)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s in volume: %v", sub, err)
		}
	}
}

func TestInjectSkillsNoopWithoutSkillsDir(t *testing.T) {
	deploymentDir := t.TempDir()
	dataDir := t.TempDir()
	cfg := &config.Config{DataDir: dataDir}

	// Don't stage anything — inject should be a no-op
	injectSkillsToVolume(cfg, "empty", deploymentDir)

	volumePath := skillsVolumePath(cfg, "empty")
	if _, err := os.Stat(volumePath); err == nil {
		t.Errorf("volume path should not exist when no skills staged")
	}
}
