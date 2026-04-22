package serviceoffercontroller

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// --- purchaseConditionIsTrue ------------------------------------------------

func TestPurchaseConditionIsTrue(t *testing.T) {
	conds := []monetizeapi.Condition{
		{Type: "Signed", Status: "True"},
		{Type: "Configured", Status: "False"},
		{Type: "Ready", Status: "Unknown"},
	}
	if !purchaseConditionIsTrue(conds, "Signed") {
		t.Error("Signed should be true")
	}
	if purchaseConditionIsTrue(conds, "Configured") {
		t.Error("Configured is False, must not report true")
	}
	if purchaseConditionIsTrue(conds, "Ready") {
		t.Error("Ready is Unknown, must not report true")
	}
	if purchaseConditionIsTrue(conds, "Missing") {
		t.Error("Missing condition must not report true")
	}
	if purchaseConditionIsTrue(nil, "Any") {
		t.Error("nil conditions must not report true")
	}
}

// --- setPurchaseCondition ---------------------------------------------------

func TestSetPurchaseCondition_AppendsNew(t *testing.T) {
	var conds []monetizeapi.Condition

	setPurchaseCondition(&conds, "Signed", "True", "SignedOK", "ok")

	if len(conds) != 1 {
		t.Fatalf("len(conds) = %d, want 1", len(conds))
	}
	if conds[0].Type != "Signed" || conds[0].Status != "True" {
		t.Errorf("conds[0] = %+v, want Type=Signed Status=True", conds[0])
	}
	if conds[0].LastTransitionTime.IsZero() {
		t.Error("LastTransitionTime must be set on new condition")
	}
}

func TestSetPurchaseCondition_UpdatesExistingNoStatusChange(t *testing.T) {
	conds := []monetizeapi.Condition{
		{Type: "Signed", Status: "True", Reason: "Old", Message: "old msg"},
	}
	// Capture the original timestamp (zero value is fine — we just want it to remain unchanged).
	originalTs := conds[0].LastTransitionTime

	setPurchaseCondition(&conds, "Signed", "True", "NewReason", "new msg")

	if len(conds) != 1 {
		t.Fatalf("condition count changed: %d", len(conds))
	}
	if conds[0].Reason != "NewReason" {
		t.Errorf("Reason = %q, want NewReason", conds[0].Reason)
	}
	if conds[0].Message != "new msg" {
		t.Errorf("Message = %q, want 'new msg'", conds[0].Message)
	}
	// Status unchanged -> LastTransitionTime must NOT be bumped.
	if !conds[0].LastTransitionTime.Equal(&originalTs) {
		t.Errorf("LastTransitionTime bumped when status did not change (before=%v, after=%v)",
			originalTs, conds[0].LastTransitionTime)
	}
}

func TestSetPurchaseCondition_StatusFlipBumpsTimestamp(t *testing.T) {
	conds := []monetizeapi.Condition{
		{Type: "Signed", Status: "False"},
	}

	setPurchaseCondition(&conds, "Signed", "True", "Flipped", "")

	if conds[0].Status != "True" {
		t.Errorf("Status = %q, want True", conds[0].Status)
	}
	if conds[0].LastTransitionTime.IsZero() {
		t.Error("LastTransitionTime must be set when status flips")
	}
}

// --- normalizeRecoverySignature --------------------------------------------

func TestNormalizeRecoverySignature(t *testing.T) {
	// 132 chars = 0x + 130 hex chars = 65 bytes.
	// v byte is the last byte; must be bumped from {0,1} -> {27,28} and left alone
	// if already {27,28} (or any v > 1).
	const baseHex = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	tests := []struct {
		name string
		sig  string
		want string
	}{
		{
			name: "v=0 bumped to 27 (0x1b)",
			sig:  baseHex + "00",
			want: baseHex + "1b",
		},
		{
			name: "v=1 bumped to 28 (0x1c)",
			sig:  baseHex + "01",
			want: baseHex + "1c",
		},
		{
			name: "v=27 unchanged",
			sig:  baseHex + "1b",
			want: baseHex + "1b",
		},
		{
			name: "v=28 unchanged",
			sig:  baseHex + "1c",
			want: baseHex + "1c",
		},
		{
			name: "short signature returned unchanged",
			sig:  "0xdeadbeef",
			want: "0xdeadbeef",
		},
		{
			name: "no 0x prefix returned unchanged",
			sig:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa00",
			want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa00",
		},
		{
			name: "malformed v byte returned unchanged",
			sig:  baseHex + "zz",
			want: baseHex + "zz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeRecoverySignature(tt.sig); got != tt.want {
				t.Errorf("normalizeRecoverySignature last byte = %q, want %q", got[len(got)-2:], tt.want[len(tt.want)-2:])
			}
		})
	}
}

