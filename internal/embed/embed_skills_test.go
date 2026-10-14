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

	// The built-in set is exactly the stack-operation skills; reference and
	// domain skills are opt-in from ObolNetwork/skills (internal/agentskills).
	// Adding one here grows every agent's per-turn context: keep it deliberate.
	coreSkills := []string{
		"agent-factory", "buy-x402", "discovery", "ethereum-local-wallet", "ethereum-networks",
		"ethereum-reference", "inspect", "obol-stack", "sell", "sub-agent-business", "swap",
	}

	sort.Strings(names)

	if len(names) != len(coreSkills) {
		t.Fatalf("got %d skills %v, want exactly %d", len(names), names, len(coreSkills))
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

func TestWriteSkillSubset_WritesOnlyRequested(t *testing.T) {
	dst := t.TempDir()
	want := []string{"ethereum-networks", "ethereum-local-wallet", "swap"}

	if err := WriteSkillSubset(dst, want); err != nil {
		t.Fatalf("WriteSkillSubset: %v", err)
	}

	// Every requested skill landed.
	for _, name := range want {
		if _, err := os.Stat(filepath.Join(dst, name, "SKILL.md")); err != nil {
			t.Errorf("%s/SKILL.md missing: %v", name, err)
		}
	}

	// Nothing else snuck in. Listing dst must equal the requested set exactly
	// for a fresh target dir — the seed should not pull in transitively any
	// skills we didn't ask for.
	entries, err := os.ReadDir(dst)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	got := make(map[string]bool)
	for _, e := range entries {
		got[e.Name()] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing %q in dst listing", name)
		}
		delete(got, name)
	}
	if len(got) > 0 {
		t.Errorf("unexpected entries in dst: %v", got)
	}
}

func TestWriteSkillSubset_LeavesExistingSkillsAlone(t *testing.T) {
	dst := t.TempDir()

	// Pretend the agent has already self-installed a custom skill.
	custom := filepath.Join(dst, "agent-installed-skill")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "SKILL.md"), []byte("# custom"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteSkillSubset(dst, []string{"swap"}); err != nil {
		t.Fatalf("WriteSkillSubset: %v", err)
	}

	// Custom skill must survive the seed write.
	if _, err := os.Stat(filepath.Join(custom, "SKILL.md")); err != nil {
		t.Errorf("agent-installed skill clobbered: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, "swap", "SKILL.md")); err != nil {
		t.Errorf("requested skill missing: %v", err)
	}
}

func TestWriteSkillSubset_RejectsUnknownSkill(t *testing.T) {
	dst := t.TempDir()

	err := WriteSkillSubset(dst, []string{"swap", "this-skill-does-not-exist"})
	if err == nil {
		t.Fatal("expected error for unknown skill, got nil")
	}
	if !strings.Contains(err.Error(), "this-skill-does-not-exist") {
		t.Errorf("error should name the missing skill, got: %v", err)
	}

	// When the input is invalid, no partial write should happen — fail fast
	// before touching the destination so callers can retry without cleanup.
	if _, err := os.Stat(filepath.Join(dst, "swap")); err == nil {
		t.Error("partial write occurred despite invalid input")
	}
}

func TestWriteSkillSubset_EmptyNamesIsNoop(t *testing.T) {
	dst := t.TempDir()
	if err := WriteSkillSubset(dst, nil); err != nil {
		t.Fatalf("nil names: %v", err)
	}
	if err := WriteSkillSubset(dst, []string{}); err != nil {
		t.Fatalf("empty names: %v", err)
	}
}

func TestWriteAllSkills(t *testing.T) {
	destDir := t.TempDir()

	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
	}

	// Every skill must have a SKILL.md
	skills := []string{"discovery", "ethereum-networks", "ethereum-local-wallet", "sell", "obol-stack", "swap", "inspect"}
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
		"sell/references/registrationrequest-spec.md",
		"sell/references/x402-pricing.md",
		"sell/references/monetize-guide.md",
	} {
		if _, err := os.Stat(filepath.Join(destDir, sub)); err != nil {
			t.Errorf("missing %s: %v", sub, err)
		}
	}

	if _, err := os.Stat(filepath.Join(destDir, "agent-factory", "scripts", "factory.py")); err != nil {
		t.Errorf("missing agent-factory/scripts/factory.py: %v", err)
	}

	// buy-x402 must have references/
	for _, sub := range []string{
		"buy-x402/references/purchase-request-spec.md",
		"buy-x402/references/x402-buyer-api.md",
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

}

