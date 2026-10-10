package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v3"
)

// smokeTestTag is the default validationResponse tag for smoke-test verdicts;
// it matches the erc8004 smoke-test request-hash domain.
const smokeTestTag = "obol/smoke-test/v1"

// smokeBytes32Re matches a 0x-prefixed bytes32 hex string (the sha256 of the
// committed report.md, or an explicit request-hash override).
var smokeBytes32Re = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

// smokeCommand groups the smoke-test agent's operator verbs. v0 carries only
// `calldata`: derive ERC-8004 validationResponse calldata for a finished
// smoke run so the operator can submit it with THEIR OWN wallet — the agent
// and the controller NEVER sign validation transactions (same stance as
// `obol bounty eval calldata`).
func smokeCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "smoke",
		Usage: "Smoke-test agent verbs: derive ERC-8004 verdict calldata for a run",
		Commands: []*cli.Command{
			smokeCalldataCommand(cfg),
		},
	}
}

// smokeCalldataCommand prints ERC-8004 validationResponse calldata for one
// smoke-test run. The request hash is derived as
// keccak256("obol/smoke-test/v1|<targetBaseURL>|<runId>") unless an explicit
// --request-hash override is given (mirrors `obol bounty eval calldata`).
func smokeCalldataCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "calldata",
		Usage: "Print ERC-8004 validationResponse calldata for a smoke run, for YOUR wallet to submit (the agent NEVER signs)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "target", Usage: "[REQUIRED] Smoke target base URL (normalized: trimmed, trailing slashes dropped)", Required: true},
			&cli.StringFlag{Name: "run-id", Usage: "[REQUIRED] Run ID from the smoke report (results.json runId)", Required: true},
			&cli.StringFlag{Name: "request-hash", Usage: "Explicit validation request hash (bytes32, 0x...) — overrides --target/--run-id derivation"},
			&cli.IntFlag{Name: "response", Usage: "[REQUIRED] Verdict score 0-100 (results.json score100; the registry reverts above 100)", Required: true},
			&cli.StringFlag{Name: "response-uri", Usage: "Commit-pinned GitHub permalink of the committed report.md"},
			&cli.StringFlag{Name: "response-hash", Usage: "sha256 of the committed report.md bytes (0x + 64 hex; results.json reportSha256). Optional, zero allowed"},
			&cli.StringFlag{Name: "tag", Usage: "Validation tag", Value: smokeTestTag},
			&cli.StringFlag{Name: "network", Usage: "Chain", Value: "base-sepolia"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			res, err := buildSmokeCalldata(smokeCalldataInput{
				Target:              cmd.String("target"),
				RunID:               cmd.String("run-id"),
				RequestHashOverride: cmd.String("request-hash"),
				Response:            int(cmd.Int("response")),
				ResponseURI:         cmd.String("response-uri"),
				ResponseHash:        cmd.String("response-hash"),
				Tag:                 cmd.String("tag"),
				Network:             cmd.String("network"),
			})
			if err != nil {
				return err
			}
			fmt.Printf("Request hash: %s\n", res.RequestHash.Hex())
			fmt.Printf("ValidationRegistry (%s): %s\n", cmd.String("network"), res.Registry)
			fmt.Printf("Calldata: 0x%x\n", res.Calldata)
			fmt.Println("Submit with YOUR wallet (e.g. the agent remote-signer or cast send) — the smoke agent and the controller NEVER sign validation transactions.")
			return nil
		},
	}
}

// smokeCalldataInput carries the raw flag values for one calldata derivation.
type smokeCalldataInput struct {
	Target              string
	RunID               string
	RequestHashOverride string
	Response            int
	ResponseURI         string
	ResponseHash        string
	Tag                 string
	Network             string
}

// smokeCalldataResult is the derived submit-ready transaction material.
type smokeCalldataResult struct {
	RequestHash common.Hash
	Registry    string
	Calldata    []byte
}

// buildSmokeCalldata validates the inputs and packs validationResponse
// calldata via the shared erc8004 encoder. Kept free of CLI plumbing so the
// golden test can pin the exact bytes.
func buildSmokeCalldata(in smokeCalldataInput) (smokeCalldataResult, error) {
	if in.Response < 0 || in.Response > erc8004.MaxValidationResponse {
		return smokeCalldataResult{}, fmt.Errorf("--response %d out of range 0-%d (the deployed registry reverts above %d; submit results.json score100, not score255)",
			in.Response, erc8004.MaxValidationResponse, erc8004.MaxValidationResponse)
	}

	requestHash := erc8004.SmokeTestRequestHash(in.Target, in.RunID)
	if raw := strings.TrimSpace(in.RequestHashOverride); raw != "" {
		if !smokeBytes32Re.MatchString(raw) {
			return smokeCalldataResult{}, fmt.Errorf("--request-hash %q is not a bytes32 hex string (0x + 64 hex chars)", raw)
		}
		requestHash = common.HexToHash(raw)
	}

	responseHash := common.Hash{}
	if raw := strings.TrimSpace(in.ResponseHash); raw != "" {
		if !smokeBytes32Re.MatchString(raw) {
			return smokeCalldataResult{}, fmt.Errorf("--response-hash %q is not a sha256 hex string (0x + 64 hex chars)", raw)
		}
		responseHash = common.HexToHash(raw)
	}

	registry, err := erc8004.ValidationRegistryAddress(in.Network)
	if err != nil {
		return smokeCalldataResult{}, err
	}

	calldata, err := erc8004.EncodeValidationResponse(
		requestHash,
		uint8(in.Response),
		in.ResponseURI,
		responseHash,
		in.Tag,
	)
	if err != nil {
		return smokeCalldataResult{}, err
	}

	return smokeCalldataResult{RequestHash: requestHash, Registry: registry, Calldata: calldata}, nil
}
