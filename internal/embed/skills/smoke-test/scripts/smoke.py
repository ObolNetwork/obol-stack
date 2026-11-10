#!/usr/bin/env python3
"""smoke.py — read-only smoke probe of an Obol Stack public surface.

Probes a TARGET base URL's public discovery + payment-gating surface with
plain GETs, writes a markdown report + machine-readable results, and (via
`post` / gh_post.py) commits the report to a seller-owned public GitHub repo.

Safety contract (non-negotiable):
  - GET only. NEVER sends an X-PAYMENT header, never signs anything, never
    settles anything, never submits chain transactions.
  - Never probes a cross-host URL: catalog endpoints are reduced to their
    PATH and re-joined onto the target base URL.
  - Response bodies capped at 1 MiB; per-check timeout 8s; one retry on
    connection-level errors only (refused/reset — fast failures), never on
    timeouts or HTTP errors.
  - Redirects are not followed (a 3xx counts as the final status).

Checks (counted unless marked informational):
  1. skill-md            GET <target>/skill.md                 -> 200 + non-empty body
  2. services-json       GET <target>/api/services.json        -> 200 + bare JSON LIST of
                         objects with non-empty string `name` and `endpoint`
  3. x402-402:<name>     per advertised service (first 5 sorted by name):
                         GET <target><path-of-endpoint>        -> 402 + valid x402 body
                         (x402Version present; accepts non-empty; each entry has
                         scheme/network non-empty, payTo/asset 0x40-hex, and a
                         positive digits-only maxAmountRequired OR amount)
  4. agent-registration  GET <target>/.well-known/agent-registration.json
                         -> 200 + JSON object  (INFORMATIONAL — excluded from score)

Scoring: passed/total over counted checks only (total >= 2).
  score255 = floor(255*passed/total)   (off-chain, task-spec field)
  score100 = floor(100*passed/total)   (THE on-chain value — registry caps at 100)

Usage:
    python3 smoke.py probe <targetBaseURL> [--run-id <id>] [--out-dir <base>]
    python3 smoke.py post  <runDir>
    python3 smoke.py run   <targetBaseURL> [--run-id <id>] [--out-dir <base>]

`probe` performs NO network writes; it writes report.md + results.json under
<out-dir>/<target-host>/<runId>/ (default ./smoke/...) and prints results.json
to stdout. `post` commits an existing report to GitHub (env GITHUB_TOKEN +
GITHUB_REPORT_REPO required) and prints the updated results.json. `run` is
probe+post one-shot for host/manual use; it degrades to probe-only when the
GitHub env is absent.

Exit codes: 0 even when checks fail (the score carries the verdict);
non-zero only on operational errors (bad args, unwritable workspace,
GitHub post failure).

Stdlib only: argparse/hashlib/json/re/secrets/socket/time/urllib.
"""

import argparse
import hashlib
import json
import os
import re
import secrets
import socket
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

VERSION = "obol/smoke-test/v1"

# Same Cloudflare-WAF-safe UA convention as buy-x402's buy.py.
USER_AGENT = "obol-smoke-test/1.0 (+https://github.com/ObolNetwork/obol-stack)"

PER_CHECK_TIMEOUT = 8          # seconds, per attempt
MAX_BODY_BYTES = 1024 * 1024   # 1 MiB body cap on every response
MAX_SERVICES = 5               # probe at most the first 5 services sorted by name

ADDR_RE = re.compile(r"^0x[0-9a-fA-F]{40}$")
DIGITS_RE = re.compile(r"^[0-9]+$")
RUN_ID_RE = re.compile(r"^[A-Za-z0-9._-]+$")
MAX_DETAIL_LEN = 200


def log(msg):
    """Diagnostics go to stderr; stdout is reserved for results.json."""
    print(msg, file=sys.stderr)


# ---------------------------------------------------------------------------
# Normalization (MUST stay in lockstep with Go: erc8004 normalizeSmokeTarget)
# ---------------------------------------------------------------------------