func TestMonetizePy_Syntax(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	destDir := t.TempDir()
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
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

func TestBuyPyAutoRefillCostCapRequiresAutoRefill(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	destDir := t.TempDir()
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
	}

	buyPy := filepath.Join(destDir, "buy-x402", "scripts", "buy.py")
	script := `
import importlib.util
import sys

spec = importlib.util.spec_from_file_location("buy", sys.argv[1])
buy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(buy)

def expect_error(opts, message):
    try:
        buy._resolve_auto_refill(opts, 100, {})
    except ValueError as exc:
        if message not in str(exc):
            raise SystemExit(f"wrong error {exc!r}, wanted {message!r}")
        return
    raise SystemExit(f"expected ValueError for {opts!r}")

expect_error({"cost_cap": "42"}, "requires --auto-refill")
expect_error({"cost_cap": "42", "auto_refill": "false"}, "requires --auto-refill")

policy = buy._resolve_auto_refill({"cost_cap": "42", "auto_refill": "true"}, 100, {})
if not policy.get("enabled") or policy.get("maxUnitPrice") != "42":
    raise SystemExit(f"cost cap with auto-refill did not persist: {policy!r}")

existing = {"enabled": True, "threshold": 10, "count": 20}
policy = buy._resolve_auto_refill({"cost_cap": "43"}, 100, existing)
if not policy.get("enabled") or policy.get("maxUnitPrice") != "43":
    raise SystemExit(f"cost cap update on existing policy failed: {policy!r}")
`
	cmd := exec.Command("python3", "-c", script, buyPy)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("buy.py auto-refill policy regression failed:\n%s\n%v", output, err)
	}
}

func TestBuyPyGaslessPermitScopedToSelectedAsset(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	destDir := t.TempDir()
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
	}

	buyPy := filepath.Join(destDir, "buy-x402", "scripts", "buy.py")
	script := `
import importlib.util
import sys

spec = importlib.util.spec_from_file_location("buy", sys.argv[1])
buy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(buy)

gasless = "0x1111111111111111111111111111111111111111"
plain = "0x2222222222222222222222222222222222222222"
calls = []

def fake_nonce(address, token, chain=None):
    calls.append(("nonce", token))
    if token == gasless:
        return "7"
    raise RuntimeError("token has no ERC20Permit")

def fake_allowance(owner, token, spender, chain=None):
    calls.append(("allowance", token))
    return 1

buy._get_erc20_permit_nonce = fake_nonce
buy._get_token_allowance = fake_allowance

if not buy._payment_uses_gasless_permit("0xsigner", gasless, "base", "permit2"):
    raise SystemExit("gasless-capable token was not detected")
if buy._payment_uses_gasless_permit("0xsigner", plain, "base", "permit2"):
    raise SystemExit("top-level gasless extension leaked to a non-permit token")

buy._ensure_permit2_allowance(
    "0xsigner",
    plain,
    "base",
    "permit2",
    extensions={"eip2612GasSponsoring": {"info": {"version": "1"}}},
)
if ("allowance", plain) not in calls:
    raise SystemExit(f"plain permit2 token skipped allowance check: {calls!r}")
`
	cmd := exec.Command("python3", "-c", script, buyPy)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("buy.py gasless scoping regression failed:\n%s\n%v", output, err)
	}
}

func TestAgentFactoryPy_Syntax(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	destDir := t.TempDir()
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
	}

	factoryPy := filepath.Join(destDir, "agent-factory", "scripts", "factory.py")
	if _, err := os.Stat(factoryPy); err != nil {
		t.Fatalf("factory.py not found: %v", err)
	}

	cmd := exec.Command("python3", "-m", "py_compile", factoryPy)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("factory.py has syntax errors:\n%s\n%v", output, err)
	}
}

