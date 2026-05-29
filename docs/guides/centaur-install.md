# Install Centaur on Obol Stack

[Centaur](https://github.com/paradigmxyz/centaur) is Paradigm's open-source
orchestrator of isolated coding agents — you @-mention a bot in Slack, the
bot spawns a per-conversation sandbox with shell + git + Python + Node, and
your harness of choice (codex / claude-code / amp / pi-mono) drives the work
back into the thread.

Running it on the Obol stack gets you two things you don't get from a
raw-Kubernetes install:

1. **Every Centaur agent runs through LiteLLM**, so any model in
   `obol model list` — including `paid/<model>` aliases purchased via x402 —
   is available inside sandboxes with zero extra config.
2. **One-command install with auto-generated secrets**: postgres password,
   firewall CA, signing keys, and the LiteLLM master key are wired up by a
   chart-managed bootstrap Job. You only paste Slack tokens.

> [!IMPORTANT]
> v1 supports a single LLM harness (`codex`, OpenAI-compatible through
> LiteLLM). Multi-harness selection, 1Password Connect, and gVisor sandboxing
> ship later.

> [!IMPORTANT]
> The bootstrap Job copies the LiteLLM master key into the sandbox env so
> agents can call `paid/*` models. This is acceptable for single-user
> installs where your obol wallet bounds the spend. Multi-tenant deployments
> should wait for v2 (per-install LiteLLM virtual keys).

## What you'll need

- Obol stack running locally (`obol stack up`).
- A Slack app (instructions below) with three secrets ready: bot token,
  signing secret, and a service-to-service API key.
- Cloudflare tunnel hostname (auto-provisioned by `obol stack up`).
- ~3 GB of free disk for the bundled Postgres PVC.

## Step 1 — Create the Slack app

1. Visit [api.slack.com/apps](https://api.slack.com/apps) → **Create New App** → **From scratch**.
2. **OAuth & Permissions** → add bot scopes: `app_mentions:read`, `chat:write`,
   `channels:history`, `groups:history`, `im:history`, `mpim:history`.
3. **Install to Workspace** → copy the **Bot User OAuth Token** (`xoxb-...`).
   That's your `SLACK_BOT_TOKEN`.
4. **Basic Information** → copy the **Signing Secret**. That's your
   `SLACK_SIGNING_SECRET`.
5. Mint a random string for `SLACKBOT_API_KEY` (used between slackbot ↔ api
   internally — not Slack-issued):
   ```bash
   openssl rand -hex 32
   ```
6. **Event Subscriptions** → paste this URL once you have your tunnel hostname
   (you'll get it back from `obol app sync centaur`):
   ```
   https://<your-tunnel-host>/api/webhooks/slack
   ```
7. Subscribe to bot events: `app_mention`, `message.channels`,
   `message.groups`, `message.im`, `message.mpim`.

## Step 2 — Install

```bash
export SLACK_BOT_TOKEN=xoxb-...
export SLACK_SIGNING_SECRET=...
export SLACKBOT_API_KEY=$(openssl rand -hex 32)

obol app install obol/centaur \
  --set slack.botToken=$SLACK_BOT_TOKEN \
  --set slack.signingSecret=$SLACK_SIGNING_SECRET \
  --set slack.botApiKey=$SLACKBOT_API_KEY

obol app sync centaur
```

The bootstrap Job runs automatically before the main pods come up; it
generates the postgres password, firewall CA, iron-proxy management key,
sandbox signing key, and reads the LiteLLM master key from `llm/litellm-secrets`.
No further secret juggling.

When `obol app sync` finishes you'll see the slack webhook URL and the
internal REST endpoint:

```
tip: Configure your Slack app event subscription:
     https://<tunnel-host>/api/webhooks/slack
tip: REST API: http://centaur.obol.stack:8080
```

Paste the webhook URL into your Slack app (step 1.6 above).

## Step 3 — Use it

In any Slack channel where you've added the bot, mention it:

> @centaur write a quick prime-sieve in python and report wall-clock for 1e8

The bot opens a thread, the API spawns an isolated sandbox pod, the harness
runs against your preferred LiteLLM-routed model, and progress streams back
into the thread.

## What model runs inside the sandbox?

Whatever's at the head of `obol model list`. The sandbox calls
`litellm.llm.svc.cluster.local:4000` with OpenAI semantics; LiteLLM picks the
model. To change it:

```bash
obol model prefer paid/aeon   # use a paid x402 model you've bought
obol model prefer qwen3.5:9b  # use local Ollama (free, slower)
obol model sync               # propagate
```

Centaur picks up the new default on the next sandbox spawn.

## Troubleshooting

**Slack `url_verification` fails on event subscription.** Tunnel may not be
running. Check `obol tunnel status`.

**Sandbox spawns but agent times out.** Likely LiteLLM-side. Port-forward and
inspect: `kubectl port-forward -n llm svc/litellm 14000:4000`, then
`curl http://127.0.0.1:14000/v1/models`.

**`centaur-bootstrap` Job in Error state.** Almost always RBAC: the Job needs
read access to `llm/litellm-secrets`. Confirm `obol stack up` finished
successfully before installing.

**Clock skew on Slack webhooks.** Slack rejects webhooks more than 5 minutes
out of date. If your k3d host has drifted (laptop suspend, VM clock issues),
slack integration silently fails. Resync your host clock.

## Tearing down

```bash
obol app delete centaur
```

Removes the namespace, PVC (deletes the postgres data), and all generated
secrets. Slack app definition stays in your Slack workspace and can be reused
for a future install.

## Reconfiguring

Edit `~/.config/obol/applications/centaur/<id>/values.yaml`, then:

```bash
obol app sync centaur
```

There's no `obol app configure` wizard yet — for v1 the edit-and-sync loop is
the supported reconfigure path.