def normalize_target(url):
    """Identical to the Go-side normalization: strip whitespace, then strip
    trailing slashes. The normalized form is what `obol smoke calldata`
    hashes into the ERC-8004 requestHash preimage
    ("obol/smoke-test/v1|<target>|<runId>") — this script never computes
    keccak256 (no reliable in-pod keccak; hashlib.sha3_256 is NIST SHA-3,
    NOT keccak256)."""
    return url.strip().rstrip("/")


def host_slug(target):
    """Lowercase hostname with ":<port>" rewritten to "-<port>", e.g.
    obol.stack:8080 -> obol.stack-8080. Used for the local run dir AND the
    GitHub report path (gh_post.py imports this — keep behavior stable)."""
    netloc = urllib.parse.urlsplit(target).netloc
    host = netloc.rsplit("@", 1)[-1].lower()
    return re.sub(r"[^a-z0-9._-]", "-", host)


def default_run_id():
    """<UTC yyyymmddTHHMMSSZ>-<6 lowercase hex>."""
    return time.strftime("%Y%m%dT%H%M%SZ", time.gmtime()) + "-" + secrets.token_hex(3)


# ---------------------------------------------------------------------------
# HTTP (GET only — by construction this module cannot send a payment)
# ---------------------------------------------------------------------------

