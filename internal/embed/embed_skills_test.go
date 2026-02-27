package embed

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestGetEmbeddedSkillNames(t *testing.T) {
	names, err := GetEmbeddedSkillNames()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Core skills that must always be present
	coreSkills := []string{
		"addresses", "building-blocks", "concepts", "discovery", "distributed-validators",
		"ethereum-networks", "ethereum-local-wallet", "frontend-playbook", "frontend-ux", "gas",
		"indexing", "l2s", "sell", "obol-stack", "orchestration", "qa", "security",
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
	skills := []string{"discovery", "distributed-validators", "ethereum-networks", "ethereum-local-wallet", "sell", "obol-stack", "addresses", "wallets"}
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

	// ethereum-local-wallet must have scripts/signer.py and references/
	for _, sub := range []string{
		"ethereum-local-wallet/scripts/signer.py",
		"ethereum-local-wallet/references/remote-signer-api.md",
	} {
		if _, err := os.Stat(filepath.Join(destDir, sub)); err != nil {
			t.Errorf("missing %s: %v", sub, err)
		}
	}

	// obol-stack must have scripts/kube.py
	if _, err := os.Stat(filepath.Join(destDir, "obol-stack", "scripts", "kube.py")); err != nil {
		t.Errorf("missing obol-stack/scripts/kube.py: %v", err)
	}

	// sell must have scripts/monetize.py and references/
	for _, sub := range []string{
		"sell/scripts/monetize.py",
		"sell/references/serviceoffer-spec.md",
		"sell/references/x402-pricing.md",
	} {
		if _, err := os.Stat(filepath.Join(destDir, sub)); err != nil {
			t.Errorf("missing %s: %v", sub, err)
		}
	}

	// discovery must have scripts/discovery.py and references/
	for _, sub := range []string{
		"discovery/scripts/discovery.py",
		"discovery/references/erc8004-registry.md",
	} {
		if _, err := os.Stat(filepath.Join(destDir, sub)); err != nil {
			t.Errorf("missing %s: %v", sub, err)
		}
	}

	// distributed-validators must have references/api-examples.md
	if _, err := os.Stat(filepath.Join(destDir, "distributed-validators", "references", "api-examples.md")); err != nil {
		t.Errorf("missing distributed-validators/references/api-examples.md: %v", err)
	}
}

func TestMonetizePy_Syntax(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	destDir := t.TempDir()
	if err := CopySkills(destDir); err != nil {
		t.Fatalf("CopySkills: %v", err)
	}

	monetizePy := filepath.Join(destDir, "sell", "scripts", "monetize.py")
	if _, err := os.Stat(monetizePy); err != nil {
		t.Fatalf("monetize.py not found: %v", err)
	}

	cmd := exec.Command("python3", "-m", "py_compile", monetizePy)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("monetize.py has syntax errors:\n%s\n%v", output, err)
	}
}

func TestKubePy_WriteHelpers(t *testing.T) {
	destDir := t.TempDir()
	if err := CopySkills(destDir); err != nil {
		t.Fatalf("CopySkills: %v", err)
	}

	kubePy := filepath.Join(destDir, "obol-stack", "scripts", "kube.py")
	data, err := os.ReadFile(kubePy)
	if err != nil {
		t.Fatalf("read kube.py: %v", err)
	}

	content := string(data)
	for _, fn := range []string{"def api_post", "def api_patch", "def api_delete"} {
		if !strings.Contains(content, fn) {
			t.Errorf("kube.py missing function %q", fn)
		}
	}
}

func TestDiscoveryPy_Syntax(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	destDir := t.TempDir()
	if err := CopySkills(destDir); err != nil {
		t.Fatalf("CopySkills: %v", err)
	}

	discoveryPy := filepath.Join(destDir, "discovery", "scripts", "discovery.py")
	if _, err := os.Stat(discoveryPy); err != nil {
		t.Fatalf("discovery.py not found: %v", err)
	}

	cmd := exec.Command("python3", "-m", "py_compile", discoveryPy)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("discovery.py has syntax errors:\n%s\n%v", output, err)
	}
}

func TestDiscoverySkill_Commands(t *testing.T) {
	destDir := t.TempDir()
	if err := CopySkills(destDir); err != nil {
		t.Fatalf("CopySkills: %v", err)
	}

	discoveryPy := filepath.Join(destDir, "discovery", "scripts", "discovery.py")
	data, err := os.ReadFile(discoveryPy)
	if err != nil {
		t.Fatalf("read discovery.py: %v", err)
	}

	content := string(data)
	for _, fn := range []string{
		"def cmd_search",
		"def cmd_agent",
		"def cmd_uri",
		"def cmd_count",
		"def get_token_uri",
		"def get_owner",
		"def get_agent_wallet",
		"def search_registered_events",
		"def fetch_agent_uri_json",
	} {
		if !strings.Contains(content, fn) {
			t.Errorf("discovery.py missing function %q", fn)
		}
	}

	// Verify key constants are present
	for _, constant := range []string{
		"REGISTERED_TOPIC",
		"SEL_TOKEN_URI",
		"SEL_OWNER_OF",
		"SEL_GET_AGENT_WALLET",
		"REGISTRY_MAINNET",
		"REGISTRY_TESTNET",
	} {
		if !strings.Contains(content, constant) {
			t.Errorf("discovery.py missing constant %q", constant)
		}
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
