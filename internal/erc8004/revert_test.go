package erc8004

import (
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

type fakeDataErr struct {
	msg  string
	data string
}

func (e *fakeDataErr) Error() string  { return e.msg }
func (e *fakeDataErr) ErrorData() any { return e.data }

func TestDecodeRevertReason_ErrorString(t *testing.T) {
	stringT, _ := abi.NewType("string", "", nil)
	args := abi.Arguments{{Type: stringT}}
	payload, err := args.Pack("not authorised")
	if err != nil {
		t.Fatal(err)
	}
	data := "0x08c379a0" + hex.EncodeToString(payload)

	got := decodeRevertReason(&fakeDataErr{msg: "execution reverted", data: data})
	if got != "not authorised" {
		t.Errorf("decoded = %q, want %q", got, "not authorised")
	}
}

func TestDecodeRevertReason_PanicCode(t *testing.T) {
	uintT, _ := abi.NewType("uint256", "", nil)
	args := abi.Arguments{{Type: uintT}}
	payload, err := args.Pack(big.NewInt(0x11)) // arithmetic overflow/underflow
	if err != nil {
		t.Fatal(err)
	}
	data := "0x4e487b71" + hex.EncodeToString(payload)

	got := decodeRevertReason(&fakeDataErr{msg: "execution reverted", data: data})
	if !strings.Contains(got, "arithmetic overflow/underflow") {
		t.Errorf("decoded = %q, want substring %q", got, "arithmetic overflow/underflow")
	}
}

func TestDecodeRevertReason_CustomErrorSelector(t *testing.T) {
	got := decodeRevertReason(&fakeDataErr{msg: "execution reverted", data: "0xcafebabe1234"})
	if got != "custom error 0xcafebabe" {
		t.Errorf("decoded = %q, want %q", got, "custom error 0xcafebabe")
	}
}

func TestDecodeRevertReason_NoData(t *testing.T) {
	cases := []error{
		nil,
		errors.New("plain"),
		&fakeDataErr{msg: "x", data: ""},
		&fakeDataErr{msg: "x", data: "0x"},
		&fakeDataErr{msg: "x", data: "0xnothex"},
	}
	for _, e := range cases {
		if got := decodeRevertReason(e); got != "" {
			t.Errorf("decoded(%v) = %q, want empty", e, got)
		}
	}
}
