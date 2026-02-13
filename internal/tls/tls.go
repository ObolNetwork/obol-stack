package tls

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// certFile is the TLS certificate filename.
	certFile = "obol-stack.pem"
	// keyFile is the TLS private key filename.
	keyFile = "obol-stack-key.pem"
	// tlsDir is the subdirectory under configDir for TLS files.
	tlsDir = "tls"
	// k8sSecretName is the Kubernetes TLS Secret name.
	k8sSecretName = "obol-stack-tls"
	// k8sNamespace is the namespace for the TLS Secret.
	k8sNamespace = "traefik"
)

// CertDir returns the TLS directory path.
func CertDir(configDir string) string {
	return filepath.Join(configDir, tlsDir)
}

// CertPath returns the path to the TLS certificate.
func CertPath(configDir string) string {
	return filepath.Join(configDir, tlsDir, certFile)
}

// KeyPath returns the path to the TLS private key.
func KeyPath(configDir string) string {
	return filepath.Join(configDir, tlsDir, keyFile)
}

// CertsExist checks if both cert and key files exist.
func CertsExist(configDir string) bool {
	_, certErr := os.Stat(CertPath(configDir))
	_, keyErr := os.Stat(KeyPath(configDir))
	return certErr == nil && keyErr == nil
}

// MkcertAvailable checks if the mkcert binary exists in binDir or PATH.
func MkcertAvailable(binDir string) bool {
	// Check binDir first
	if _, err := os.Stat(filepath.Join(binDir, "mkcert")); err == nil {
		return true
	}
	// Fall back to PATH
	_, err := exec.LookPath("mkcert")
	return err == nil
}

// mkcertPath returns the path to the mkcert binary, preferring binDir.
func mkcertPath(binDir string) string {
	p := filepath.Join(binDir, "mkcert")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if path, err := exec.LookPath("mkcert"); err == nil {
		return path
	}
	return "mkcert"
}

// mkcertEnv returns the current environment with JAVA_HOME cleared.
// mkcert checks all trust stores including Java keytool, which can fail
// if the Java keystore is missing or corrupted. Since we only need browser
// trust (OS keychain), we skip the Java trust store entirely.
func mkcertEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "JAVA_HOME=") {
			env = append(env, e)
		}
	}
	return env
}

// GenerateCerts generates a wildcard TLS certificate for *.obol.stack using mkcert.
// It installs the mkcert CA into the system trust store and creates a certificate
// covering wildcard subdomains, the bare domain, localhost, and loopback addresses.
func GenerateCerts(binDir, configDir string) error {
	mkcert := mkcertPath(binDir)
	env := mkcertEnv()

	// Create TLS directory
	dir := CertDir(configDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create TLS directory: %w", err)
	}

	// Install the local CA into the system trust store.
	// On macOS this adds to the login keychain; on Linux it updates ca-certificates.
	installCmd := exec.Command(mkcert, "-install")
	installCmd.Env = env
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("mkcert -install failed: %w", err)
	}

	// Generate wildcard certificate.
	// SANs: *.obol.stack (wildcard subdomains), obol.stack (bare domain),
	// localhost + loopback (fallback).
	certPath := CertPath(configDir)
	keyPath := KeyPath(configDir)
	genCmd := exec.Command(mkcert,
		"-cert-file", certPath,
		"-key-file", keyPath,
		"*.obol.stack",
		"obol.stack",
		"localhost",
		"127.0.0.1",
		"::1",
	)
	genCmd.Env = env
	genCmd.Stdout = os.Stdout
	genCmd.Stderr = os.Stderr
	if err := genCmd.Run(); err != nil {
		return fmt.Errorf("mkcert cert generation failed: %w", err)
	}

	return nil
}

// EnsureK8sSecret creates or updates the TLS Secret in the traefik namespace.
// Uses kubectl with --dry-run=client piped to apply for idempotent creation.
func EnsureK8sSecret(binDir, configDir, kubeconfigPath string) error {
	kubectl := filepath.Join(binDir, "kubectl")
	certPath := CertPath(configDir)
	keyPath := KeyPath(configDir)

	// Verify cert files exist
	if !CertsExist(configDir) {
		return fmt.Errorf("TLS certificate files not found at %s", CertDir(configDir))
	}

	// kubectl create secret tls obol-stack-tls \
	//   --cert=<cert> --key=<key> -n traefik \
	//   --dry-run=client -o yaml | kubectl apply -f -
	createCmd := exec.Command(kubectl,
		"create", "secret", "tls", k8sSecretName,
		"--cert="+certPath,
		"--key="+keyPath,
		"-n", k8sNamespace,
		"--dry-run=client",
		"-o", "yaml",
	)
	createCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	applyCmd := exec.Command(kubectl,
		"apply", "-f", "-",
	)
	applyCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	applyCmd.Stderr = os.Stderr

	// Pipe create output to apply stdin
	pipe, err := createCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	applyCmd.Stdin = pipe

	if err := createCmd.Start(); err != nil {
		return fmt.Errorf("kubectl create secret failed to start: %w", err)
	}
	if err := applyCmd.Start(); err != nil {
		return fmt.Errorf("kubectl apply failed to start: %w", err)
	}

	if err := createCmd.Wait(); err != nil {
		return fmt.Errorf("kubectl create secret failed: %w", err)
	}
	if err := applyCmd.Wait(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}

	return nil
}