class _NoRedirect(urllib.request.HTTPRedirectHandler):
    """Never follow redirects: a redirect could send the probe cross-host.
    A 3xx is returned as the final status of the check."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


_OPENER = urllib.request.build_opener(_NoRedirect)


def _fetch(url):
    """GET url. Returns (status, body_bytes, error_str). Never raises.

    status == 0 with a non-empty error_str means no HTTP response at all.
    One retry on connection-level errors only (ConnectionError — refused /
    reset fail fast); timeouts and DNS failures are NOT retried so the
    worst-case probe budget stays bounded under the agent's 80s terminal
    timeout."""
    req = urllib.request.Request(
        url,
        method="GET",
        headers={"User-Agent": USER_AGENT, "Accept": "*/*"},
    )
    attempt = 0
    while True:
        attempt += 1
        try:
            with _OPENER.open(req, timeout=PER_CHECK_TIMEOUT) as resp:
                return resp.getcode(), resp.read(MAX_BODY_BYTES), ""
        except urllib.error.HTTPError as exc:
            try:
                body = exc.read(MAX_BODY_BYTES)
            except Exception:
                body = b""
            return exc.code, body, ""
        except urllib.error.URLError as exc:
            reason = getattr(exc, "reason", exc)
            if isinstance(reason, ConnectionError) and attempt == 1:
                time.sleep(1)
                continue
            if isinstance(reason, (socket.timeout, TimeoutError)):
                return 0, b"", "timeout after %ds" % PER_CHECK_TIMEOUT
            return 0, b"", "connection failed: %s" % reason
        except (socket.timeout, TimeoutError):
            return 0, b"", "timeout after %ds" % PER_CHECK_TIMEOUT
        except ConnectionError as exc:
            if attempt == 1:
                time.sleep(1)
                continue
            return 0, b"", "connection failed: %s" % exc
        except OSError as exc:
            return 0, b"", "network error: %s" % exc


def _clip(detail):
    detail = str(detail)
    if len(detail) > MAX_DETAIL_LEN:
        detail = detail[: MAX_DETAIL_LEN - 1] + "…"
    return detail


def _check(name, ok, detail, ms, informational=False):
    entry = {"name": name, "ok": bool(ok), "detail": _clip(detail), "ms": int(ms)}
    if informational:
        entry["informational"] = True
    return entry


def _timed(name, fn, informational=False):
    """ms = wall-clock per check (includes the single connection-error retry)."""
    t0 = time.monotonic()
    ok, detail, extra = fn()
    ms = round((time.monotonic() - t0) * 1000)
    return _check(name, ok, detail, ms, informational=informational), extra


# ---------------------------------------------------------------------------
# Checks
# ---------------------------------------------------------------------------

def check_skill_md(target):
    def run():
        status, body, err = _fetch(target + "/skill.md")
        if err:
            return False, err, None
        if status != 200:
            return False, "expected 200, got %d" % status, None
        if not body.decode("utf-8", "replace").strip():
            return False, "200 but body empty after strip", None
        return True, "200, %d bytes" % len(body), None

    return _timed("skill-md", run)[0]


def check_services_json(target):
    """Returns (check, services). services is the validated advertised list
    (possibly empty) when the check passed, else []."""

    def run():
        status, body, err = _fetch(target + "/api/services.json")
        if err:
            return False, err, []
        if status != 200:
            return False, "expected 200, got %d" % status, []
        try:
            parsed = json.loads(body.decode("utf-8", "replace"))
        except ValueError as exc:
            return False, "invalid JSON: %s" % exc, []
        # The catalog is a BARE JSON array of entries — not {"services": [...]}.
        if not isinstance(parsed, list):
            return False, "top-level JSON is not a list", []
        for i, entry in enumerate(parsed):
            if not isinstance(entry, dict):
                return False, "entry %d is not an object" % i, []
            name = entry.get("name")
            endpoint = entry.get("endpoint")
            if not isinstance(name, str) or not name.strip():
                return False, "entry %d missing non-empty string `name`" % i, []
            if not isinstance(endpoint, str) or not endpoint.strip():
                return False, "entry %d (%s) missing non-empty string `endpoint`" % (i, name), []
        return True, "200, %d service(s) advertised" % len(parsed), parsed

    return _timed("services-json", run)


def _validate_accepts_entry(entry, idx):
    """Returns failure reason or '' for one entry of the 402 `accepts` list.
    Amount uses the same v1/v2 dual lookup as buy.py: maxAmountRequired
    falling back to amount."""
    if not isinstance(entry, dict):
        return "accepts[%d] is not an object" % idx
    for field in ("scheme", "network"):
        value = entry.get(field)
        if not isinstance(value, str) or not value.strip():
            return "accepts[%d].%s missing or empty" % (idx, field)
    for field in ("payTo", "asset"):
        value = entry.get(field)
        if not isinstance(value, str) or not ADDR_RE.match(value):
            return "accepts[%d].%s is not a 0x..40-hex address" % (idx, field)
    raw = entry.get("maxAmountRequired")
    if raw is None or not str(raw).strip():
        raw = entry.get("amount")
    amount = str(raw if raw is not None else "").strip()
    if not DIGITS_RE.match(amount) or int(amount) <= 0:
        return "accepts[%d] has no positive digits-only maxAmountRequired/amount" % idx
    return ""


def check_service_402(target, service):
    """One counted check per advertised service. Probes ONLY the path of the
    catalog endpoint joined onto the target base URL — never a cross-host URL
    the catalog hands us."""
    name = service["name"].strip()

    def run():
        path = urllib.parse.urlsplit(service["endpoint"].strip()).path
        if not path.startswith("/"):
            path = "/" + path
        status, body, err = _fetch(target + path)
        if err:
            return False, err, None
        if status != 402:
            return False, "expected 402, got %d" % status, None
        try:
            parsed = json.loads(body.decode("utf-8", "replace"))
        except ValueError as exc:
            return False, "402 body is not JSON: %s" % exc, None
        if not isinstance(parsed, dict):
            return False, "402 body is not a JSON object", None
        if "x402Version" not in parsed:
            return False, "402 body missing x402Version", None
        accepts = parsed.get("accepts")
        if not isinstance(accepts, list) or not accepts:
            return False, "402 body has no non-empty accepts list", None
        for i, entry in enumerate(accepts):
            reason = _validate_accepts_entry(entry, i)
            if reason:
                return False, reason, None
        return True, "402, %d payment option(s)" % len(accepts), None

    return _timed("x402-402:" + name, run)[0]


def check_agent_registration(target):
    """INFORMATIONAL — recorded but excluded from passed/total/score."""

    def run():
        status, body, err = _fetch(target + "/.well-known/agent-registration.json")
        if err:
            return False, err, None
        if status != 200:
            return False, "expected 200, got %d" % status, None
        try:
            parsed = json.loads(body.decode("utf-8", "replace"))
        except ValueError as exc:
            return False, "invalid JSON: %s" % exc, None
        if not isinstance(parsed, dict):
            return False, "200 but body is not a JSON object", None
        return True, "200, JSON object", None

    return _timed("agent-registration", run, informational=True)[0]


# ---------------------------------------------------------------------------
# Report rendering
# ---------------------------------------------------------------------------

def _md_cell(text):
    return str(text).replace("|", "\\|").replace("\n", " ").replace("\r", " ")


def build_report(results, probed_count, advertised_count):
    lines = [
        "# Obol Stack Smoke Report",
        "",
        "- Target: %s" % results["target"],
        "- Run ID: %s" % results["runId"],
        "- Timestamp: %s" % results["timestamp"],
        "- Result: %d/%d checks passed — score %d/100"
        % (results["passed"], results["total"], results["score100"]),
        "",
        "| Check | OK | Latency (ms) | Detail |",
        "|---|---|---|---|",
    ]
    for check in results["checks"]:
        name = check["name"]
        if check.get("informational"):
            name += " (info)"
        lines.append(
            "| %s | %s | %d | %s |"
            % (_md_cell(name), "yes" if check["ok"] else "no", check["ms"], _md_cell(check["detail"]))
        )
    if advertised_count > probed_count:
        lines.append("")
        lines.append("Probed %d of %d advertised services" % (probed_count, advertised_count))
    return "\n".join(lines) + "\n"


# ---------------------------------------------------------------------------
# Probe driver
# ---------------------------------------------------------------------------

def run_probe(target_raw, run_id, out_base):
    target = normalize_target(target_raw)
    if not target.startswith(("http://", "https://")):
        raise SystemExit(
            "error: target must be an absolute http(s) URL (got %r) — the "
            "normalized target is hashed into the on-chain requestHash, so "
            "always pass the scheme explicitly" % target_raw
        )
    if run_id is None or not str(run_id).strip():
        run_id = default_run_id()
    run_id = str(run_id).strip()
    if not RUN_ID_RE.match(run_id) or set(run_id) == {"."}:
        # A buyer can suggest the run id; "." / ".." would escape the
        # per-run directory under the report root.
        raise SystemExit("error: --run-id must match ^[A-Za-z0-9._-]+$ and not be dots-only (got %r)" % run_id)

    log("smoke probe: target=%s run-id=%s" % (target, run_id))

    checks = []
    checks.append(check_skill_md(target))
    log("  [%s] skill-md: %s" % ("ok" if checks[-1]["ok"] else "FAIL", checks[-1]["detail"]))

    services_check, services = check_services_json(target)
    checks.append(services_check)
    log("  [%s] services-json: %s" % ("ok" if services_check["ok"] else "FAIL", services_check["detail"]))

    advertised = len(services) if services_check["ok"] else 0
    probed = 0
    if services_check["ok"] and services:
        for service in sorted(services, key=lambda s: s["name"])[:MAX_SERVICES]:
            check = check_service_402(target, service)
            checks.append(check)
            probed += 1
            log("  [%s] %s: %s" % ("ok" if check["ok"] else "FAIL", check["name"], check["detail"]))

    info = check_agent_registration(target)
    checks.append(info)
    log("  [%s] agent-registration (info): %s" % ("ok" if info["ok"] else "FAIL", info["detail"]))

    counted = [c for c in checks if not c.get("informational")]
    passed = sum(1 for c in counted if c["ok"])
    total = len(counted)  # always >= 2 (skill-md + services-json)

    results = {
        "version": VERSION,
        "target": target,
        "runId": run_id,
        "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "checks": checks,
        "passed": passed,
        "total": total,
        "score255": (255 * passed) // total,
        "score100": (100 * passed) // total,
        "reportSha256": "",
        "permalink": "",
    }

    run_dir = os.path.join(out_base, host_slug(target), run_id)
    os.makedirs(run_dir, exist_ok=True)

    report = build_report(results, probed, advertised)
    report_bytes = report.encode("utf-8")
    report_path = os.path.join(run_dir, "report.md")
    with open(report_path, "wb") as fh:
        fh.write(report_bytes)
    # reportSha256 = sha256 over the EXACT bytes written to disk (the same
    # bytes gh_post.py base64s into the GitHub PUT). Computed after the final
    # report write, before results.json.
    results["reportSha256"] = hashlib.sha256(report_bytes).hexdigest()

    with open(os.path.join(run_dir, "results.json"), "w", encoding="utf-8") as fh:
        fh.write(json.dumps(results, indent=2) + "\n")

    log("run dir: %s" % os.path.abspath(run_dir))
    return results, run_dir


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

def _load_gh_post():
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    import gh_post  # noqa: E402  (sibling script in this skill)

    return gh_post


def cmd_probe(args):
    results, _ = run_probe(args.target, args.run_id, args.out_dir)
    print(json.dumps(results, indent=2))
    return 0


def cmd_post(args):
    gh_post = _load_gh_post()
    try:
        results, _, _ = gh_post.post_run(args.run_dir)
    except gh_post.PostError as exc:
        log("error: %s" % exc)
        log("report remains local; re-run: python3 smoke.py post %s" % args.run_dir)
        return 1
    print(json.dumps(results, indent=2))
    return 0


def cmd_run(args):
    results, run_dir = run_probe(args.target, args.run_id, args.out_dir)
    if os.environ.get("GITHUB_TOKEN", "").strip() and os.environ.get("GITHUB_REPORT_REPO", "").strip():
        gh_post = _load_gh_post()
        try:
            results, _, _ = gh_post.post_run(run_dir)
        except gh_post.PostError as exc:
            print(json.dumps(results, indent=2))
            log("error: %s" % exc)
            log("report remains local; re-run: python3 smoke.py post %s" % run_dir)
            return 1
    else:
        log("GITHUB_TOKEN/GITHUB_REPORT_REPO not set; report kept local (no GitHub post)")
    print(json.dumps(results, indent=2))
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="smoke.py",
        description="Read-only smoke probe of an Obol Stack public surface (never pays, never signs).",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_probe = sub.add_parser("probe", help="probe checks only; writes report.md + results.json, no network writes")
    p_probe.add_argument("target", nargs="?", help="target base URL, e.g. https://<tunnel-host> or http://obol.stack:8080")
    p_probe.add_argument("--target", dest="target_flag", help="alternative to the positional target")
    p_probe.add_argument("--run-id", help="run identifier (^[A-Za-z0-9._-]+$); default <utc-stamp>-<6hex>")
    p_probe.add_argument("--out-dir", default="./smoke", help="base output dir (default ./smoke)")
    p_probe.set_defaults(func=cmd_probe)

    p_post = sub.add_parser("post", help="commit an existing run dir's report.md to GitHub (env GITHUB_TOKEN + GITHUB_REPORT_REPO)")
    p_post.add_argument("run_dir", help="run dir written by probe, e.g. ./smoke/<target-host>/<runId>")
    p_post.set_defaults(func=cmd_post)

    p_run = sub.add_parser("run", help="probe + post one-shot (host/manual use; agents should run probe then post)")
    p_run.add_argument("target", nargs="?", help="target base URL")
    p_run.add_argument("--target", dest="target_flag", help="alternative to the positional target")
    p_run.add_argument("--run-id", help="run identifier")
    p_run.add_argument("--out-dir", default="./smoke", help="base output dir (default ./smoke)")
    p_run.set_defaults(func=cmd_run)

    args = parser.parse_args(argv)
    if hasattr(args, "target"):
        target = args.target or getattr(args, "target_flag", None)
        if not target:
            parser.error("a target base URL is required (positional or --target)")
        args.target = target
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
