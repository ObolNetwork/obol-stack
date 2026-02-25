# Skills Host-Path Injection v3

## Problem

The ConfigMap-based skill injection (tar → kubectl create configmap → init container extraction → rollout restart) is fragile, complex, and failed in practice. We need a simpler approach.

## Solution

Write embedded skills directly to the host filesystem path that maps to `/data/.openclaw/skills/` inside the OpenClaw container. This is the native skills directory that OpenClaw watches with a file watcher. No ConfigMap, no init container, no restart needed.

## Key Discovery: Volume Mount Chain

```
HOST $DATA_DIR
  → k3d volume mount → /data on all k3d nodes
    → local-path-provisioner → /data/<namespace>/<pvc-name>/
      → PVC mount in container → /data
```

- **PVC name** (from chart): `openclaw-data`
- **Namespace**: `openclaw-<id>` (e.g. `openclaw-default`)
- **Container mount**: `/data` (persistence.mountPath)
- **State dir**: `/data/.openclaw` (OPENCLAW_STATE_DIR env)
- **Native skills dir watched by OpenClaw**: `/data/.openclaw/skills/`

## Host Path Formula

```
$DATA_DIR / openclaw-<id> / openclaw-data / .openclaw / skills /
```

| Mode | Concrete Path |
|------|---------------|
| **Dev** | `.workspace/data/openclaw-<id>/openclaw-data/.openclaw/skills/` |
| **Prod** | `~/.local/share/obol/openclaw-<id>/openclaw-data/.openclaw/skills/` |

## Implementation Steps

### 1. Add `skillsVolumePath()` helper

Returns the host-side path to `/data/.openclaw/skills/` inside the PVC.

```go
func skillsVolumePath(cfg *config.Config, id string) string {
    namespace := fmt.Sprintf("%s-%s", appName, id)
    return filepath.Join(cfg.DataDir, namespace, "openclaw-data", ".openclaw", "skills")
}
```

### 2. Add `injectSkillsToVolume()` function

Copies staged skills from config dir directly to the host PVC path.
Called BEFORE helmfile sync so skills are present at first pod boot.

### 3. Rewrite `SkillsSync()` for runtime use

`obol openclaw skills sync --from <dir>` now copies to host path instead of creating ConfigMap.

### 4. Remove old ConfigMap machinery from `doSync()`

- Remove `ensureNamespaceExists()` call (only existed for pre-creating ConfigMap)
- Remove `syncStagedSkills()` call
- Replace with `injectSkillsToVolume()` call

### 5. Disable chart skills feature in overlay

Change overlay from:
```yaml
skills:
  enabled: true
  createDefault: false
```
To:
```yaml
skills:
  enabled: false
```

This removes the init container, ConfigMap volume, and `skills.load.extraDirs` config entirely. OpenClaw uses its native file watcher on `/data/.openclaw/skills/`.

### 6. Update `copyWorkspaceToPod()` to use host path

Same pattern — write directly to `$DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/workspace/` instead of kubectl cp.

## Revised Data Flow

```
Embedded skills (internal/embed/skills/)
    │ stageDefaultSkills()
    ▼
$CONFIG_DIR/applications/openclaw/<id>/skills/        ← staged source
    │ injectSkillsToVolume()
    ▼
$DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/skills/  ← host PVC path
    │ k3d volume mount
    ▼
Container: /data/.openclaw/skills/                    ← native watched dir
    │ OpenClaw file watcher
    ▼
Skills loaded ✓
```

## Revised `doSync()` Flow

**Before**: ensureNamespace → stageSkills → syncStagedSkills(ConfigMap) → helmfile sync → copyWorkspaceToPod(kubectl cp)

**After**: stageSkills → injectSkillsToVolume(host path) → helmfile sync → copyWorkspaceToVolume(host path)

## Files Modified

- `internal/openclaw/openclaw.go` — all changes
- `internal/openclaw/overlay_test.go` — update expected overlay output

## What Gets Deleted

- `syncStagedSkills()` function
- ConfigMap creation logic in `SkillsSync()` (rewritten for host-path)
- `ensureNamespaceExists()` call in `doSync()` (before helmfile sync)
- `skills.enabled: true` / `skills.createDefault: false` from overlay
- tar archiving, kubectl delete/create configmap, rollout restart
