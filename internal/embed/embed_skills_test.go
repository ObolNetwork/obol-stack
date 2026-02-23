package embed

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestGetEmbeddedSkillNames(t *testing.T) {
	names, err := GetEmbeddedSkillNames()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Core skills that must always be present
	coreSkills := []string{
		"addresses", "building-blocks", "concepts", "distributed-validators",
		"ethereum-networks", "frontend-playbook", "frontend-ux", "gas",
		"indexing", "l2s", "obol-stack", "orchestration", "qa", "security",
		"ship", "standards", "testing", "tools", "wallets", "why",
	}
	sort.Strings(names)

	if len(names) < len(coreSkills) {
		t.Fatalf("got %d skills %v, want at least %d", len(names), names, len(coreSkills))
	}

	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	for _, core := range coreSkills {
		if !nameSet[core] {
			t.Errorf("missing core skill %q in %v", core, names)
		}
	}
}

func TestCopySkills(t *testing.T) {
	destDir := t.TempDir()

	if err := CopySkills(destDir); err != nil {
		t.Fatalf("CopySkills: %v", err)
	}

	// Every skill must have a SKILL.md
	skills := []string{"distributed-validators", "ethereum-networks", "obol-stack", "addresses", "wallets"}
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
	customDir := filepath.Join(destDir, "obol-stack")
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
