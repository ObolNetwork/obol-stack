<!--
Release description template based on:
https://github.com/ObolNetwork/obol-stack/releases/tag/v0.9.0

Use this to rewrite the draft body created by .github/workflows/release.yml
before publishing. Replace every bracketed placeholder, preserve the generated
changelog sections at the bottom, and delete empty optional sections.
-->

[![Obol banner](https://obol.org/obolnetwork.png)](https://obol.org/obolnetwork.png)

# [version] - [release theme]

> Optional short quote, definition, or one-line context for the release theme.

[One or two paragraphs describing the release in user terms. Name the headline
capability, the user journey it unlocks, and the important networks, runtimes,
or payment rails involved.]

[Primary call to action. Example: "Get started with `obol [command]`." Include
one public social/demo prompt only when the release is demo-worthy.]

<p align="center">
<img width="[width]" height="[height]" alt="[short screenshot description]" src="[github-user-attachment-url]" />
</p>

> [!NOTE]
> Include faucet, testnet, migration, or compatibility notes that reduce first-run friction.

> [!WARNING]
> This software is early alpha, you could lose what you put in. Please use caution when it comes to non-testnet assets.

## Install / Upgrade

```bash
# Install this release
OBOL_RELEASE=[version] bash <(curl -s https://stack.obol.org)

# Run the stack
obol stack init && obol stack up

# Try the headline workflow
obol [headline command]

# Optional agent/operator hand-off
[copy-paste setup or prompt for the release's intended operator path]
```

---

## Release Highlights

### [headline feature] - [user outcome]

[Explain the primary feature as a concrete workflow. Prefer exact commands,
network names, payment tokens, and observable success states over internal
implementation detail.]

```bash
[canonical command]
```

[One paragraph explaining why this is the canonical starting point and what users
can build from it next.]

### [payment, network, or settlement change]

[Describe the money path carefully. Specify buyer and seller responsibilities,
gas requirements, approval/permit behavior, settlement behavior, and supported
networks.]

### [runtime, agent, or developer-experience change]

[Describe the runtime or operator experience. Include the smallest useful command
set and call out alternate runtimes or compatibility expectations when relevant.]

```bash
[command 1]
[command 2]
[command 3]
```

### [ecosystem, plugin, or integration change]

[Describe integrations users can install or connect to. Link to source
repositories or docs.]

---

## Smaller wins

- **[short feature name]** - [one-sentence user impact].
- **[short feature name]** - [one-sentence user impact].
- **[short feature name]** - [one-sentence user impact].

## Breaking changes / Migration notes

- [Delete this section if there are no breaking changes.]
- **Pre-release tester warning**: If you ran an unreleased marketplace or
  chart-consolidation branch before this release, `obol stack up` may fail
  with Helm `invalid ownership metadata` errors for resources that moved into
  the `base` chart. This is not a supported production migration path. Back up
  anything you need from the local test stack, then recreate it:

  ```bash
  obol stack down
  obol stack purge --force
  obol stack init
  obol stack up
  ```

## Known issues

- [Delete this section if there are no known release-impacting issues.]

## What's Changed

<!-- Keep or regenerate GitHub's generated PR list here. Curate the highlight
sections above by hand; do not make users infer the release story from this list. -->

* [generated PR entry]

## New Contributors

<!-- Keep GitHub's generated new-contributor section when present. Delete when empty. -->

* [generated contributor entry]

**Full Changelog**: https://github.com/ObolNetwork/obol-stack/compare/[previous-tag]...[version]
