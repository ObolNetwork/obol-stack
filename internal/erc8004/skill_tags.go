// Skill marketplace ↔ ERC-8004 tag + metadata-key convention.
//
// Skill ratings ride the Reputation Registry's giveFeedback tag pair
// using the ERC-8239 draft "Agent Skill Rating" convention:
//
//	tag1 = "asr:skill"
//	tag2 = "eip155:<chainId>:<identityRegistryAddr>:<agentId>:<name>@<version>"
//
// This file implements the obol interim form of the ERC-8239 draft
// (ethereum/EIPs PR #1704) tag2: the registry address is lowercased hex
// for determinism (giveFeedback tags are exact-match strings on-chain,
// so a mixed-case address would silently fork the rating namespace) and
// the agentId is rendered in decimal. The skill ref is "<name>@<version>".
//
// Bundle integrity is anchored on the Identity Registry via setMetadata
// with key "skill.sha256:<name>@<version>" and the 64-char ASCII
// lowercase hex sha256 of the gzipped bundle bytes as the value —
// ASCII hex rather than raw bytes so block explorers render it legibly
// and GetMetadata comparison is a bytes.Equal on the hex string.
//
// Signing model is identical to the rest of this package: the CLI only
// builds calldata; the operator/buyer submits with their OWN wallet.
// The controller NEVER signs.

package erc8004

import (
	"fmt"
	"math/big"
	"strings"
)

// SkillTag1 is the fixed tag1 for skill-rating feedback entries
// (ERC-8239 draft "asr" = agent skill rating).
const SkillTag1 = "asr:skill"

// skillHashKeyPrefix prefixes the Identity Registry setMetadata key
// that anchors a skill bundle's sha256.
const skillHashKeyPrefix = "skill.sha256:"

// SkillRef builds the canonical "<name>@<version>" skill reference.
// Both parts must be non-empty and free of ':' (tag2 is colon-
// delimited) and '@' (the ref separator).
func SkillRef(name, version string) (string, error) {
	if err := checkSkillRefPart("skill name", name); err != nil {
		return "", err
	}
	if err := checkSkillRefPart("skill version", version); err != nil {
		return "", err
	}
	return name + "@" + version, nil
}

// ParseSkillRef splits a "<name>@<version>" reference and re-validates
// both parts. Use it to normalize operator-supplied refs before they
// reach a tag or metadata key.
func ParseSkillRef(ref string) (name, version string, err error) {
	name, version, ok := strings.Cut(strings.TrimSpace(ref), "@")
	if !ok {
		return "", "", fmt.Errorf("erc8004: skill ref %q must be <name>@<version> (e.g. buy-x402@0.1.0)", ref)
	}
	if _, err := SkillRef(name, version); err != nil {
		return "", "", err
	}
	return name, version, nil
}

func checkSkillRefPart(what, v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("erc8004: %s must not be empty", what)
	}
	if strings.ContainsAny(v, ":@") {
		return fmt.Errorf("erc8004: %s %q must not contain ':' or '@'", what, v)
	}
	return nil
}

// SkillTag2 builds the ERC-8239-style tag2 binding a rating to one
// skill of one agent on one registry deployment:
//
//	eip155:<chainId>:<lowercase identityRegistryAddr>:<agentId decimal>:<skillRef>
//
// skillRef must be a valid "<name>@<version>" reference (see SkillRef).
func SkillTag2(net NetworkConfig, agentID *big.Int, skillRef string) (string, error) {
	if err := checkAgentID(agentID); err != nil {
		return "", err
	}
	if _, _, err := ParseSkillRef(skillRef); err != nil {
		return "", err
	}
	return fmt.Sprintf("eip155:%d:%s:%s:%s",
		net.ChainID,
		strings.ToLower(net.RegistryAddress),
		agentID.String(),
		skillRef,
	), nil
}

// SkillHashMetadataKey returns the Identity Registry setMetadata key
// under which a skill bundle's sha256 is anchored:
// "skill.sha256:<name>@<version>". The metadata VALUE is the 64-char
// ASCII lowercase hex sha256 of the gzipped bundle bytes, stored as
// []byte(hex).
func SkillHashMetadataKey(skillRef string) string {
	return skillHashKeyPrefix + skillRef
}
