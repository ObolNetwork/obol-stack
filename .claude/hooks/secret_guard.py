#!/usr/bin/env python3
"""PreToolUse guard for Bash: keeps wallet keys / keystore passwords out of the
transcript and stops commands that could destroy or publish them.

Decisions:
  deny  -> printing a secret file to stdout, leaking it via git/curl/gist,
           `git clean -x/-X` (deletes the gitignored wallet backups).
  ask   -> anything else touching a secret path, kubectl Secret dumps that
           are not redirected to a file, `obol stack purge`, k3d cluster
           delete, rm of stack config/data dirs, `git add -f`, `git stash -a`.
  allow -> everything else (no output; normal permission rules apply).

Metadata-only commands (ls, stat, shasum, cmp, cp, chmod, mkdir, du) on secret
paths are allowed: they never print contents. See CLAUDE.md "Secrets & wallet
keys" for the policy this enforces.
"""

import json
import re
import shlex
import sys

SECRET_PATH = re.compile(
    r"obol-wallet-backup|\.private_keys|remote-signer-keystore|values-remote-signer\.ya?ml"
    r"|wallet-vault|obol-stack-export|obol-stack-backup|cloudflared-token|keystore\.json"
    r"|UTC--|litellm-secrets|agent-secrets\.json|\.gateway-token|/\.env\b|\.envrc\.local"
)

# Commands whose whole purpose is to emit file contents.
READERS = {
    "cat", "less", "more", "head", "tail", "bat", "base64", "xxd", "od", "hexdump",
    "strings", "nl", "tac", "pbcopy", "open", "code", "vim", "vi", "nano",
}
# Commands that never print file contents.
METADATA_ONLY = {"ls", "stat", "shasum", "sha256sum", "cmp", "cp", "chmod", "mkdir", "du", "test", "[", "true"}
EXFIL = re.compile(
    r"\bgh\s+gist\b|\bcurl\b.*(-F|--form|-T|--upload-file|--data-binary\s+@|-d\s+@)"
    r"|\bscp\b|\brsync\b.*:|\bnc\b|\bpbcopy\b"
)


def segments(cmd: str):
    """Split on shell control operators; return first word of each segment."""
    for part in re.split(r"\|\||&&|[|;&\n]|\$\(|`", cmd):
        part = part.strip().lstrip("(").strip()
        if not part:
            continue
        try:
            words = shlex.split(part, posix=True)
        except ValueError:
            words = part.split()
        # skip leading VAR=value assignments and sudo/env wrappers
        while words and (re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", words[0]) or words[0] in ("sudo", "env", "command", "time")):
            words = words[1:]
        if words:
            yield words[0].rsplit("/", 1)[-1], part


def decide(cmd: str):
    secret = bool(SECRET_PATH.search(cmd))
    segs = list(segments(cmd))
    verbs = {v for v, _ in segs}

    # --- hard denies -------------------------------------------------------
    if re.search(r"\bgit\s+clean\b[^|;&]*\s-[a-zA-Z]*[xX]", cmd):
        return "deny", "git clean -x/-X deletes gitignored wallet backups (obol-wallet-backup-*.json, .private_keys/)."
    if secret and EXFIL.search(cmd):
        return "deny", "Refusing to upload/copy-out a wallet/secret file (gist, curl upload, scp, clipboard)."
    if secret and re.search(r"\bgit\s+(add|commit|stash\s+push)\b", cmd):
        return "deny", "Wallet/secret files must never be staged or committed."
    if secret:
        for verb, part in segs:
            if verb in READERS and SECRET_PATH.search(part):
                return "deny", (
                    f"'{verb}' would print a wallet/secret file into the transcript. Use metadata only "
                    "(ls/stat/shasum), copy it with cp, or extract public fields (address) with a program "
                    "that never prints key material."
                )

    # --- asks ---------------------------------------------------------------
    if re.search(r"\b(obol|kubectl)\b.*\bget\s+secrets?\b", cmd) and re.search(r"\s-o\s*=?\s*(json|yaml|jsonpath|go-template)|--output", cmd):
        if not re.search(r">\s*[^&\s]", cmd):
            return "ask", "kubectl Secret dump to stdout would put secret values in the transcript; redirect to a 0600 file or select only `.data|keys`."
    if re.search(r"\bobol\b.*\bstack\s+purge\b", cmd):
        return "ask", "obol stack purge deletes config (keystore passwords) and, with -f, data (keystores). Confirm wallets are backed up."
    if re.search(r"\bk3d\s+cluster\s+delete\b", cmd):
        return "ask", "Deleting a k3d cluster destroys in-cluster sub-agent wallet Secrets (remote-signer-keystore)."
    if "rm" in verbs and (secret or re.search(r"\.workspace|\.config/obol|\.local/share/obol|wallet-vault", cmd)):
        return "ask", "rm touches stack config/data or wallet material."
    if re.search(r"\bgit\s+add\b.*(\s-f\b|--force)", cmd):
        return "ask", "git add --force bypasses .gitignore (which protects wallet backups)."
    if re.search(r"\bgh\s+gist\s+(create|edit)\b", cmd):
        return "ask", "Gists are public-by-URL; confirm the content holds no keys, passwords, tokens or personal paths."
    if re.search(r"\bgit\s+stash\b.*(\s-a\b|--all)", cmd):
        return "ask", "git stash --all sweeps up gitignored wallet backups; a later drop would delete them."
    if secret and not verbs <= METADATA_ONLY:
        return "ask", "Command touches a wallet/secret path; confirm it cannot print key material or passwords."
    return None, None


def main():
    try:
        payload = json.load(sys.stdin)
    except Exception:
        return 0
    cmd = (payload.get("tool_input") or {}).get("command") or ""
    decision, reason = decide(cmd)
    if decision:
        print(json.dumps({
            "hookSpecificOutput": {
                "hookEventName": "PreToolUse",
                "permissionDecision": decision,
                "permissionDecisionReason": f"[secret-guard] {reason}",
            }
        }))
    return 0


if __name__ == "__main__":
    sys.exit(main())
