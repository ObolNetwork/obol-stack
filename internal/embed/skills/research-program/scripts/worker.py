#!/usr/bin/env python3
"""Auto-research worker — the runner side of an obol decentralized research program.

Joins a program's collective knowledge base over the open internet (the
owner's Cloudflare URL), runs one real experiment (karpathy/autoresearch
nanoGPT by default), and posts its metric back. The KB decides KEEP/REJECT
and tracks the champion; rewards are settled by the owner.

Stdlib only (urllib/json/subprocess) so it drops onto any runner with no
install. The membership flow is RFC 8628 device-auth: print a user code, the
owner approves it, we poll for a member token, then every KB call carries it.

Usage:
  python3 worker.py --kb <public-url> --program <id> --worker <name> \\
      [--time-budget 60] [--repo ~/autoresearch] [--experiment "<shell>"]

Without --experiment it runs the autoresearch baseline:
  cd <repo> && TIME_BUDGET override && uv run train.py   → parses 'val_bpb:'.
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request


def _post(url, token, body, timeout=120):
    data = json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method="POST")
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read().decode())


def _get(url, token, timeout=60):
    req = urllib.request.Request(url, method="GET")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read().decode())


def log(msg):
    print(msg, file=sys.stderr, flush=True)


def join(kb, program, worker):
    """Device-auth: get a code, wait for owner approval, return a member token."""
    grant = _post(kb + "/auth/device/code", None, {"worker": worker})
    user_code = grant["user_code"]
    interval = max(2, int(grant.get("interval", 5)))
    log("")
    log("=" * 52)
    log("  JOIN CODE for %s:   %s" % (program, user_code))
    log("  Owner runs:  obol research approve %s" % user_code)
    log("=" * 52)
    log("")
    deadline = time.time() + int(grant.get("expires_in", 900))
    while time.time() < deadline:
        res = _post(kb + "/auth/device/token", None, {"device_code": grant["device_code"]})
        if res.get("status") == "authorized":
            log("Admitted to %s." % program)
            return res["token"]
        time.sleep(interval)
    raise SystemExit("join timed out waiting for owner approval")


def run_autoresearch(repo, time_budget):
    """Run the real karpathy/autoresearch training; return (val_bpb, tail)."""
    repo = os.path.expanduser(repo)
    if not os.path.isdir(repo):
        raise SystemExit("autoresearch repo not found at %s" % repo)
    env = dict(os.environ)
    env["PATH"] = os.path.expanduser("~/.local/bin") + ":" + env.get("PATH", "")
    # Shrink the fixed training budget for a fast-but-real run by patching the
    # imported constant via an env shim train.py respects if present, else sed.
    # autoresearch reads TIME_BUDGET from prepare.py; override at runtime.
    cmd = (
        "cd %s && sed -i.bak 's/^TIME_BUDGET = .*/TIME_BUDGET = %d/' prepare.py && "
        "uv run --no-sync python train.py; mv -f prepare.py.bak prepare.py 2>/dev/null || true"
        % (repo, int(time_budget))
    )
    log("Running autoresearch experiment (TIME_BUDGET=%ss) …" % time_budget)
    p = subprocess.run(["bash", "-lc", cmd], env=env, capture_output=True, text=True)
    out = (p.stdout or "") + "\n" + (p.stderr or "")
    m = re.search(r"^val_bpb:\s*([0-9.]+)", out, re.MULTILINE)
    if not m:
        log(out[-2000:])
        raise SystemExit("could not parse val_bpb from train.py output")
    return float(m.group(1)), out[-1500:]


def run_custom(experiment, metric):
    """Run an arbitrary experiment shell command; parse '<metric>: <float>'."""
    p = subprocess.run(["bash", "-lc", experiment], capture_output=True, text=True)
    out = (p.stdout or "") + "\n" + (p.stderr or "")
    m = re.search(r"^%s:\s*([0-9.eE+-]+)" % re.escape(metric), out, re.MULTILINE)
    if not m:
        log(out[-2000:])
        raise SystemExit("could not parse '%s:' from experiment output" % metric)
    return float(m.group(1)), out[-1500:]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--kb", required=True, help="owner KB base URL (Cloudflare)")
    ap.add_argument("--program", required=True)
    ap.add_argument("--worker", required=True)
    ap.add_argument("--time-budget", type=int, default=60)
    ap.add_argument("--repo", default="~/autoresearch")
    ap.add_argument("--experiment", default="", help="custom experiment shell cmd (else autoresearch)")
    args = ap.parse_args()

    kb = args.kb.rstrip("/")

    token = join(kb, args.program, args.worker)

    task = _get(kb + "/task", token)
    metric = task["program"]["criteria"]["metric"]
    champ = task.get("champion")
    log("Task: optimize %s (%s). Current champion: %s" % (
        metric, task["program"]["criteria"]["direction"],
        ("%.6f" % champ["value"]) if champ else "none"))

    if args.experiment:
        value, tail = run_custom(args.experiment, metric)
    else:
        value, tail = run_autoresearch(args.repo, args.time_budget)
    log("Experiment %s = %.6f" % (metric, value))

    res = _post(kb + "/results", token, {"worker": args.worker, "value": value, "output": tail})
    verdict = "KEPT (new champion)" if res.get("champion") else ("ACCEPTED" if res.get("accepted") else "rejected")
    log("Submitted: %s = %.6f → %s (impact %.6f)" % (metric, value, verdict, res.get("impact", 0.0)))
    # Machine-readable final line on stdout.
    print(json.dumps({"worker": args.worker, "metric": metric, "value": value,
                      "accepted": res.get("accepted"), "champion": res.get("champion"),
                      "impact": res.get("impact")}))


if __name__ == "__main__":
    try:
        main()
    except urllib.error.HTTPError as e:
        raise SystemExit("HTTP %s: %s" % (e.code, e.read().decode()[:300]))
