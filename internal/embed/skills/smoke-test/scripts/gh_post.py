#!/usr/bin/env python3
"""gh_post.py — commit a smoke report.md to the seller-owned public GitHub repo.

Posting contract:
  - Base https://api.github.com (override with GITHUB_API_BASE for tests only).
  - Repo from env GITHUB_REPORT_REPO, validated against
    ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$.
  - Token from env GITHUB_TOKEN ONLY. By construction this script never
    prints headers or env values, never puts the token in argv, and redacts
    the token from every error string before it can reach stderr.
  - Headers on every call: Authorization Bearer, Accept
    application/vnd.github+json, X-GitHub-Api-Version 2022-11-28, and the
    obol-smoke-test User-Agent.
  - Redirects are NEVER followed (the default opener would replay the
    Authorization header to the redirect target, even cross-host); a 3xx
    from the API is surfaced as the final status and is a hard failure.
  - Path: reports/<target-host>/<runId>.md (target-host = lowercase hostname
    with ":<port>" rewritten to "-<port>").
  - Create-or-update: GET contents for the existing blob sha (a read, not a
    write; other-than-200/404 retried once then abort), then PUT. On 409
    re-GET the sha once and retry the PUT once. On 403/429 honor Retry-After
    (fallback: x-ratelimit-reset delta), sleep min(value, 30s), max 2
    retries. On 5xx/connection errors exponential 2s/4s, max 2 retries.
    Total post budget 25s; on exhaustion exit non-zero with a re-run hint.
  - Permalink = https://github.com/{o}/{r}/blob/{PUT .commit.sha}/{path}
    (commit-pinned, NOT the branch-floating .content.html_url).
  - Write #2 (best-effort, failure never fails the run):
    reports/<target-host>/latest.md with only runId, score line, permalink.
    Exactly <= 2 writes per run; results.json is NEVER committed.

Usage:
    GITHUB_TOKEN=... GITHUB_REPORT_REPO=owner/repo \
        python3 gh_post.py <runDir>

stdout payload is exactly two lines (everything else goes to stderr):
    permalink: <commit-pinned blob URL>
    content-sha: <blob sha of the committed report.md>

Exit codes: 0 on success (even if the best-effort latest.md write failed),
non-zero when the report could not be committed (report stays local; `post`
is re-runnable).

Stdlib only: argparse/base64/json/os/re/sys/time/urllib.
"""

import argparse
import base64
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

# Shared normalization with the probe — same skill scripts/ dir.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from smoke import USER_AGENT, host_slug  # noqa: E402

API_BASE = os.environ.get("GITHUB_API_BASE", "https://api.github.com").rstrip("/")
REPO_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
POST_BUDGET_SECONDS = 25
MAX_SLEEP_SECONDS = 30


