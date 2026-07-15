# docs.obol.org gaps (hackathon setup feedback)

**Audience:** maintainers of GitBook pages under `https://docs.obol.org/obol-stack/*`  
**Source:** hackathon installer walkthrough (v0.13.0)  
**In-repo fixes:** `README.md`, `docs/getting-started.md` (this repository).  
**External site:** still needs the same copy in GitBook (not sourced from this repo).

## Verdict on each report

| # | Claim | Live docs.obol.org (checked) | Product truth (v0.13) | Action |
|---|--------|------------------------------|----------------------|--------|
| 1 | Intro still centers OpenClaw / llmspy | **Mostly fixed** — Intro already says Hermes default + LiteLLM gateway | Hermes default; OpenClaw optional; LiteLLM gateway | Keep watching for stale side pages; no llmspy remains |
| 2 | Pinned version still `v0.1.0` | **Stale pin is `v0.9.0`**, not `v0.1.0` | Current release **v0.13.0** | Quickstart “Specific version” tab → `OBOL_RELEASE=v0.13.0` (or “latest tag”) |
| 3 | Hosts failure exits before resume docs | FAQ only shows `echo … >> /etc/hosts` | Installer does **not** hard-exit; `stack up` retries hosts | Document resume: hosts → `obol stack init` → `obol stack up` → `obol agent init` if needed |
| 4 | `OBOL_NONINTERACTIVE=true` undocumented | Missing | Skips interactive `sudo -v` in `EnsureHostsEntries` | Document on Quickstart + FAQ |
| 5 | Ollama decline → no default Hermes | Missing | `SetupDefault` skips when LiteLLM has no models | Document Ollama prompt + `obol model setup` + `obol agent init` |
| 6 | Cloudflared described as always active | Intro table lists Cloudflared; Quickstart implies temp tunnel on `stack up` | **Dormant** until first sell / `tunnel restart` / permanent setup | Intro + Quickstart: dormant by default |
| 7 | Need `http://obol.stack:8080` + Host header | Weak (8080 only as port-80 fallback) | Traefik routes frontend on `Host: obol.stack`; **localhost:8080 → 404** | Prominently document in Quickstart Step 2 / Explore |

## Suggested GitBook copy (drop-in)

### Quickstart — Specific version tab

```shell
OBOL_RELEASE=v0.13.0 bash <(curl -s https://stack.obol.org)
```

Use the current tag from the GitHub releases page when newer than v0.13.0.

### Quickstart — after install (hosts resume)

If `/etc/hosts` could not be updated, install still completed. Fix hosts, then start the stack:

```shell
echo "127.0.0.1 obol.stack" | sudo tee -a /etc/hosts
obol stack init
obol stack up
# Only if stack up skipped the agent (no model):
obol model setup
obol agent init
```

Automation: `OBOL_NONINTERACTIVE=true` skips sudo password prompts for hosts updates (fails if sudo is not already cached).

### Quickstart — local UI (new callout after Step 2)

Open **http://obol.stack:8080** (or `http://obol.stack/` if port 80 is mapped).

Always use the **`obol.stack` host**. Traefik only serves the frontend for that hostname. `http://localhost:8080` returns **404** even when the stack is healthy.

### Quickstart / Intro — tunnel

The Cloudflare tunnel is **not** activated by a plain `obol stack up`. It stays dormant until the first selling workflow (e.g. `obol sell demo` / `obol sell http`) or an explicit `obol tunnel restart`. For a stable public hostname use `obol tunnel setup --hostname …`.

### FAQ — expand “cannot modify /etc/hosts”

After the manual hosts line:

```shell
obol stack init
obol stack up
```

Also note agent hostnames (`obol-agent.obol.stack`, …) are added on agent install/sync, and that `OBOL_NONINTERACTIVE=true` is the non-interactive escape hatch for sudo.

### FAQ / Quickstart — models

If you skip Ollama at install and never configure a provider, the default Hermes agent is **not** deployed. Run `obol model setup`, then `obol agent init`.

## Owner

| Surface | Repo |
|---------|------|
| README + `docs/getting-started.md` | `ObolNetwork/obol-stack` |
| docs.obol.org GitBook | Obol docs site (not this git tree) |
