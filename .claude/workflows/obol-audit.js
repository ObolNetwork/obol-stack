// obol-audit — hostile-operator bug sweep (layer 4 of docs/testing/proactive-bug-finding.md).
//
// Fan out N operator-lens finders over the current tree, adversarially verify
// each candidate with two independent refuters, return the ranked survivors.
// Re-run after any large change. Pass args to tell it what is already fixed so
// it does not re-report known issues:
//   Workflow({ name: "obol-audit", args: { repo: "/abs/path", known: ["short description of a fixed bug", ...] } })
//
// Defaults: repo = the obol-stack checkout, known = [] (report everything).

export const meta = {
  name: 'obol-audit',
  description: 'Hostile-operator audit: lens finders + adversarial refuters → ranked confirmed bugs',
  phases: [
    { title: 'Hunt', detail: 'one finder per operator lens' },
    { title: 'Verify', detail: 'two adversarial refuters per candidate' },
  ],
}

const REPO = (args && args.repo) || '/Users/bussyjd/Development/OBOL-WORKBENCH/obol-stack'
const KNOWN = (args && args.known) || []

const knownBlock = KNOWN.length
  ? `KNOWN ISSUES — re-reporting any of these is a FAILURE, exclude them:\n${KNOWN.map((k, i) => `(${i + 1}) ${k}`).join('\n')}`
  : 'No known-issue exclusions supplied — report every real bug you find.'

const COMMON = `You are a hostile-but-realistic node operator auditing the obol-stack Go repo at ${REPO}. READ-ONLY: no file edits, no commits, no kubectl, no deploys. You may grep, read, run go test, and write throwaway /tmp programs to confirm behavior.

${knownBlock}

Find NEW, REAL bugs of your assigned lens — behavior a reasonable operator hits that produces wrong results, broken routes, lost/misleading state, leaked internal data, free rides, or misleading output. For each: trace the ACTUAL code path end to end (function + file:line — no speculation), give the concrete operator scenario that triggers it, and confirm it is not a known issue and not already guarded by a test. Quality over quantity: top findings only (max 3), ranked by confidence x severity. Zero findings is fine — do NOT pad with style nits or untriggerable theoretical races.`

// The invariants these lenses probe map to docs/testing/proactive-bug-finding.md.
const LENSES = [
  { key: 'state-staleness', prompt: 'Lens: SPEC-CHANGE STALENESS (invariant 1). When an operator mutates a spec field (hostname, payTo, price, network, type, upstream) or deletes+recreates an offer, which derived status / published document / on-chain artifact fails to reset or re-derive? Read the reconcile paths and ask of each derived artifact: what resets this when its input changes?' },
  { key: 'name-injectivity', prompt: 'Lens: GENERATED NAME COLLISIONS (invariant 2). Enumerate every generated resource name (middlewares, routes, services, configmaps, grants, tunnel/hermes resources) and check each is injective over (namespace, name, purpose), including truncation collisions and delimiter ambiguity (can two distinct (ns,name) pairs join to the same string?).' },
  { key: 'status-truth', prompt: 'Lens: STATUS LIES (invariant 3). Find conditions/statuses set on apply-success rather than observed reality, or stuck after the cause resolves. Check every readiness signal: PaymentConfigured, Registered, agent/purchase readiness, wallet address, tunnel status, sell status output, dashboard sources.' },
  { key: 'public-surface-leak', prompt: 'Lens: INTERNAL DATA IN PUBLIC SURFACES (invariant 4). Enumerate every publicly-served document (openapi.json, .well-known/x402, agent-registration.json, skill.md, catalog, storefront, 402 body, chat page, async job-status/result endpoints) and trace each field back to its source: does anything pull internal CR fields, cluster-internal hostnames/IPs, raw errors, secrets, or model endpoints without a public-intent gate?' },
  { key: 'url-construction', prompt: 'Lens: URL/HOST/PATH CONSTRUCTION (invariant 5). Audit every URL build for double slashes, missing/hardcoded scheme, port handling, trailing-slash sensitivity, host-header trust, path traversal via offer names, http fallbacks for non-local hosts.' },
  { key: 'payment-verification', prompt: 'Lens: PAYMENT CORRECTNESS (invariant 6). Audit the x402 settle/verify flow for money-losing or free-ride bugs: request reaching upstream without settle; replay of a payment across offers/paths/methods/prices with the same tuple; concurrent duplicate submissions; amount/asset/network confusion; nonce single-use scope; price-change-in-flight; a cheap-route payment passing on an expensive route.' },
  { key: 'tx-sequencing', prompt: 'Lens: ON-CHAIN CLIENT CORRECTNESS. Audit the erc8004 client/signer and CLI registration flow: missing receipt.Status checks (tx sent but reverted → success assumed), gas estimation on stale state, chain-id confusion, recovery paths trusting ambiguous historical matches without re-verifying current ownership, partial multi-network failure leaving inconsistent CRD state.' },
  { key: 'cli-validation', prompt: 'Lens: CLI INPUT TRAPS. Audit cmd/obol for flags whose help mismatches behavior, silent normalization that surprises (TrimRight/ToLower on case-sensitive addresses/hostnames), values passed unvalidated into k8s names / YAML / URLs / on-chain amounts (injection, panics, zero/negative prices), and defaults that differ between sibling subcommands.' },
]

