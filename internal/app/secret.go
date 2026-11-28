package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// SecretSetOptions are the sources of an `obol app secret set`.
type SecretSetOptions struct {
	// EnvFiles are KEY=VALUE files (kubectl --from-env-file semantics:
	// '#' comments and blank lines skipped, value taken verbatim).
	EnvFiles []string
	// Files are "key=path" or "path" (key = file name).
	Files []string
}

// maxSecretBytes is the Kubernetes Secret size limit.
const maxSecretBytes = 1 << 20

var (
	secretNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	secretKeyRE  = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)
)

// readSecretData reads every source in-process. Errors name the file and
// line, never its content.
func readSecretData(opts SecretSetOptions) (map[string][]byte, error) {
	data := map[string][]byte{}
	total := 0

	add := func(key string, value []byte, source string) error {
		if !secretKeyRE.MatchString(key) {
			return fmt.Errorf("%s: invalid key %q (allowed: letters, digits, '-', '_', '.')", source, key)
		}

		if _, dup := data[key]; dup {
			return fmt.Errorf("%s: key %q given more than once", source, key)
		}

		total += len(value)
		if total > maxSecretBytes {
			return errors.New("secret data exceeds the 1 MiB Kubernetes limit")
		}

		data[key] = value

		return nil
	}

	for _, path := range opts.EnvFiles {
		raw, err := readLimited(path)
		if err != nil {
			return nil, err
		}

		raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))

		for i, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimRight(line, "\r")
			trimmed := strings.TrimLeft(line, " \t")

			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}

			key, value, ok := strings.Cut(trimmed, "=")
			if !ok {
				return nil, fmt.Errorf("%s line %d: expected KEY=VALUE", path, i+1)
			}

			if err := add(key, []byte(value), fmt.Sprintf("%s line %d", path, i+1)); err != nil {
				return nil, err
			}
		}
	}

	for _, spec := range opts.Files {
		key, path, ok := strings.Cut(spec, "=")
		if !ok {
			path = spec
			key = filepath.Base(spec)
		}

		if key == "" || path == "" {
			return nil, fmt.Errorf("invalid --from-file %q: use key=path or path", spec)
		}

		raw, err := readLimited(path)
		if err != nil {
			return nil, err
		}

		if err := add(key, raw, path); err != nil {
			return nil, err
		}
	}

	if len(data) == 0 {
		return nil, errors.New("no secret data: pass --from-env-file <file> and/or --from-file key=path")
	}

	return data, nil
}

func readLimited(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("read %s: not a regular file", path)
	}

	if info.Size() > maxSecretBytes {
		return nil, fmt.Errorf("read %s: larger than the 1 MiB Secret limit", path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	return raw, nil
}

// secretManifest builds the Secret applied on kubectl's stdin.
func secretManifest(name, namespace string, data map[string][]byte) ([]byte, error) {
	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    map[string]string{"obol.org/managed-by": "obol-app"},
		},
		"type": "Opaque",
		"data": data, // []byte marshals to base64
	}

	return json.Marshal(manifest)
}

