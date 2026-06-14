// Identity Registry calldata builders (calldata-printer pattern).
//
// The transact path for setMetadata exists on Client
// (SetMetadataWithOpts), but agent operators frequently hold the
// registration key in a wallet the CLI never sees. These encoders build
// the raw to+data pair so the CLI can print it and the OPERATOR submits
// with their own wallet — the controller NEVER signs.

package erc8004

import (
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

var (
	identityABIOnce   sync.Once
	identityABIParsed abi.ABI
	identityABIErr    error
)

// identityABI lazily parses the embedded Identity Registry ABI once.
// Client.newClient keeps its own parse (it predates this helper and
// owns a bound contract); encoders share this copy.
func identityABI() (abi.ABI, error) {
	identityABIOnce.Do(func() {
		identityABIParsed, identityABIErr = abi.JSON(strings.NewReader(identityRegistryABI))
	})
	if identityABIErr != nil {
		return abi.ABI{}, fmt.Errorf("erc8004: parse identity registry abi: %w", identityABIErr)
	}
	return identityABIParsed, nil
}

// EncodeSetMetadata builds calldata for
// setMetadata(uint256 agentId, string metadataKey, bytes metadataValue)
// on the ERC-8004 Identity Registry. Must be submitted by the agent
// owner's wallet. Note the registry's reference implementation reverts
// when the new value equals the stored value (see SetMetadataWithOpts),
// so re-submitting an unchanged hash fails on-chain as a no-op guard.
func EncodeSetMetadata(agentID *big.Int, key string, value []byte) ([]byte, error) {
	if err := checkAgentID(agentID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("erc8004: metadata key must not be empty")
	}

	parsed, err := identityABI()
	if err != nil {
		return nil, err
	}
	data, err := parsed.Pack("setMetadata", agentID, key, value)
	if err != nil {
		return nil, fmt.Errorf("erc8004: pack setMetadata: %w", err)
	}
	return data, nil
}

// SetMetadataCall is the decoded argument set of a setMetadata call.
type SetMetadataCall struct {
	AgentID *big.Int
	Key     string
	Value   []byte
}

// DecodeSetMetadataCalldata decodes setMetadata calldata (selector +
// ABI-encoded args). Useful for provenance checks on observed
// transactions and for tests.
func DecodeSetMetadataCalldata(data []byte) (SetMetadataCall, error) {
	parsed, err := identityABI()
	if err != nil {
		return SetMetadataCall{}, err
	}
	values, err := unpackCalldata(parsed, "setMetadata", data)
	if err != nil {
		return SetMetadataCall{}, err
	}
	if len(values) != 3 {
		return SetMetadataCall{}, fmt.Errorf("erc8004: setMetadata arg count = %d, want 3", len(values))
	}

	out := SetMetadataCall{}
	var ok bool
	if out.AgentID, ok = values[0].(*big.Int); !ok {
		return SetMetadataCall{}, fmt.Errorf("erc8004: agentId type = %T", values[0])
	}
	if out.Key, ok = values[1].(string); !ok {
		return SetMetadataCall{}, fmt.Errorf("erc8004: metadataKey type = %T", values[1])
	}
	if out.Value, ok = values[2].([]byte); !ok {
		return SetMetadataCall{}, fmt.Errorf("erc8004: metadataValue type = %T", values[2])
	}
	return out, nil
}
