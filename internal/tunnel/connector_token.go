package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// connectorTokenClaims is the decoded payload of a Cloudflare Tunnel connector
// token (the value the dashboard hands you for `cloudflared tunnel run --token`).
// It is base64-encoded JSON of the form {"a":<accountTag>,"t":<tunnelID>,"s":<secret>}.
type connectorTokenClaims struct {
	AccountTag string `json:"a"`
	TunnelID   string `json:"t"`
	Secret     string `json:"s"`
}

var uuidRegexp = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// parseConnectorToken decodes and validates a Cloudflare Tunnel connector token.
// It returns the embedded account tag and tunnel ID so callers can populate
// tunnel state without any Cloudflare API call. The token's secret never needs
// to be inspected — it is opaque to us and handed straight to cloudflared.
func parseConnectorToken(token string) (claims connectorTokenClaims, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return claims, errors.New("connector token is empty")
	}

	decoded, decErr := decodeBase64Flexible(token)
	if decErr != nil {
		return claims, errors.New("connector token is not valid base64 (copy the token from Cloudflare dashboard → Networks → Tunnels)")
	}

	if jsonErr := json.Unmarshal(decoded, &claims); jsonErr != nil {
		return claims, errors.New("connector token payload is not the expected JSON shape")
	}

	if strings.TrimSpace(claims.AccountTag) == "" || strings.TrimSpace(claims.TunnelID) == "" || strings.TrimSpace(claims.Secret) == "" {
		return claims, errors.New("connector token is missing required fields (account, tunnel id, or secret)")
	}
	if !uuidRegexp.MatchString(strings.ToLower(strings.TrimSpace(claims.TunnelID))) {
		return claims, errors.New("connector token tunnel id is not a valid UUID")
	}

	return claims, nil
}

// extractConnectorToken pulls a connector token out of a pasted value that may
// be the bare token or a full dashboard command such as
// `cloudflared tunnel run --token eyJ…` or `cloudflared service install eyJ…`.
// It returns "" when no plausible token is found.
func extractConnectorToken(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	// Whole value already parses — bare token.
	if _, err := parseConnectorToken(input); err == nil {
		return input
	}

	// `--token=eyJ…` form.
	if m := regexp.MustCompile(`--token[=\s]+(\S+)`).FindStringSubmatch(input); len(m) == 2 {
		if _, err := parseConnectorToken(m[1]); err == nil {
			return m[1]
		}
	}

	// Otherwise scan whitespace-delimited fields for the last one that parses
	// as a connector token (handles `cloudflared service install <token>`).
	fields := strings.Fields(input)
	for i := len(fields) - 1; i >= 0; i-- {
		if _, err := parseConnectorToken(fields[i]); err == nil {
			return fields[i]
		}
	}

	return ""
}

// decodeBase64Flexible decodes a base64 string that may be standard or URL-safe
// and may or may not carry padding.
func decodeBase64Flexible(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		if decoded, err := enc.DecodeString(s); err == nil {
			return decoded, nil
		} else {
			lastErr = err
		}
	}
	return nil, lastErr
}