class PostError(Exception):
    """Operational posting failure. Messages are pre-redacted."""


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    """Never follow redirects: the default handler re-sends every request
    header — including Authorization — to the redirect target, even
    cross-host, which would leak the token (mirrors smoke.py's _NoRedirect;
    here the request carries the only credential). A 3xx comes back as the
    final status and the retry ladder treats it as a hard failure."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


_OPENER = urllib.request.build_opener(_NoRedirect)


def _log(msg):
    print(msg, file=sys.stderr)


def _redact(text, token):
    text = str(text)
    return text.replace(token, "[REDACTED]") if token else text


def _remaining(deadline):
    return deadline - time.monotonic()


def _check_deadline(deadline):
    if _remaining(deadline) <= 0:
        raise PostError(
            "post budget (%ds) exhausted; report remains local — re-run `post <runDir>`"
            % POST_BUDGET_SECONDS
        )


def _sleep_within(seconds, deadline):
    seconds = min(seconds, MAX_SLEEP_SECONDS)
    if seconds >= _remaining(deadline):
        raise PostError(
            "post budget (%ds) exhausted while backing off; report remains local — "
            "re-run `post <runDir>`" % POST_BUDGET_SECONDS
        )
    time.sleep(seconds)


def _retry_after_seconds(headers):
    """Retry-After seconds, falling back to the x-ratelimit-reset delta."""
    raw = headers.get("Retry-After") or headers.get("retry-after")
    if raw:
        try:
            return max(1, int(float(raw)))
        except ValueError:
            pass
    reset = headers.get("x-ratelimit-reset") or headers.get("X-RateLimit-Reset")
    if reset:
        try:
            return max(1, int(float(reset)) - int(time.time()))
        except ValueError:
            pass
    return 2


def _gh_request(method, url, token, payload=None, deadline=None):
    """One GitHub API call. Returns (status, headers_dict, body_bytes).
    status == 0 means no HTTP response (connection-level failure); the body
    then carries a redacted reason. Never raises, never logs headers."""
    headers = {
        "Authorization": "Bearer " + token,
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": USER_AGENT,
    }
    data = None
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    timeout = 10.0
    if deadline is not None:
        timeout = max(1.0, min(10.0, _remaining(deadline)))
    req = urllib.request.Request(url, data=data, method=method, headers=headers)
    try:
        with _OPENER.open(req, timeout=timeout) as resp:
            return resp.getcode(), dict(resp.headers), resp.read()
    except urllib.error.HTTPError as exc:
        try:
            body = exc.read()
        except Exception:
            body = b""
        return exc.code, dict(exc.headers or {}), body
    except Exception as exc:  # URLError, timeout, ConnectionError, ...
        return 0, {}, _redact(exc, token).encode("utf-8")


def _body_snippet(body, token):
    return _redact(body.decode("utf-8", "replace")[:200], token)


def _contents_url(owner_repo, path):
    return "%s/repos/%s/contents/%s" % (API_BASE, owner_repo, urllib.parse.quote(path, safe="/"))


def _get_existing_sha(owner_repo, path, token, deadline):
    """Existing blob sha for create-or-update. 200 -> sha, 404 -> None,
    anything else retried once then abort."""
    url = _contents_url(owner_repo, path)
    for attempt in (1, 2):
        _check_deadline(deadline)
        status, _, body = _gh_request("GET", url, token, deadline=deadline)
        if status == 200:
            try:
                return json.loads(body).get("sha") or None
            except ValueError:
                return None
        if status == 404:
            return None
        if attempt == 1:
            continue
        raise PostError(
            "GET contents %s failed (status %s): %s" % (path, status, _body_snippet(body, token))
        )


def _put_file(owner_repo, path, message, content_bytes, sha, token, deadline):
    """PUT one file via the contents API with the contract's retry ladder.
    Returns the parsed PUT response JSON."""
    url = _contents_url(owner_repo, path)
    body = {"message": message, "content": base64.b64encode(content_bytes).decode("ascii")}
    if sha:
        body["sha"] = sha
    rate_retries = 0
    server_retries = 0
    conflict_retried = False
    while True:
        _check_deadline(deadline)
        status, headers, raw = _gh_request("PUT", url, token, payload=body, deadline=deadline)
        if status in (200, 201):
            try:
                return json.loads(raw)
            except ValueError:
                raise PostError("PUT %s returned %d but unparseable JSON" % (path, status))
        if status == 409 and not conflict_retried:
            conflict_retried = True
            new_sha = _get_existing_sha(owner_repo, path, token, deadline)
            if new_sha:
                body["sha"] = new_sha
            else:
                body.pop("sha", None)
            continue
        if status in (403, 429) and rate_retries < 2:
            rate_retries += 1
            _sleep_within(_retry_after_seconds(headers), deadline)
            continue
        if (status >= 500 or status == 0) and server_retries < 2:
            server_retries += 1
            _sleep_within(2 ** server_retries, deadline)  # 2s, then 4s
            continue
        raise PostError(
            "PUT %s failed (status %s): %s" % (path, status, _body_snippet(raw, token))
        )


def post_run(run_dir):
    """Commit <run_dir>/report.md per the contract, update results.json with
    the commit-pinned permalink, best-effort update latest.md.
    Returns (results_dict, permalink, content_sha). Raises PostError on
    operational failure (report stays local; re-runnable)."""
    token = os.environ.get("GITHUB_TOKEN", "").strip()
    owner_repo = os.environ.get("GITHUB_REPORT_REPO", "").strip()
    if not token:
        raise PostError("GITHUB_TOKEN is not set (provision it via the hermes-env Secret)")
    if not REPO_RE.match(owner_repo):
        raise PostError(
            "GITHUB_REPORT_REPO=%r is not <owner>/<repo> "
            "(^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$)" % owner_repo
        )

    results_path = os.path.join(run_dir, "results.json")
    report_path = os.path.join(run_dir, "report.md")
    try:
        with open(results_path, "r", encoding="utf-8") as fh:
            results = json.load(fh)
        with open(report_path, "rb") as fh:
            report_bytes = fh.read()
    except (OSError, ValueError) as exc:
        raise PostError("cannot load run dir %s: %s" % (run_dir, _redact(exc, token)))

    run_id = str(results.get("runId", "")).strip()
    target = str(results.get("target", "")).strip()
    if not run_id or not target:
        raise PostError("results.json missing runId/target — re-run the probe")
    passed = int(results.get("passed", 0))
    total = int(results.get("total", 0))
    score100 = int(results.get("score100", 0))

    target_host = host_slug(target)
    report_repo_path = "reports/%s/%s.md" % (target_host, run_id)
    deadline = time.monotonic() + POST_BUDGET_SECONDS

    # Write #1 — the report itself (create-or-update).
    _log("posting %s to %s:%s" % (report_path, owner_repo, report_repo_path))
    sha = _get_existing_sha(owner_repo, report_repo_path, token, deadline)
    put = _put_file(
        owner_repo,
        report_repo_path,
        "smoke: %s %s %d/%d" % (target_host, run_id, passed, total),
        report_bytes,
        sha,
        token,
        deadline,
    )
    try:
        commit_sha = put["commit"]["sha"]
        content_sha = put["content"]["sha"]
    except (KeyError, TypeError):
        raise PostError("PUT response missing commit/content sha")

    permalink = "https://github.com/%s/blob/%s/%s" % (owner_repo, commit_sha, report_repo_path)
    results["permalink"] = permalink
    try:
        with open(results_path, "w", encoding="utf-8") as fh:
            fh.write(json.dumps(results, indent=2) + "\n")
    except OSError as exc:
        _log("warning: could not update results.json: %s" % _redact(exc, token))

    # Write #2 — best-effort latest.md pointer; failure does NOT fail the run.
    latest_repo_path = "reports/%s/latest.md" % target_host
    latest_bytes = (
        "Run ID: %s\nResult: %d/%d checks passed — score %d/100\nReport: %s\n"
        % (run_id, passed, total, score100, permalink)
    ).encode("utf-8")
    try:
        latest_sha = _get_existing_sha(owner_repo, latest_repo_path, token, deadline)
        _put_file(
            owner_repo,
            latest_repo_path,
            "smoke: %s latest %s" % (target_host, run_id),
            latest_bytes,
            latest_sha,
            token,
            deadline,
        )
    except PostError as exc:
        _log("warning: latest.md update skipped: %s" % exc)

    return results, permalink, content_sha


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="gh_post.py",
        description="Commit a smoke run's report.md to the seller-owned public report repo.",
    )
    parser.add_argument("run_dir", help="run dir written by smoke.py probe, e.g. ./smoke/<target-host>/<runId>")
    args = parser.parse_args(argv)
    try:
        _, permalink, content_sha = post_run(args.run_dir)
    except PostError as exc:
        _log("error: %s" % exc)
        _log("report remains local; re-run: python3 gh_post.py %s" % args.run_dir)
        return 1
    # The ONLY stdout payload lines:
    print("permalink: %s" % permalink)
    print("content-sha: %s" % content_sha)
    return 0


if __name__ == "__main__":
    sys.exit(main())
