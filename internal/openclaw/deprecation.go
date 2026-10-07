package openclaw

// RemovalVersion is the release that removes the OpenClaw runtime
// (deprecated in v0.15).
const RemovalVersion = "v0.16"

// DeprecationNotice is the user-facing OpenClaw deprecation + migration
// text, printed by `obol openclaw ...`, `--runtime openclaw` and by
// `obol stack up` when openclaw-* namespaces exist. The migration restores
// the OpenClaw wallet backup into Hermes (same walletbackup wire format;
// pinned by hermes.TestRestoreWalletCmd_MigratesOpenClawBackup).
var DeprecationNotice = []string{
	"OpenClaw is deprecated and will be removed in " + RemovalVersion + "; Hermes is the default runtime.",
	"Migrate its wallet to Hermes: obol agent wallet backup --runtime openclaw <id> --file openclaw-wallet.json",
	"  then: obol agent wallet restore --runtime hermes --input openclaw-wallet.json --force",
	"  (--force replaces the Hermes agent's wallet; back it up first: obol agent wallet backup --runtime hermes)",
}