func TestAgentFactoryPy_AcceptAllowsZeroDecimals(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	destDir := t.TempDir()
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
	}

	factoryPy := filepath.Join(destDir, "agent-factory", "scripts", "factory.py")
	script := `
import importlib.util
import sys

spec = importlib.util.spec_from_file_location("factory", sys.argv[1])
factory = importlib.util.module_from_spec(spec)
spec.loader.exec_module(factory)

pay_to = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
raw = "asset=0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb,decimals=0,transfer=permit2,eip712-name=Whole Token,eip712-version=1,symbol=WHOLE,network=base,price=1,pay-to=" + pay_to
payment, _ = factory.parse_accept_option(raw, "")
if payment["asset"]["decimals"] != 0:
    raise SystemExit(f"explicit decimals=0 was not preserved: {payment!r}")

payments = factory.build_accept_payments([
    "asset=0xcccccccccccccccccccccccccccccccccccccccc,network=base,price=1,pay-to=" + pay_to,
], "")
if payments[0]["asset"]["decimals"] != -1:
    raise SystemExit(f"omitted decimals should use pending sentinel: {payments!r}")

def fetch(network, addr):
    return {"decimals": 0, "decimalsSet": True, "symbol": "WHOLE", "eip712Name": "Whole Token", "eip712Version": "1"}

factory.autofill_accept_payments(payments, fetch)
asset = payments[0]["asset"]
if asset["decimals"] != 0 or asset["eip712Name"] != "Whole Token":
    raise SystemExit(f"zero-decimal autofill failed: {asset!r}")
`
	cmd := exec.Command("python3", "-c", script, factoryPy)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("factory.py zero-decimal accept regression failed:\n%s\n%v", output, err)
	}
}

func TestAgentFactoryPy_ProfileArchiveAndRegistrationBehavior(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	destDir := t.TempDir()
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
	}

	factoryPy := filepath.Join(destDir, "agent-factory", "scripts", "factory.py")
	script := `
import importlib.util
import io
import sys
import tarfile
from types import SimpleNamespace

spec = importlib.util.spec_from_file_location("factory", sys.argv[1])
factory = importlib.util.module_from_spec(spec)
spec.loader.exec_module(factory)

archive = factory.build_profile_archive("medical", "Stay in scope.", [])
factory.validate_profile_archive_bytes(archive)

bad = io.BytesIO()
with tarfile.open(fileobj=bad, mode="w:gz") as tf:
    body = b"escape"
    info = tarfile.TarInfo("../escape")
    info.size = len(body)
    tf.addfile(info, io.BytesIO(body))
try:
    factory.validate_profile_archive_bytes(bad.getvalue())
except ValueError:
    pass
else:
    raise SystemExit("unsafe archive accepted")

args = SimpleNamespace(
    name="medical",
    model="antangelmed",
    network="base-sepolia",
    pay_to="0x1111111111111111111111111111111111111111",
    max_timeout=300,
    price="0.05",
    path=None,
    offer_name=None,
    register=False,
    register_name="Medical Advisor",
    register_description=None,
    register_skills=[],
    skills=["privacy-filter"],
)
offer = factory.serviceoffer_resource(args, "hermes-obol-agent")
registration = offer["spec"]["registration"]
# §5 decoupling: register_name/description populate the block for discovery,
# but on-chain registration (enabled) is driven ONLY by --register. Here
# register=False, so the block is present yet enabled stays False.
if registration.get("enabled") is not False:
    raise SystemExit("registration.enabled must follow --register, not register-name")
if registration.get("name") != "Medical Advisor":
    raise SystemExit(f"registration name not populated: {registration!r}")
if registration.get("skills") != ["privacy-filter"]:
    raise SystemExit(f"registration skills did not inherit agent skills: {registration!r}")
expected_metadata = {
    "runtime": "hermes",
    "model": args.model,
    "pricingUnit": "agent-turn",
    "x402Price": "0.05",
    "x402Asset": "USDC",
    "x402Network": "base-sepolia",
}
if registration.get("metadata") != expected_metadata:
    raise SystemExit(f"registration metadata mismatch: {registration!r}")
`

	cmd := exec.Command("python3", "-c", script, factoryPy)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("factory.py behavior test failed:\n%s\n%v", output, err)
	}
}

