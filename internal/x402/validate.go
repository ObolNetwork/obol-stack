package x402

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// ValidateWallet checks that addr is a valid 0x-prefixed 20-byte hex Ethereum address.
func ValidateWallet(addr string) error {
	if !strings.HasPrefix(addr, "0x") && !strings.HasPrefix(addr, "0X") {
		return fmt.Errorf("invalid wallet address %q: must be 0x-prefixed 40-char hex", addr)
	}

	if !common.IsHexAddress(addr) {
		return fmt.Errorf("invalid wallet address %q: must be 0x-prefixed 40-char hex", addr)
	}

	return nil
}
