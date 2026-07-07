# Remote QA Worktrees

Use this reference when running remote QA on the two QA machines with sudo
access. Default to direct `obol` CLI commands. Use repo flow scripts only for
explicit release-smoke/full-flow gates or named flow regressions.

## Rules

- Never use the shared source checkout directly for QA.
- Always create a separate worktree per run.
- Assume parallel stack tests may already be running.
- Treat the QA runner and LLM inference endpoint as separate inputs. Set
  `OBOL_LLM_ENDPOINT` and `OBOL_LLM_MODEL` explicitly; do not infer either one
  from the machine running the flow.
- Never run broad host cleanup such as global Docker/k3d purges.
- Delete only stacks whose stack IDs are recorded in the QA worktree.
- Do not record hostnames, personal paths, or secrets in the skill.
- Do not create custom shell scripts. Use `tmux` to run the exact CLI command
  sequence when a long run needs to continue unattended.
- Never edit flow files inside a QA worktree to make a smoke pass. If the flow
  is stale, fix it in the repo and rerun from that commit.

## Create Worktree

```bash
BASE=${OBOL_QA_BASE:-$HOME/obol-stack-src}
QA_PARENT=${OBOL_QA_PARENT:-$HOME}
TOOL_ROOT=${OBOL_QA_TOOL_ROOT:-$HOME/.workspace/bin}
FOUNDRY_BIN=${FOUNDRY_BIN:-$HOME/.foundry/bin}
SHA=$(git -C "$BASE" rev-parse --short HEAD)
QA=$QA_PARENT/obol-stack-qa-$(date +%Y%m%d-%H%M%S)-$SHA

git -C "$BASE" worktree add --detach "$QA" HEAD
cp "$BASE/.env" "$QA/.env"
chmod 600 "$QA/.env"
mkdir -p "$QA/.workspace/bin"
for tool in kubectl helm helmfile k3d k9s openclaw obol; do
  [ -x "$TOOL_ROOT/$tool" ] && ln -sf "$TOOL_ROOT/$tool" "$QA/.workspace/bin/$tool"
done
```

If the source checkout is not `$HOME/obol-stack-src`, set `OBOL_QA_BASE`.

## Refresh Existing Worktree

When syncing a local integration branch into an existing QA worktree, preserve
per-worktree secrets and runtime state. Do not use a raw `rsync --delete` that
can remove `.env`.

```bash
rsync -az --delete \
  --exclude '.git' \
  --exclude '.env' \
  --exclude '.envB' \
  --exclude '.workspace*' \
  --exclude '.tmp' \
  ./ "$QA/"

test -s "$QA/.env" || cp "$BASE/.env" "$QA/.env"
chmod 600 "$QA/.env"
```

## CLI-First QA Session

Use this shape for ordinary remote validation:

```bash
cd "$QA"
export PATH="$QA/.workspace/bin:$FOUNDRY_BIN:$TOOL_ROOT:$PATH"
export OBOL_DEVELOPMENT=true
export OBOL_NONINTERACTIVE=true
go build -o .workspace/bin/obol ./cmd/obol
obol stack init --force
obol stack up
obol model setup custom --endpoint "$OBOL_LLM_ENDPOINT" --model "$OBOL_LLM_MODEL"
obol model prefer "$OBOL_LLM_MODEL"
obol model sync
obol sell list
obol kubectl get pods -A
```

For long direct CLI runs, start tmux with the command sequence inline. Keep logs
under `$QA/.tmp/` and avoid writing a wrapper script.

## Launch Release-Gate Flow

Full seller/buyer QA needs an OpenAI-compatible endpoint reachable from the QA
runner and from the in-cluster LiteLLM pods. Set `OBOL_LLM_MODEL` to an id
returned by `/models`.

```bash
cd "$QA"
export PATH="$QA/.workspace/bin:$FOUNDRY_BIN:$TOOL_ROOT:$PATH"
export OBOL_LLM_ENDPOINT=${OBOL_LLM_ENDPOINT:-http://127.0.0.1:8000/v1}
export OBOL_LLM_MODEL=${OBOL_LLM_MODEL:-qwen36-deep}
ts=$(date +%Y%m%d-%H%M%S)
log="$QA/.tmp/flow-14-$ts.log"
art="$QA/.tmp/flow-14-$ts-artifacts"
mkdir -p "$art"

tmux new-session -d -s "qa-flow14-$ts" \
  "cd $QA && PATH=$PATH OBOL_DEVELOPMENT=true OBOL_NONINTERACTIVE=true OBOL_LLM_ENDPOINT=$OBOL_LLM_ENDPOINT OBOL_LLM_MODEL=$OBOL_LLM_MODEL FLOW14_ARTIFACT_DIR=$art bash flows/flow-14-live-obol-base-sepolia.sh > $log 2>&1; rc=\$?; printf '\n__FLOW14_DONE_RC__=%s\n' \"\$rc\" >> $log"
```

For full release smoke, use the same env plus:

```bash
RELEASE_SMOKE_INCLUDE_OBOL=true
RELEASE_SMOKE_INCLUDE_OBOL_FORK=true
```

Monitor:

```bash
tmux has-session -t "qa-flow14-$ts" 2>/dev/null && echo RUNNING || echo NOT_RUNNING
tail -n 200 "$log"
```

## Fast Failure Checks

Before a full release smoke, spend two minutes ruling out environment drift:

```bash
curl -fsS "$OBOL_LLM_ENDPOINT/models" | head
helm repo add bedag https://bedag.github.io/helm-charts/ >/dev/null 2>&1 || true
helm pull bedag/raw --version 2.0.2 --destination "$QA/.tmp" >/dev/null
docker pull registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.18.0 >/dev/null
k3d cluster list
```

If `helm pull bedag/raw --version 2.0.2` times out, Hermes agent install can
fail before the flow reaches product logic. Record it as a dependency/bootstrap
blocker and rerun after the repo or cache is healthy; do not mutate the flow.
The kube-state-metrics pull catches the flow-02 monitoring dependency before a
full smoke spends its first timeout waiting on a registry path.

## Cleanup

```bash
cd "$QA"
export PATH="$QA/.workspace/bin:$FOUNDRY_BIN:$TOOL_ROOT:$PATH"

for ws in .workspace .workspace-alice .workspace-bob; do
  if [ -f "$ws/config/.stack-id" ]; then
    sid=$(tr -d '[:space:]' < "$ws/config/.stack-id")
    [ -n "$sid" ] && k3d cluster delete "obol-stack-$sid" >/dev/null 2>&1 || true
  fi
done
```

After saving needed logs/artifacts:

```bash
if ! git -C "$BASE" worktree remove --force "$QA"; then
  stale_root="$QA_PARENT/.obol-qa-stale"
  mkdir -p "$stale_root"
  mv "$QA" "$stale_root/$(basename "$QA")-$(date +%Y%m%d-%H%M%S)"
  git -C "$BASE" worktree prune
fi
```

Root-owned runtime files can block deletion. Move stale worktrees aside instead of deleting arbitrary host paths.
