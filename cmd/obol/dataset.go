package main

// obol dataset — owner side of a versioned, membership-gated dataset offer.
//
//	obol dataset from <bundle-dir> --name <id>   ingest a bundle as a new
//	                                             signed version (creates v1).
//	obol dataset version <id> --bundle <dir>     append the next signed version.
//	obol dataset publish <id>                    host the artifact server on
//	                                             this machine + a Cloudflare
//	                                             tunnel; gate every byte.
//	obol dataset approve <user-code>             admit a worker (membership).
//	obol dataset verify <id>                     walk the signed version chain.
//	obol dataset status <id>                     versions + members.
//
// The artifact server is the host gateway (same spirit as `obol sell
// inference` / `obol research publish`): it runs on the owner's machine, never
// in the cluster, and reaches remote buyers over the real internet via
// Cloudflare. Bytes never leave the host un-gated.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/dataset"
	x402 "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/urfave/cli/v3"
	x402types "github.com/x402-foundation/x402/go/types"
)

// datasetState lets approve/status reach a running publish server.
type datasetState struct {
	ID         string `json:"id"`
	LocalAddr  string `json:"local_addr"`
	PublicURL  string `json:"public_url"`
	OwnerToken string `json:"owner_token"`
}

func datasetCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "dataset",
		Usage: "Publish and sell versioned, membership-gated datasets",
		Commands: []*cli.Command{
			datasetFromCommand(cfg),
			datasetVersionCommand(cfg),
			datasetPublishCommand(cfg),
			datasetApproveCommand(cfg),
			datasetVerifyCommand(cfg),
			datasetStatusCommand(cfg),
		},
	}
}

func datasetFromCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "from",
		Usage:     "Ingest a dataset bundle directory as a new signed version",
		ArgsUsage: "<bundle-dir>",
		Flags:     []cli.Flag{&cli.StringFlag{Name: "name", Usage: "Dataset id", Required: true}},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return fmt.Errorf("bundle directory required: obol dataset from <bundle-dir> --name <id>")
			}
			return appendDatasetVersion(cfg, cmd, strings.TrimSpace(cmd.String("name")), cmd.Args().First())
		},
	}
}

func datasetVersionCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "version",
		Usage:     "Append the next signed version of an existing dataset",
		ArgsUsage: "<id>",
		Flags:     []cli.Flag{&cli.StringFlag{Name: "bundle", Usage: "New bundle directory", Required: true}},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return fmt.Errorf("dataset id required: obol dataset version <id> --bundle <dir>")
			}
			id := strings.TrimSpace(cmd.Args().First())
			if _, err := os.Stat(datasetStorePath(cfg, id)); err != nil {
				return fmt.Errorf("dataset %q not found — create it with 'obol dataset from'", id)
			}
			return appendDatasetVersion(cfg, cmd, id, cmd.String("bundle"))
		},
	}
}

func appendDatasetVersion(cfg *config.Config, cmd *cli.Command, id, bundleDir string) error {
	u := getUI(cmd)
	if id == "" {
		return fmt.Errorf("dataset id required")
	}
	key, err := dataset.LoadOrCreateKey(datasetKeyPath(cfg, id))
	if err != nil {
		return err
	}
	signer := dataset.NewEthSigner(key)

	manifestHash, artifactPath, fileHash, size, err := dataset.ReadBundle(bundleDir)
	if err != nil {
		return err
	}

	store := dataset.NewStore(datasetStorePath(cfg, id))
	st, err := store.Load()
	if err != nil {
		return err
	}
	log := dataset.LogFromVersions(st.Versions)
	// Tamper-evidence is only real if the producer refuses to extend a chain it
	// cannot verify: a rewritten earlier entry must not be silently signed over.
	if len(st.Versions) > 0 {
		if err := log.Verify(dataset.EthVerifier{}, signer.SignerID()); err != nil {
			return fmt.Errorf("refusing to extend dataset %q: existing version log fails verification: %w", id, err)
		}
	}
	v, err := log.Append(manifestHash, fileHash, size, signer, time.Now())
	if err != nil {
		return err
	}

	st.ID, st.GroupID, st.Versions = id, id, log.Versions()
	if st.Artifacts == nil {
		st.Artifacts = map[int]string{}
	}
	st.Artifacts[v.Seq] = artifactPath
	if err := store.Save(st); err != nil {
		return err
	}

	u.Successf("Dataset %q version %d recorded", id, v.Seq)
	u.Infof("Manifest hash: %s", v.ManifestHash)
	u.Infof("File hash:     %s", v.FileHash)
	u.Infof("Size:          %d bytes", v.Size)
	u.Infof("Owner:         %s", signer.SignerID())
	u.Dim("Publish it with:  obol dataset publish " + id)
	return nil
}

func datasetPublishCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "publish",
		Usage:     "Host the dataset's artifact server and expose it over a Cloudflare tunnel",
		ArgsUsage: "<id>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "membership", Usage: "open | invite", Value: "invite"},
			&cli.IntFlag{Name: "port", Usage: "Local port (0 = pick a free one)", Value: 0},
			&cli.BoolFlag{Name: "no-tunnel", Usage: "Serve locally only"},
			&cli.StringFlag{Name: "price", Usage: "Per-join price in USDC (enables x402 paid join; empty = invite/open only)"},
			&cli.StringFlag{Name: "pay-to", Usage: "USDC recipient (default: the dataset owner address)"},
			&cli.StringFlag{Name: "chain", Usage: "Payment chain", Value: "base-sepolia"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() != 1 {
				return fmt.Errorf("dataset id required: obol dataset publish <id>")
			}
			id := strings.TrimSpace(cmd.Args().First())

			key, err := dataset.LoadOrCreateKey(datasetKeyPath(cfg, id))
			if err != nil {
				return err
			}
			signer := dataset.NewEthSigner(key)

			store := dataset.NewStore(datasetStorePath(cfg, id))
			st, err := store.Load()
			if err != nil {
				return err
			}
			if len(st.Versions) == 0 {
				return fmt.Errorf("dataset %q has no versions — run 'obol dataset from' first", id)
			}
			// Never serve a chain we cannot verify against the owner key: a
			// tampered persisted store must fail closed, not be published.
			pubLog := dataset.LogFromVersions(st.Versions)
			if err := pubLog.Verify(dataset.EthVerifier{}, signer.SignerID()); err != nil {
				return fmt.Errorf("refusing to serve dataset %q: version log fails verification: %w", id, err)
			}
			artifacts := dataset.NewFileArtifacts()
			for seq, path := range st.Artifacts {
				artifacts.Set(seq, path)
			}
			ents := dataset.NewEntitlements()
			ents.Load(st.Entitlements)

			ownerToken, err := randomToken("obol-dataset-owner-")
			if err != nil {
				return err
			}

			// Optional x402 paid join: when --price is set, gate /join/paid with
			// a real facilitator-verified, in-process-settled payment for the
			// join price (the owner address is the default payee). Without it,
			// the dataset is invite/open-membership only — never free-on-payment.
			var paidJoin func(http.Handler) http.Handler
			var joinAtomic string
			if price := strings.TrimSpace(cmd.String("price")); price != "" {
				chain, cerr := x402.ResolveChainInfo(cmd.String("chain"))
				if cerr != nil {
					return fmt.Errorf("paid join: %w", cerr)
				}
				payTo := strings.TrimSpace(cmd.String("pay-to"))
				if payTo == "" {
					payTo = signer.SignerID()
				}
				req := x402.BuildV2Requirement(chain, price, payTo, 0)
				joinAtomic = req.Amount
				paidJoin = x402.NewForwardAuthMiddleware(x402.ForwardAuthConfig{
					FacilitatorURL:   x402.DefaultFacilitatorURL,
					VerifyOnly:       false,
					SettlesInProcess: true,
				}, []x402types.PaymentRequirements{req})
			}

			srv := dataset.NewServer(dataset.Config{
				ID:              id,
				Membership:      cmd.String("membership"),
				OwnerToken:      ownerToken,
				OwnerSigner:     signer.SignerID(),
				Log:             pubLog,
				Ents:            ents,
				Store:           store,
				Artifacts:       artifacts,
				PaidJoin:        paidJoin,
				JoinPriceAtomic: joinAtomic,
			})

			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cmd.Int("port")))
			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}
			localAddr := "http://" + ln.Addr().String()
			httpSrv := &http.Server{Handler: srv.Handler()}
			go func() { _ = httpSrv.Serve(ln) }()

			publicURL := localAddr
			var tunnel *exec.Cmd
			if !cmd.Bool("no-tunnel") {
				u.Info("Opening Cloudflare tunnel …")
				if turl, tcmd, terr := startQuickTunnel(ctx, ln.Addr().String()); terr != nil {
					u.Warnf("tunnel failed (%v) — serving locally only at %s", terr, localAddr)
				} else {
					publicURL, tunnel = turl, tcmd
				}
			}

			head, _ := pubLog.Head()
			_ = writeDatasetState(cfg, datasetState{ID: id, LocalAddr: localAddr, PublicURL: publicURL, OwnerToken: ownerToken})

			u.Successf("Dataset %q published (head version %d)", id, head.Seq)
			u.Infof("Public URL:  %s", publicURL)
			u.Infof("Owner:       %s", signer.SignerID())
			u.Infof("Membership:  %s", cmd.String("membership"))
			u.Blank()
			u.Bold("Buyers fetch with:")
			u.Printf("  obol buy dataset %s --id %s --member-token <token> --owner %s", publicURL, id, signer.SignerID())
			if cmd.String("membership") == dataset.MembershipInvite {
				u.Dim("Admit a worker's printed code:  obol dataset approve <user-code>")
			}
			u.Dim("Ctrl-C to stop.")

			sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()
			<-sigCtx.Done()

			u.Info("Stopping …")
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(shutCtx)
			if tunnel != nil && tunnel.Process != nil {
				_ = tunnel.Process.Kill()
			}
			_ = removeDatasetState(cfg, id)
			return nil
		},
	}
}

func datasetApproveCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "approve",
		Usage:     "Admit a worker to a dataset (the membership decision)",
		ArgsUsage: "<user-code>",
		Flags:     []cli.Flag{&cli.StringFlag{Name: "dataset", Usage: "Dataset id (defaults to the only running one)"}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() != 1 {
				return fmt.Errorf("user code required: obol dataset approve <user-code>")
			}
			st, err := loadDatasetState(cfg, cmd.String("dataset"))
			if err != nil {
				return err
			}
			body, _ := json.Marshal(map[string]string{"user_code": cmd.Args().First()})
			resp, err := datasetOwnerReq(ctx, st, http.MethodPost, "/auth/device/approve", body)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				b, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("approve failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
			}
			u.Successf("Approved %s into %q", cmd.Args().First(), st.ID)
			return nil
		},
	}
}

func datasetVerifyCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "verify",
		Usage:     "Walk a dataset's signed version chain (offline)",
		ArgsUsage: "<id>",
		Action: func(_ context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() != 1 {
				return fmt.Errorf("dataset id required: obol dataset verify <id>")
			}
			id := strings.TrimSpace(cmd.Args().First())
			key, err := dataset.LoadOrCreateKey(datasetKeyPath(cfg, id))
			if err != nil {
				return err
			}
			owner := dataset.NewEthSigner(key).SignerID()
			st, err := dataset.NewStore(datasetStorePath(cfg, id)).Load()
			if err != nil {
				return err
			}
			log := dataset.LogFromVersions(st.Versions)
			if err := log.Verify(dataset.EthVerifier{}, owner); err != nil {
				return fmt.Errorf("chain INVALID: %w", err)
			}
			head, _ := log.Head()
			u.Successf("Chain valid: %d version(s), head v%d, owner %s", log.Len(), head.Seq, owner)
			return nil
		},
	}
}

func datasetStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "Show versions and member count",
		ArgsUsage: "<id>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() != 1 {
				return fmt.Errorf("dataset id required: obol dataset status <id>")
			}
			id := strings.TrimSpace(cmd.Args().First())
			st, err := dataset.NewStore(datasetStorePath(cfg, id)).Load()
			if err != nil {
				return err
			}
			log := dataset.LogFromVersions(st.Versions)
			u.Bold(fmt.Sprintf("Dataset %s — %d version(s)", id, log.Len()))
			for _, v := range log.Versions() {
				u.Printf("  v%d  %s  %d bytes  (%s…)", v.Seq, v.ManifestHash[:12], v.Size, v.Signature[:12])
			}
			u.Infof("Entitled members: %d", len(st.Entitlements))
			return nil
		},
	}
}

