package main

// obol skills — skill-marketplace utilities on top of ERC-8004:
// anchoring a bundle's sha256 on the Identity Registry, rating skills
// via the Reputation Registry (ERC-8239 draft tag convention, obol
// interim form), reading aggregate reputation, and verifying a
// downloaded bundle against the on-chain hash.
//
// Calldata-printer pattern throughout: the CLI prints to+data, the
// OPERATOR (or buyer) submits with their own wallet. obol NEVER signs.
//
// Distinct from `obol openclaw skills`, which manages skill files on an
// OpenClaw instance's PVC.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v3"
)

func skillsCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "skills",
		Usage: "Skill marketplace: anchor bundle hashes, rate skills, read reputation, verify downloads (ERC-8004)",
		Commands: []*cli.Command{
			skillsCalldataCommand(cfg),
			skillsReputationCommand(cfg),
			skillsVerifyCommand(cfg),
		},
	}
}

func skillsCalldataCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "calldata",
		Usage: "Print ERC-8004 calldata for skill operations (submitted with YOUR wallet — obol NEVER signs)",
		Commands: []*cli.Command{
			skillsCalldataSetHashCommand(cfg),
			skillsCalldataFeedbackCommand(cfg),
		},
	}
}

// skillsCalldataSetHashCommand prints IdentityRegistry.setMetadata
// calldata anchoring a skill bundle's sha256 under the key
// "skill.sha256:<name>@<version>".
func skillsCalldataSetHashCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "set-hash",
		Usage:     "Print IdentityRegistry setMetadata calldata anchoring a skill bundle's sha256",
		ArgsUsage: "<name>@<version>",
		Description: `Anchors the bundle hash on the seller's ERC-8004 agent so buyers can
verify a paid download against the chain (obol skills verify).

The hash comes from --hash (printed by ` + "`obol sell skill`" + `) or is
computed from a local bundle with --from-bundle. The metadata value is
stored as the 64-char ASCII lowercase hex string.

Example:
  obol skills calldata set-hash quant-notes@0.1.0 --agent-id 42 --hash <sha256> --chain base`,
		Flags: []cli.Flag{
			&cli.Int64Flag{Name: "agent-id", Usage: "[REQUIRED] Your ERC-8004 agent id (Identity Registry tokenId)", Required: true},
			&cli.StringFlag{Name: "chain", Usage: "Registration chain (base, base-sepolia, ethereum)", Value: "base"},
			&cli.StringFlag{Name: "skill", Usage: "Skill ref <name>@<version> (alternative to the positional argument)"},
			&cli.StringFlag{Name: "hash", Usage: "Bundle sha256 as 64 hex chars (with or without 0x prefix)"},
			&cli.StringFlag{Name: "from-bundle", Aliases: []string{"bundle"}, Usage: "Path to a bundle.tar.gz to hash instead of --hash"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ref, err := skillRefFromCmd(cmd)
			if err != nil {
				return err
			}

			hashArg := strings.TrimSpace(cmd.String("hash"))
			bundlePath := strings.TrimSpace(cmd.String("from-bundle"))
			var hexHash string
			switch {
			case hashArg != "" && bundlePath != "":
				return fmt.Errorf("--hash and --from-bundle are mutually exclusive — pass exactly one")
			case hashArg != "":
				hexHash, err = parseSkillHashArg(hashArg)
				if err != nil {
					return err
				}
			case bundlePath != "":
				hexHash, err = sha256File(bundlePath)
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("hash source required: --hash 0x<sha256> or --from-bundle <bundle.tar.gz>")
			}

			net, err := erc8004.ResolveNetwork(cmd.String("chain"))
			if err != nil {
				return err
			}
			key := erc8004.SkillHashMetadataKey(ref)
			calldata, err := erc8004.EncodeSetMetadata(big.NewInt(cmd.Int64("agent-id")), key, []byte(hexHash))
			if err != nil {
				return err
			}

			fmt.Printf("Skill:          %s\n", ref)
			fmt.Printf("Metadata key:   %s\n", key)
			fmt.Printf("Metadata value: %s (ASCII hex sha256)\n", hexHash)
			fmt.Printf("IdentityRegistry (%s): %s\n", net.Name, net.RegistryAddress)
			fmt.Printf("Calldata: 0x%x\n", calldata)
			fmt.Println("Submit with YOUR wallet (the agent owner; e.g. the agent remote-signer or cast send) — the controller NEVER signs.")
			fmt.Println("Note: re-submitting an unchanged value reverts on-chain (the registry rejects no-op writes).")
			return nil
		},
	}
}