func TestKubePy_WriteHelpers(t *testing.T) {
	destDir := t.TempDir()
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
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

// TestKubePy_NamespaceScoping pins the read-only CLI's API paths: own
// namespace by default, -n for another namespace, -A cluster-wide, logs query
// params, and describe refusing Secrets before any API call.
func TestKubePy_NamespaceScoping(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	destDir := t.TempDir()
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
	}

	kubePy := filepath.Join(destDir, "obol-stack", "scripts", "kube.py")
	script := `
import contextlib
import importlib.util
import io
import sys
import urllib.parse

spec = importlib.util.spec_from_file_location("kube", sys.argv[1])
kube = importlib.util.module_from_spec(spec)
spec.loader.exec_module(kube)

calls = []
kube.load_sa = lambda: ("tok", "hermes-obol-agent")
kube.make_ssl_context = lambda: None
def fake_get(path, token, ssl_ctx, quiet=False):
    calls.append(path)
    return {"items": [], "metadata": {"name": "x"}}
kube.api_get = fake_get

class Resp(io.BytesIO):
    def __enter__(self): return self
    def __exit__(self, *a): return False
def fake_urlopen(req, context=None, timeout=None):
    calls.append(req.full_url[len(kube.API_SERVER):])
    return Resp(b"line\n")
kube.urllib.request.urlopen = fake_urlopen

def run(*argv):
    calls.clear()
    with contextlib.redirect_stdout(io.StringIO()):
        kube.main(list(argv))
    return calls[-1]

def expect(got, want):
    if got != want:
        raise SystemExit(f"path {got!r} != {want!r}")

expect(run("pods"), "/api/v1/namespaces/hermes-obol-agent/pods")
expect(run("pods", "-n", "ethereum-abc"), "/api/v1/namespaces/ethereum-abc/pods")
expect(run("pods", "-A"), "/api/v1/pods")
expect(run("statefulsets", "-n", "dv"), "/apis/apps/v1/namespaces/dv/statefulsets")
expect(run("pvcs", "-A"), "/api/v1/persistentvolumeclaims")
expect(run("deployments", "-A", "-l", "app=x"), "/apis/apps/v1/deployments?labelSelector=app%3Dx")
expect(run("events", "-A", "--type", "Warning"), "/api/v1/events?fieldSelector=type%3DWarning")
expect(run("namespaces"), "/api/v1/namespaces")
expect(run("describe", "statefulset", "beacon", "-n", "ethereum-abc"),
       "/apis/apps/v1/namespaces/ethereum-abc/statefulsets/beacon")

got = run("logs", "charon-0", "-n", "dv", "-c", "charon", "--since", "10m", "--previous", "--tail", "50")
path, _, query = got.partition("?")
expect(path, "/api/v1/namespaces/dv/pods/charon-0/log")
params = dict(urllib.parse.parse_qsl(query))
if params != {"tailLines": "50", "container": "charon", "sinceSeconds": "600", "previous": "true"}:
    raise SystemExit(f"logs params {params!r}")

for argv in (["describe", "secret", "x"], ["pods", "-n", "a", "-A"]):
    calls.clear()
    try:
        with contextlib.redirect_stderr(io.StringIO()):
            kube.main(argv)
    except SystemExit as e:
        if not e.code:
            raise SystemExit(f"{argv} should fail")
    else:
        raise SystemExit(f"{argv} should fail")
    if calls:
        raise SystemExit(f"{argv} must not call the API: {calls}")
`

	cmd := exec.Command("python3", "-c", script, kubePy)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kube.py behavior test failed:\n%s\n%v", output, err)
	}
}

func TestDiscoveryPy_Syntax(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}

	destDir := t.TempDir()
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
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
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
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

func TestWriteAllSkillsKeepsExisting(t *testing.T) {
	destDir := t.TempDir()

	// Pre-create a skill directory with custom content
	customDir := filepath.Join(destDir, "obol-stack")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	customFile := filepath.Join(customDir, "custom.txt")
	if err := os.WriteFile(customFile, []byte("user content"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// writeAllSkills should still succeed (it copies all files, including into existing dirs)
	if err := writeAllSkills(destDir); err != nil {
		t.Fatalf("writeAllSkills: %v", err)
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

// writeAllSkills seeds every embedded skill into dst through the production
// path (WriteSkillSubset).
func writeAllSkills(dst string) error {
	names, err := GetEmbeddedSkillNames()
	if err != nil {
		return err
	}
	return WriteSkillSubset(dst, names)
}