// buyDatasetCommand is added to `obol buy` so buyers fetch with
// `obol buy dataset <url> --id <id> --member-token <tok>`.
func buyDatasetCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "dataset",
		Usage:     "Download a versioned dataset over HTTP, verifying its whole-file hash",
		ArgsUsage: "<seller-url>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "id", Usage: "Dataset id (or embed /dataset/<id> in the URL)"},
			&cli.IntFlag{Name: "version", Usage: "Version to fetch (0 = head)"},
			&cli.StringFlag{Name: "member-token", Usage: "Member token (owner-issued or payment-minted)", Required: true},
			&cli.StringFlag{Name: "out", Usage: "Output file (default <id>-v<N>.jsonl)"},
			&cli.StringFlag{Name: "owner", Usage: "Expected owner 0x address that must have signed the version log (pins identity; recommended)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() != 1 {
				return fmt.Errorf("seller URL required: obol buy dataset <url> --id <id> --member-token <tok>")
			}
			base, id := splitDatasetURL(cmd.Args().First(), cmd.String("id"))
			if id == "" {
				return fmt.Errorf("dataset id required (pass --id or a /dataset/<id> URL)")
			}
			out := cmd.String("out")
			if out == "" {
				v := cmd.Int("version")
				if v == 0 {
					v = 1
				}
				out = fmt.Sprintf("%s-v%d.jsonl", id, v)
			}
			u.Infof("Fetching %s (version %v) → %s", id, orHead(cmd.Int("version")), out)
			res, err := dataset.Fetch(ctx, dataset.FetchOptions{
				BaseURL: base, ID: id, Version: cmd.Int("version"),
				Token: cmd.String("member-token"), OutPath: out,
				ExpectedOwner: strings.TrimSpace(cmd.String("owner")),
			})
			if err != nil {
				return err
			}
			if res.Resumed {
				u.Dim("(resumed an interrupted download)")
			}
			u.Successf("Verified v%d: %d bytes, file hash %s", res.Version, res.Bytes, res.FileHash)
			u.Dim("Manifest: " + res.ManifestHash)
			return nil
		},
	}
}

// --- state + url helpers ---

func datasetServeDir(cfg *config.Config) string { return filepath.Join(cfg.ConfigDir, "dataset-serve") }

func datasetKeyPath(cfg *config.Config, id string) string {
	return filepath.Join(datasetServeDir(cfg), id+".key")
}

func datasetStorePath(cfg *config.Config, id string) string {
	return filepath.Join(datasetServeDir(cfg), id+".store.json")
}

func writeDatasetState(cfg *config.Config, st datasetState) error {
	if err := os.MkdirAll(datasetServeDir(cfg), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(filepath.Join(datasetServeDir(cfg), st.ID+".state.json"), b, 0o600)
}

func removeDatasetState(cfg *config.Config, id string) error {
	return os.Remove(filepath.Join(datasetServeDir(cfg), id+".state.json"))
}

func loadDatasetState(cfg *config.Config, id string) (datasetState, error) {
	dir := datasetServeDir(cfg)
	if id != "" {
		return readDatasetStateFile(filepath.Join(dir, id+".state.json"))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return datasetState{}, fmt.Errorf("no running dataset (publish one first)")
	}
	var found []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".state.json") {
			found = append(found, e.Name())
		}
	}
	switch len(found) {
	case 0:
		return datasetState{}, fmt.Errorf("no running dataset (publish one first)")
	case 1:
		return readDatasetStateFile(filepath.Join(dir, found[0]))
	default:
		return datasetState{}, fmt.Errorf("multiple datasets running — pass --dataset <id>")
	}
}

func readDatasetStateFile(path string) (datasetState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return datasetState{}, fmt.Errorf("read dataset state: %w", err)
	}
	var st datasetState
	if err := json.Unmarshal(b, &st); err != nil {
		return datasetState{}, err
	}
	return st, nil
}

func datasetOwnerReq(ctx context.Context, st datasetState, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = strings.NewReader(string(body))
	}
	req, _ := http.NewRequestWithContext(ctx, method, st.LocalAddr+path, r)
	req.Header.Set("Authorization", "Bearer "+st.OwnerToken)
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

// splitDatasetURL separates a base URL from an embedded /dataset/<id> path.
func splitDatasetURL(raw, flagID string) (base, id string) {
	raw = strings.TrimRight(raw, "/")
	if i := strings.Index(raw, "/dataset/"); i >= 0 {
		base = raw[:i]
		rest := strings.TrimPrefix(raw[i:], "/dataset/")
		id = strings.SplitN(rest, "/", 2)[0]
		if flagID != "" {
			id = flagID
		}
		return base, id
	}
	return raw, flagID
}

func orHead(v int) any {
	if v == 0 {
		return "head"
	}
	return v
}
