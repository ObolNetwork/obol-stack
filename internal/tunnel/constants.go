package tunnel

const (
	tunnelNamespace     = "traefik"
	tunnelLabelSelector = "app.kubernetes.io/name=cloudflared"

	tunnelServiceURL = "http://traefik.traefik.svc.cluster.local:80"

	// cloudflared-tunnel-token is created by `obol tunnel provision`.
	tunnelTokenSecretName = "cloudflared-tunnel-token"
	tunnelTokenSecretKey  = "TUNNEL_TOKEN"

	// Locally-managed tunnel resources created by `obol tunnel login`.
	localManagedSecretName    = "cloudflared-local-credentials"
	localManagedConfigMapName = "cloudflared-local-config"
)
