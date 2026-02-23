package embed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetEmbeddedSkillNames(t *testing.T) {
	names, err := GetEmbeddedSkillNames()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Core skills that must always be present
	required := []string{"distributed-validators", "ethereum-networks", "local-ethereum-wallet", "obol-stack"}
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	for _, r := range required {
		if !nameSet[r] {
			t.Errorf("required skill %q not found in embedded skills %v", r, names)
		}
	}
}

func TestCopySkills(t *testing.T) {
	destDir := t.TempDir()

	if err := CopySkills(destDir); err != nil {
		t.Fatalf("CopySkills: %v", err)
	}

	// Every skill must have a SKILL.md
	skills := []string{"distributed-validators", "ethereum-networks", "local-ethereum-wallet", "obol-stack"}
	for _, skill := range skills {
		skillMD := filepath.Join(destDir, skill, "SKILL.md")
		info, err := os.Stat(skillMD)
		if err != nil {
			t.Errorf("%s/SKILL.md: %v", skill, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s/SKILL.md is empty", skill)
		}
	}

	// ethereum-networks must have scripts/rpc.py and references/
	for _, sub := range []string{
		"ethereum-networks/scripts/rpc.py",
		"ethereum-networks/references/erc20-methods.md",
		"ethereum-networks/references/common-contracts.md",
	} {
		if _, err := os.Stat(filepath.Join(destDir, sub)); err != nil {
			t.Errorf("missing %s: %v", sub, err)
		}
	}

	// obol-stack must have scripts/kube.py
	if _, err := os.Stat(filepath.Join(destDir, "obol-stack", "scripts", "kube.py")); err != nil {
		t.Errorf("missing obol-stack/scripts/kube.py: %v", err)
	}

	// distributed-validators must have references/api-examples.md
	if _, err := os.Stat(filepath.Join(destDir, "distributed-validators", "references", "api-examples.md")); err != nil {
		t.Errorf("missing distributed-validators/references/api-examples.md: %v", err)
	}
}

func TestCopySkillsSkipsExisting(t *testing.T) {
	destDir := t.TempDir()

	// Pre-create a skill directory with custom content
	customDir := filepath.Join(destDir, "local-ethereum-wallet")
	if err := os.MkdirAll(customDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	customFile := filepath.Join(customDir, "custom.txt")
	if err := os.WriteFile(customFile, []byte("user content"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// CopySkills should still succeed (it copies all files, including into existing dirs)
	if err := CopySkills(destDir); err != nil {
		t.Fatalf("CopySkills: %v", err)
	}

	// Custom file should still be present
	if _, err := os.Stat(customFile); err != nil {
		t.Errorf("custom file was removed: %v", err)
	}

	// But SKILL.md should also have been copied
	if _, err := os.Stat(filepath.Join(customDir, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md not copied alongside custom content: %v", err)
	}
}