// skillsCalldataFeedbackCommand prints ReputationRegistry.giveFeedback
// calldata rating one skill of one agent, tagged with the ERC-8239
// draft convention (tag1 "asr:skill", obol interim tag2).
func skillsCalldataFeedbackCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "feedback",
		Usage:     "Print ReputationRegistry giveFeedback calldata rating a skill (buyer-submitted)",
		ArgsUsage: "<name>@<version>",
		Description: `Rates one skill of one seller agent with a 0-100 score. The rating is
tagged tag1="asr:skill" and tag2 in the documented obol interim form of
the ERC-8239 draft, so per-skill reputation aggregates cleanly.

Example:
  obol skills calldata feedback quant-notes@0.1.0 --agent-id 42 --value 95 --chain base`,
		Flags: []cli.Flag{
			&cli.Int64Flag{Name: "agent-id", Usage: "[REQUIRED] The SELLER's ERC-8004 agent id (Identity Registry tokenId)", Required: true},
			&cli.IntFlag{Name: "value", Usage: "[REQUIRED] Score 0-100", Required: true},
			&cli.StringFlag{Name: "chain", Usage: "Chain hosting the registries (base, base-sepolia, ethereum)", Value: "base"},
			&cli.StringFlag{Name: "skill", Usage: "Skill ref <name>@<version> (alternative to the positional argument)"},
			&cli.StringFlag{Name: "endpoint", Usage: "Optional endpoint the rating refers to (e.g. the offer URL)"},
			&cli.StringFlag{Name: "feedback-uri", Aliases: []string{"uri"}, Usage: "Optional URI of an off-chain document backing the rating"},
			&cli.StringFlag{Name: "feedback-hash", Aliases: []string{"hash"}, Usage: "Optional 32-byte hash (0x...) of the feedback document"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ref, err := skillRefFromCmd(cmd)
			if err != nil {
				return err
			}
			value := cmd.Int("value")
			if value < 0 || value > 100 {
				return fmt.Errorf("--value must be 0-100, got %d", value)
			}

			net, err := erc8004.ResolveNetwork(cmd.String("chain"))
			if err != nil {
				return err
			}
			registry, err := erc8004.ReputationRegistryAddress(cmd.String("chain"))
			if err != nil {
				return err
			}
			agentID := big.NewInt(cmd.Int64("agent-id"))
			tag2, err := erc8004.SkillTag2(net, agentID, ref)
			if err != nil {
				return err
			}

			fbHash := common.Hash{}
			if h := strings.TrimSpace(cmd.String("feedback-hash")); h != "" {
				raw, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(h), "0x"))
				if err != nil || len(raw) != 32 {
					return fmt.Errorf("--feedback-hash must be 32 bytes of hex (0x + 64 chars), got %q", h)
				}
				fbHash = common.BytesToHash(raw)
			}

			calldata, err := erc8004.EncodeGiveFeedback(
				agentID,
				big.NewInt(int64(value)),
				0, // score is already 0-100, no fixed-point scaling
				erc8004.SkillTag1,
				tag2,
				strings.TrimSpace(cmd.String("endpoint")),
				strings.TrimSpace(cmd.String("feedback-uri")),
				fbHash,
			)
			if err != nil {
				return err
			}

			fmt.Printf("Feedback: skill %s on agent %s, score %d/100\n", ref, agentID, value)
			fmt.Printf("tag1: %s\n", erc8004.SkillTag1)
			fmt.Printf("tag2: %s\n", tag2)
			fmt.Printf("ReputationRegistry (%s): %s\n", net.Name, registry)
			fmt.Printf("Calldata: 0x%x\n", calldata)
			fmt.Println("Submit with YOUR wallet (the buyer's) — self-feedback from the agent owner reverts on-chain; the controller NEVER signs.")
			return nil
		},
	}
}

