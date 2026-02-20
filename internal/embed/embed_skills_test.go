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

	want := []string{"hello", "obol-blockchain", "obol-dvt", "obol-k8s"}
	sort.Strings(names)

	if len(names) != len(want) {
		t.Fatalf("got %d skills %v, want %d %v", len(names), names, len(want), want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("skill[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestCopySkills(t *testing.T) {
	destDir := t.TempDir()

	if err := CopySkills(destDir); err != nil {
		t.Fatalf("CopySkills: %v", err)
	}

	// Every skill must have a SKILL.md
	skills := []string{"hello", "obol-blockchain", "obol-dvt", "obol-k8s"}
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

	// obol-blockchain must have scripts/rpc.py and references/
	for _, sub := range []string{
		"obol-blockchain/scripts/rpc.py",
		"obol-blockchain/references/erc20-methods.md",
		"obol-blockchain/references/common-contracts.md",
	} {
		if _, err := os.Stat(filepath.Join(destDir, sub)); err != nil {
			t.Errorf("missing %s: %v", sub, err)
		}
	}

	// obol-k8s must have scripts/kube.py
	if _, err := os.Stat(filepath.Join(destDir, "obol-k8s", "scripts", "kube.py")); err != nil {
		t.Errorf("missing obol-k8s/scripts/kube.py: %v", err)
	}

	// obol-dvt must have references/api-examples.md
	if _, err := os.Stat(filepath.Join(destDir, "obol-dvt", "references", "api-examples.md")); err != nil {
		t.Errorf("missing obol-dvt/references/api-examples.md: %v", err)
	}
}

func TestCopySkillsSkipsExisting(t *testing.T) {
	destDir := t.TempDir()

	// Pre-create a skill directory with custom content
	customDir := filepath.Join(destDir, "hello")
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
