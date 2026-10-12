package tunnel

import (
	"errors"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

type SetupOptions struct {
	Hostname string

	// ConnectorToken is a Cloudflare Tunnel connector token from the dashboard
	// (Networks → Tunnels). Accepts the bare token or the full
	// `cloudflared tunnel run --token …` line — the prefix is stripped.
	ConnectorToken string

	TransportProtocol string
}

type SetupResult struct {
	Hostname          string `json:"hostname"`
	URL               string `json:"url"`
	Mode              string `json:"mode"`
	ManagementMode    string `json:"management_mode"`
	TransportProtocol string `json:"transport_protocol,omitempty"`
}

// Setup is the single guided command for creating a permanent public URL. By
// wires a dashboard-managed Cloudflare Tunnel from a connector token (least
// privilege, no host binary).
func Setup(cfg *config.Config, u *ui.UI, opts SetupOptions) (*SetupResult, error) {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		if !u.IsTTY() || u.IsJSON() {
			return nil, errors.New("--hostname is required (e.g. stack.example.com)")
		}
		input, err := u.Input("Public hostname (e.g. stack.example.com)", "")
		if err != nil {
			return nil, err
		}
		hostname = normalizeHostname(input)
	}
	if hostname == "" {
		return nil, errors.New("--hostname is required (e.g. stack.example.com)")
	}

	connectorToken, err := resolveConnectorToken(u, hostname, opts.ConnectorToken)
	if err != nil {
		return nil, err
	}

	if err := ProvisionWithToken(cfg, u, TokenProvisionOptions{
		Hostname:          hostname,
		ConnectorToken:    connectorToken,
		TransportProtocol: opts.TransportProtocol,
	}); err != nil {
		return nil, err
	}

	return &SetupResult{
		Hostname:          hostname,
		URL:               "https://" + hostname,
		Mode:              tunnelExposurePersistent,
		ManagementMode:    tunnelManagementRemote,
		TransportProtocol: normalizeTunnelTransportProtocol(opts.TransportProtocol),
	}, nil
}

// resolveConnectorToken returns a validated connector token from the supplied
// value or, in an interactive session, by walking the user through the
// Cloudflare dashboard steps and prompting for it.
func resolveConnectorToken(u *ui.UI, hostname, supplied string) (string, error) {
	if token := extractConnectorToken(supplied); token != "" {
		if _, err := parseConnectorToken(token); err != nil {
			return "", err
		}
		return token, nil
	}

	if !u.IsTTY() || u.IsJSON() {
		return "", errors.New("a Cloudflare connector token is required: pass it as 'obol tunnel setup <token>' or via --token")
	}

	printConnectorSetupSteps(u, hostname)
	input, err := u.Input("Paste the connector token (or the whole 'cloudflared tunnel run --token …' line)", "")
	if err != nil {
		return "", err
	}
	token := extractConnectorToken(input)
	if token == "" {
		return "", errors.New("no connector token found in the pasted value")
	}
	if _, err := parseConnectorToken(token); err != nil {
		return "", err
	}
	return token, nil
}

// printConnectorSetupSteps prints the Cloudflare dashboard walkthrough, including
// the exact in-cluster Service URI the user must route the public hostname to.
func printConnectorSetupSteps(u *ui.UI, hostname string) {
	u.Blank()
	u.Bold("Create a permanent tunnel in the Cloudflare dashboard")
	u.Print("A tunnel exposes your stack to the public internet so buyers can discover and pay")
	u.Print("for the services you sell. You only need this once you're ready to sell.")
	u.Blank()
	u.Print("  1. Open https://one.dash.cloudflare.com → Networks → Tunnels → Create a tunnel")
	u.Print("  2. Choose 'Cloudflared', name the tunnel, and save.")
	u.Print("  3. On the install screen, copy the token (the long eyJ… value).")
	u.Dim("     You do NOT run the command it shows — Obol runs the connector for you.")
	u.Print("  4. Open the 'Public Hostname' tab → Add a public hostname:")
	u.Detail("       Subdomain / Domain", hostname)
	u.Detail("       Type", "HTTP")
	u.Detail("       Service URL", "http://traefik.traefik.svc.cluster.local:80")
	u.Print("  5. Save the public hostname.")
	u.Blank()
}