const FINDINGS_SCHEMA = {
  type: 'object',
  required: ['findings'],
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        required: ['title', 'file', 'lines', 'severity', 'scenario', 'codePath', 'whyNotKnown'],
        properties: {
          title: { type: 'string' },
          file: { type: 'string' },
          lines: { type: 'string' },
          severity: { type: 'string', enum: ['critical', 'high', 'medium', 'low'] },
          scenario: { type: 'string' },
          codePath: { type: 'string' },
          whyNotKnown: { type: 'string' },
        },
      },
    },
  },
}

const VERDICT_SCHEMA = {
  type: 'object',
  required: ['refuted', 'notes'],
  properties: {
    refuted: { type: 'boolean' },
    notes: { type: 'string' },
  },
}

phase('Hunt')
const found = await parallel(LENSES.map((l) => () =>
  agent(`${COMMON}\n\n${l.prompt}`, { label: `hunt:${l.key}`, phase: 'Hunt', model: 'sonnet', schema: FINDINGS_SCHEMA })
))
const candidates = found.filter(Boolean).flatMap((r, i) => r.findings.map((f) => ({ ...f, lens: LENSES[i].key })))
log(`${candidates.length} candidate findings across ${LENSES.length} lenses`)

phase('Verify')
const verified = await parallel(candidates.map((c) => () =>
  parallel([0, 1].map((n) => () =>
    agent(`${COMMON}\n\nYou are adversarial refuter #${n + 1}. A finder claims this bug. Try HARD to refute it by reading the actual code: is the cited path real, is the scenario operator-triggerable, is it already guarded/fixed/known, does the claimed consequence follow? Default to refuted=true if uncertain or if it is a style/theoretical issue.\n\nFinding (lens ${c.lens}):\n${JSON.stringify(c, null, 2)}`,
      { label: `refute:${c.lens}`, phase: 'Verify', model: 'sonnet', schema: VERDICT_SCHEMA })
  )).then((votes) => {
    const live = votes.filter(Boolean)
    return { ...c, survives: live.length >= 1 && live.every((v) => !v.refuted), refuterNotes: live.map((v) => v.notes) }
  })
))

const sev = { critical: 0, high: 1, medium: 2, low: 3 }
const confirmed = verified.filter(Boolean).filter((f) => f.survives).sort((a, b) => (sev[a.severity] ?? 9) - (sev[b.severity] ?? 9))
const rejected = verified.filter(Boolean).filter((f) => !f.survives).map((f) => ({ title: f.title, lens: f.lens }))
log(`${confirmed.length} confirmed, ${rejected.length} rejected`)
return { confirmed, rejected }