// skillsReputationCommand reads the aggregate per-skill rating via
// getSummary, filtered to the skill's tag pair.
func skillsReputationCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "reputation",
		Usage:     "Read a skill's aggregate on-chain rating (ReputationRegistry getSummary)",
		ArgsUsage: "<name>@<version>",
		Flags: []cli.Flag{
			&cli.Int64Flag{Name: "agent-id", Usage: "[REQUIRED] The seller's ERC-8004 agent id", Required: true},
			&cli.StringFlag{Name: "chain", Usage: "Chain hosting the registries (base, base-sepolia, ethereum)", Value: "base"},
			&cli.StringFlag{Name: "skill", Usage: "Skill ref <name>@<version> (alternative to the positional argument)"},
			&cli.StringSliceFlag{Name: "raters", Usage: "Optional whitelist of rater addresses (0x..., repeatable); empty = all raters"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			ref, err := skillRefFromCmd(cmd)
			if err != nil {
				return err
			}
			net, err := erc8004.ResolveNetwork(cmd.String("chain"))
			if err != nil {
				return err
			}
			registry, err := erc8004.ReputationRegistryAddress(cmd.String("chain"))
			if err != nil {
				return err
			}
			raters, err := parseRaterAddresses(cmd.StringSlice("raters"))
			if err != nil {
				return err
			}
			agentID := big.NewInt(cmd.Int64("agent-id"))
			tag2, err := erc8004.SkillTag2(net, agentID, ref)
			if err != nil {
				return err
			}

			// Read-only eRPC-backed client; no signer anywhere near this path.
			client, err := erc8004.NewClientForNetwork(ctx, stack.LocalIngressURL(cfg)+"/rpc", net)
			if err != nil {
				return fmt.Errorf("connect to %s via eRPC: %w", net.Name, err)
			}
			defer client.Close()

			reader, err := erc8004.NewReputationReader(client.ETH(), registry)
			if err != nil {
				return err
			}
			summary, err := reader.Summary(ctx, agentID, raters, erc8004.SkillTag1, tag2)
			if err != nil {
				return err
			}

			score := skillScoreString(summary.SummaryValue, summary.SummaryValueDecimals)
			if u.IsJSON() {
				return u.JSON(struct {
					AgentID  int64  `json:"agentId"`
					Skill    string `json:"skill"`
					Network  string `json:"network"`
					Registry string `json:"registry"`
					Tag1     string `json:"tag1"`
					Tag2     string `json:"tag2"`
					Count    uint64 `json:"count"`
					Score    string `json:"score"`
				}{
					AgentID:  cmd.Int64("agent-id"),
					Skill:    ref,
					Network:  net.Name,
					Registry: registry,
					Tag1:     erc8004.SkillTag1,
					Tag2:     tag2,
					Count:    summary.Count,
					Score:    score,
				})
			}

			u.Printf("Skill:    %s (agent %s on %s)", ref, agentID, net.Name)
			u.Printf("tag2:     %s", tag2)
			u.Printf("Ratings:  %d", summary.Count)
			u.Printf("Score:    %s / 100", score)
			if len(raters) > 0 {
				u.Printf("Raters:   %d whitelisted", len(raters))
			}
			return nil
		},
	}
}

