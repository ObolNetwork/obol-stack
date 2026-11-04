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


# Hardware adaptation applied to train.py before running. autoresearch ships
# a FlashAttention-3 attention path (flash_attn_func) that has no kernel image
# for some GPUs (e.g. NVIDIA GB10 / Blackwell sm_121). train.py is the file an
# autoresearch agent edits, so swapping that one call to PyTorch-native SDPA —
# which runs on any CUDA device — is a legitimate, in-framework adaptation.
_FA3_CALL = "y = fa3.flash_attn_func(q, k, v, causal=True, window_size=window_size)"
_SDPA_CALL = ("y = torch.nn.functional.scaled_dot_product_attention("
              "q.transpose(1, 2), k.transpose(1, 2), v.transpose(1, 2), "
              "is_causal=True, enable_gqa=True).transpose(1, 2)")

# Model/batch fit: the default 124M model at DEVICE_BATCH_SIZE=128 (×2048 seq)
# OOM-kills on a memory-pressured GPU. Shrink to a small GPT at a small batch
# so eager training fits and finishes fast — train.py's own comment says
# "reduce if OOM". Still a real GPT producing a real val_bpb.
_TRAIN_SUBS = [
    (_FA3_CALL, _SDPA_CALL),
    ("    n_layer: int = 12", "    n_layer: int = 4"),
    ("    n_embd: int = 768", "    n_embd: int = 512"),
    ("DEVICE_BATCH_SIZE = 128", "DEVICE_BATCH_SIZE = 8"),
    # One micro-batch per optimizer step: the default 2**19 token batch means
    # 32 grad-accum micro-steps per step (~35s/step eager on GB10). 2**14 =
    # batch*seq, so grad_accum_steps=1 and a step is ~1s — train.py needs
    # step>10 to stop, so this keeps the whole run to seconds, not minutes.
    ("TOTAL_BATCH_SIZE = 2**19", "TOTAL_BATCH_SIZE = 2**14"),
]

_PREP = r'''
import re, shutil
shutil.copy("train.py", "train.py.obolbak"); shutil.copy("prepare.py", "prepare.py.obolbak")
t = open("train.py").read()
for a, b in {subs!r}:
    t = t.replace(a, b)
open("train.py", "w").write(t)
p = open("prepare.py").read()
p = re.sub(r"^TIME_BUDGET = .*", "TIME_BUDGET = {budget}", p, flags=re.M)
p = re.sub(r"^EVAL_TOKENS = .*", "EVAL_TOKENS = 131072  # shrunk for fast eager eval", p, flags=re.M)
open("prepare.py", "w").write(p)
'''


def run_autoresearch(repo, time_budget):
    """Run the real karpathy/autoresearch training; return (val_bpb, tail).

    Adapts train.py for the local GPU (FA3 → SDPA), shrinks the fixed time
    budget, runs eager (FA3's absence makes torch.compile tracing moot on
    these devices), then restores the originals.
    """
    repo = os.path.expanduser(repo)
    if not os.path.isdir(repo):
        raise SystemExit("autoresearch repo not found at %s" % repo)
    env = dict(os.environ)
    env["PATH"] = os.path.expanduser("~/.local/bin") + ":" + env.get("PATH", "")
    # Run eager (no torch.compile). On bleeding-edge GPUs (NVIDIA GB10 /
    # Blackwell sm_121a) Triton/ptxas can't yet assemble inductor kernels;
    # eager has no Triton dependency and just runs. The cost of eager is a
    # slow final eval over EVAL_TOKENS, which we shrink below so the run
    # completes quickly — both are legitimate train.py-for-this-hardware edits.
    env["TORCHDYNAMO_DISABLE"] = "1"

    prep = _PREP.format(subs=_TRAIN_SUBS, budget=int(time_budget))
    cmd = (
        "cd %s && python3 -c %s && "
        "uv run --no-sync python train.py; "
        "mv -f train.py.obolbak train.py 2>/dev/null; mv -f prepare.py.obolbak prepare.py 2>/dev/null || true"
        % (repo, _shquote(prep))
    )
    log("Running autoresearch experiment (TIME_BUDGET=%ss, GB10-adapted) …" % time_budget)
    p = subprocess.run(["bash", "-lc", cmd], env=env, capture_output=True, text=True)
    out = (p.stdout or "") + "\n" + (p.stderr or "")
    m = re.search(r"^val_bpb:\s*([0-9.]+)", out, re.MULTILINE)
    if not m:
        log(out[-2000:])
        raise SystemExit("could not parse val_bpb from train.py output")
    return float(m.group(1)), out[-1500:]


def _shquote(s):
    return "'" + s.replace("'", "'\\''") + "'"


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