// --- normalizePurchasedUpstreamURL ------------------------------------------

func TestNormalizePurchasedUpstreamURL_EdgeCases(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"https://seller.example", "https://seller.example"},
		{"https://seller.example/", "https://seller.example"},
		{"https://seller.example/services/api", "https://seller.example/services/api"},
		{"https://seller.example/v1/chat/completions", "https://seller.example"},
		{"https://seller.example/chat/completions", "https://seller.example"},
		{"https://seller.example/v1/chat/completions/", "https://seller.example"},
		{"   https://seller.example/v1/chat/completions  ", "https://seller.example"},
		// Only /v1/chat/completions or /chat/completions are stripped — not anywhere in the middle.
		{"https://seller.example/chat/completions/extra", "https://seller.example/chat/completions/extra"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := normalizePurchasedUpstreamURL(tt.in); got != tt.want {
				t.Errorf("normalizePurchasedUpstreamURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// --- preSignedAuthMaps ------------------------------------------------------

func TestPreSignedAuthMaps_Empty(t *testing.T) {
	pr := &monetizeapi.PurchaseRequest{}
	_, err := preSignedAuthMaps(pr)
	if err == nil {
		t.Fatal("expected error for empty auths")
	}
	if !strings.Contains(err.Error(), "no pre-signed auths") {
		t.Errorf("error = %q, want substring 'no pre-signed auths'", err.Error())
	}
}

func TestPreSignedAuthMaps_NormalizesSignature(t *testing.T) {
	const baseHex = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	pr := &monetizeapi.PurchaseRequest{
		Spec: monetizeapi.PurchaseRequestSpec{
			PreSignedAuths: []monetizeapi.PreSignedAuth{
				{
					Signature:   baseHex + "00", // v=0, must be normalized to 1b
					From:        "0x1111",
					To:          "0x2222",
					Value:       "1000",
					ValidAfter:  "1",
					ValidBefore: "2",
					Nonce:       "0xdeadbeef",
				},
				{
					Signature:   baseHex + "1c", // already normalized
					From:        "0x3333",
					To:          "0x4444",
					Value:       "2000",
					ValidAfter:  "3",
					ValidBefore: "4",
					Nonce:       "0xfeedface",
				},
			},
		},
	}

	auths, err := preSignedAuthMaps(pr)
	if err != nil {
		t.Fatalf("preSignedAuthMaps: %v", err)
	}
	if len(auths) != 2 {
		t.Fatalf("len(auths) = %d, want 2", len(auths))
	}
	if got, _ := auths[0]["signature"].(string); !strings.HasSuffix(got, "1b") {
		t.Errorf("auth[0] signature suffix = %q, want normalized to 1b", got[len(got)-2:])
	}
	if got, _ := auths[1]["signature"].(string); !strings.HasSuffix(got, "1c") {
		t.Errorf("auth[1] signature suffix = %q, want 1c unchanged", got[len(got)-2:])
	}
	// Round-trip verification of the other fields.
	if auths[0]["from"] != "0x1111" {
		t.Errorf("auth[0] from = %q", auths[0]["from"])
	}
	if auths[1]["nonce"] != "0xfeedface" {
		t.Errorf("auth[1] nonce = %q", auths[1]["nonce"])
	}
	// Required keys must all be present.
	for i, a := range auths {
		for _, k := range []string{"signature", "from", "to", "value", "validAfter", "validBefore", "nonce"} {
			if _, ok := a[k]; !ok {
				t.Errorf("auth[%d] missing key %q", i, k)
			}
		}
	}
}