// skillsVerifyCommand checks a downloaded bundle against the seller's
// on-chain hash anchor. Exit code is non-zero on mismatch or when no
// anchor exists, so scripts can gate installs on it.
func skillsVerifyCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "verify",
		Usage:     "Verify a downloaded skill bundle against the seller's on-chain sha256 anchor",
		ArgsUsage: "<bundle.tar.gz>",
		Flags: []cli.Flag{
			&cli.Int64Flag{Name: "agent-id", Usage: "[REQUIRED] The seller's ERC-8004 agent id", Required: true},
			&cli.StringFlag{Name: "skill", Usage: "[REQUIRED] Skill ref <name>@<version>", Required: true},
			&cli.StringFlag{Name: "chain", Usage: "Chain hosting the Identity Registry (base, base-sepolia, ethereum)", Value: "base"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() != 1 {
				return fmt.Errorf("bundle path required: obol skills verify <bundle.tar.gz> --agent-id N --skill <name>@<version>")
			}
			bundlePath := cmd.Args().First()

			_, _, err := erc8004.ParseSkillRef(cmd.String("skill"))
			if err != nil {
				return err
			}
			ref := strings.TrimSpace(cmd.String("skill"))

			localHash, err := sha256File(bundlePath)
			if err != nil {
				return err
			}

			net, err := erc8004.ResolveNetwork(cmd.String("chain"))
			if err != nil {
				return err
			}
			client, err := erc8004.NewClientForNetwork(ctx, stack.LocalIngressURL(cfg)+"/rpc", net)
			if err != nil {
				return fmt.Errorf("connect to %s via eRPC: %w", net.Name, err)
			}
			defer client.Close()

			key := erc8004.SkillHashMetadataKey(ref)
			agentID := big.NewInt(cmd.Int64("agent-id"))
			onChain, err := client.GetMetadata(ctx, agentID, key)
			if err != nil {
				return fmt.Errorf("read on-chain metadata %q for agent %s on %s: %w", key, agentID, net.Name, err)
			}
			if len(onChain) == 0 {
				return fmt.Errorf("FAIL: no on-chain hash anchored for %s (agent %s, key %q, %s) — ask the seller to run `obol skills calldata set-hash`",
					ref, agentID, key, net.Name)
			}

			if !skillHashMatches(onChain, localHash) {
				u.Errorf("MISMATCH — do not trust this bundle")
				u.Printf("  local sha256:    %s", localHash)
				u.Printf("  on-chain anchor: %s", strings.TrimSpace(string(onChain)))
				return fmt.Errorf("bundle %s does not match the on-chain hash for %s (agent %s, %s)", bundlePath, ref, agentID, net.Name)
			}

			u.Successf("OK — bundle matches the on-chain anchor")
			u.Printf("  skill:  %s (agent %s on %s)", ref, agentID, net.Name)
			u.Printf("  sha256: %s", localHash)
			return nil
		},
	}
}

// ── pure helpers (unit-tested without a live chain) ─────────────────────────

// skillRefFromCmd resolves the <name>@<version> ref from the positional
// argument or --skill and validates it.
func skillRefFromCmd(cmd *cli.Command) (string, error) {
	ref := strings.TrimSpace(cmd.Args().First())
	if ref == "" {
		ref = strings.TrimSpace(cmd.String("skill"))
	}
	if ref == "" {
		return "", fmt.Errorf("skill ref required: pass <name>@<version> as the argument or via --skill")
	}
	if _, _, err := erc8004.ParseSkillRef(ref); err != nil {
		return "", err
	}
	return ref, nil
}

var skillHashRe = regexp.MustCompile(`^[a-f0-9]{64}$`)

// parseSkillHashArg normalizes an operator-supplied sha256: trims, drops
// an optional 0x prefix, lowercases, and validates 64 hex chars.
func parseSkillHashArg(s string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(s))
	h = strings.TrimPrefix(h, "0x")
	if !skillHashRe.MatchString(h) {
		return "", fmt.Errorf("invalid sha256 %q: want 64 hex chars (optionally 0x-prefixed)", s)
	}
	return h, nil
}

// sha256File hashes a file's bytes to lowercase hex.
func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// parseRaterAddresses validates and converts --raters values.
func parseRaterAddresses(raw []string) ([]common.Address, error) {
	var out []common.Address
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !common.IsHexAddress(r) {
			return nil, fmt.Errorf("invalid rater address %q", r)
		}
		out = append(out, common.HexToAddress(r))
	}
	return out, nil
}

// skillHashMatches compares the on-chain metadata value (ASCII hex,
// possibly 0x-prefixed or differently cased) against the local
// lowercase hex hash.
func skillHashMatches(onChain []byte, localHex string) bool {
	chain := strings.ToLower(strings.TrimSpace(string(onChain)))
	chain = strings.TrimPrefix(chain, "0x")
	return chain == strings.ToLower(strings.TrimSpace(localHex))
}

// skillScoreString renders getSummary's fixed-point aggregate
// (summaryValue × 10^-decimals) as a decimal string.
func skillScoreString(value *big.Int, decimals uint8) string {
	if value == nil {
		return "0"
	}
	if decimals == 0 {
		return value.String()
	}
	f := new(big.Float).SetInt(value)
	scale := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	f.Quo(f, scale)
	return f.Text('f', int(decimals))
}
