# Remote QA Worktrees

Use this reference when running flows on the two remote QA machines with sudo access.

## Rules

- Never use the shared source checkout directly for QA.
- Always create a separate worktree per run.
- Assume parallel stack tests may already be running.
- Never run broad host cleanup such as global Docker/k3d purges.
- Delete only stacks whose stack IDs are recorded in the QA worktree.
- Do not record hostnames, personal paths, or secrets in the skill.

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

## Launch Live OBOL Smoke

Full seller/buyer QA needs an OpenAI-compatible endpoint on the QA machine.
Set `OBOL_LLM_MODEL` to an id returned by `/models`.

```bash
cd "$QA"
export PATH="$QA/.workspace/bin:$FOUNDRY_BIN:$TOOL_ROOT:$PATH"
export OBOL_LLM_ENDPOINT=${OBOL_LLM_ENDPOINT:-http://127.0.0.1:8000/v1}
export OBOL_LLM_MODEL=${OBOL_LLM_MODEL:-qwen36-fast}
ts=$(date +%Y%m%d-%H%M%S)
log="$QA/.tmp/flow-14-$ts.log"
art="$QA/.tmp/flow-14-$ts-artifacts"
mkdir -p "$art"

tmux new-session -d -s "qa-flow14-$ts" \
  "cd $QA && PATH=$PATH OBOL_DEVELOPMENT=true OBOL_NONINTERACTIVE=true OBOL_REUSE_LOCAL_DEV_IMAGES=true OBOL_LLM_ENDPOINT=$OBOL_LLM_ENDPOINT OBOL_LLM_MODEL=$OBOL_LLM_MODEL FLOW14_ARTIFACT_DIR=$art bash flows/flow-14-live-obol-base-sepolia.sh > $log 2>&1; rc=\$?; printf '\n__FLOW14_DONE_RC__=%s\n' \"\$rc\" >> $log"
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