// SetSecret creates or updates a Secret in an app's namespace from files
// read in-process. Values go to kubectl on stdin only and are never
// printed; the output lists key names.
//
// It server-side applies with field manager obol-app, so keys set by this
// command are replaced, and keys another tool added to the same Secret stay.
func SetSecret(cfg *config.Config, u *ui.UI, identifier, secretName string, opts SecretSetOptions) error {
	if !secretNameRE.MatchString(secretName) || len(secretName) > 253 {
		return fmt.Errorf("invalid Secret name %q: lowercase letters, digits, '-' and '.'", secretName)
	}

	namespace, err := appNamespace(cfg, identifier)
	if err != nil {
		return err
	}

	data, err := readSecretData(opts)
	if err != nil {
		return err
	}

	exists, err := namespaceExists(cfg, namespace)
	if err != nil {
		return fmt.Errorf("check namespace %s: %w", namespace, err)
	}

	if !exists {
		if _, err := kubectl(cfg, nil, "create", "namespace", namespace); err != nil {
			return fmt.Errorf("create namespace %s: %w", namespace, err)
		}

		u.Infof("Created namespace %s", namespace)
	}

	manifest, err := secretManifest(secretName, namespace, data)
	if err != nil {
		return err
	}

	applyArgs := []string{"apply", "--server-side", "--force-conflicts", "--field-manager=obol-app", "-f", "-"}
	if _, err := kubectl(cfg, manifest, applyArgs...); err != nil {
		return fmt.Errorf("apply Secret %s/%s: %w", namespace, secretName, err)
	}

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	u.Successf("Secret %s set in namespace %s", secretName, namespace)
	u.Detail("Keys", strings.Join(keys, ", "))

	return nil
}

// ListSecrets prints the Secrets in an app's namespace: names, types and
// key names, never values. Helm release records are left out.
func ListSecrets(cfg *config.Config, u *ui.UI, identifier string) error {
	namespace, err := appNamespace(cfg, identifier)
	if err != nil {
		return err
	}

	secrets, err := listSecretMeta(cfg, namespace)
	if err != nil {
		return fmt.Errorf("list Secrets in %s: %w", namespace, err)
	}

	type row struct {
		Name        string   `json:"name"`
		Type        string   `json:"type"`
		Keys        []string `json:"keys"`
		KeyMaterial bool     `json:"key_material"`
	}

	rows := []row{}

	for _, s := range secrets {
		if s.Type == "helm.sh/release.v1" {
			continue
		}

		keys := s.Keys
		if keys == nil {
			keys = []string{}
		}

		sort.Strings(keys)
		rows = append(rows, row{Name: s.Name, Type: s.Type, Keys: keys, KeyMaterial: looksLikeKeyMaterial(s.Name, s.Type, s.Keys)})
	}

	if u.IsJSON() {
		return u.JSON(rows)
	}

	if len(rows) == 0 {
		u.Printf("No Secrets in namespace %s", namespace)
		return nil
	}

	u.Printf("Secrets in namespace %s (names and keys only):", namespace)

	for _, r := range rows {
		note := ""
		if r.KeyMaterial {
			note = "  [key material]"
		}

		u.Printf("  %s (%s): %s%s", r.Name, r.Type, strings.Join(r.Keys, ", "), note)
	}

	return nil
}

// appNamespace resolves an installed app's namespace.
func appNamespace(cfg *config.Config, identifier string) (string, error) {
	appName, id, err := parseDeploymentIdentifier(identifier)
	if err != nil {
		return "", err
	}

	dir := deploymentDirFor(cfg, appName, id)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("deployment not found: %s", identifier)
	}

	meta, err := loadAppMeta(dir, appName, id)
	if err != nil {
		return "", err
	}

	return meta.Namespace, nil
}

// ResolveExplicitInstance matches an app argument ("app/id", or "app" when
// only one deployment of that app exists) without the single-app
// shortcut of ResolveInstance, for commands whose later arguments would
// otherwise be mistaken for the app.
func ResolveExplicitInstance(cfg *config.Config, arg string) (string, error) {
	instances, err := ListInstanceIDs(cfg)
	if err != nil {
		return "", err
	}

	var prefix []string

	for _, inst := range instances {
		if inst == arg {
			return inst, nil
		}

		if typ, _, ok := strings.Cut(inst, "/"); ok && typ == arg {
			prefix = append(prefix, inst)
		}
	}

	if len(prefix) == 1 {
		return prefix[0], nil
	}

	if len(instances) == 0 {
		return "", errors.New("no app deployments found — run 'obol app install <chart>' to create one")
	}

	return "", fmt.Errorf("unknown app %q; installed: %s", arg, strings.Join(instances, ", "))
}
