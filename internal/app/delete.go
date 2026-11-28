package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// DeleteOptions controls Delete.
type DeleteOptions struct {
	// Force skips the confirmation prompt (an alias of Yes for deletes that
	// keep data). It never implies DeleteData and never confirms it.
	Force bool
	// DeleteData deletes the namespace even when it holds PVCs or Secrets
	// that look like key material (dv-pod key shares, ENR keys).
	DeleteData bool
	// KeepData removes only the helm release, keeping the namespace, its
	// PVCs and Secrets, and the app's config directory.
	KeepData bool
	// Yes skips the confirmation prompt, and also confirms DeleteData
	// without typing the app name. Scripts pass --yes for every delete.
	Yes bool
}

// namespaceData is what a namespace holds that must not be lost by accident.
type namespaceData struct {
	PVCs    []string
	Secrets []string // secrets that look like key material
	// Unknown is set when the check itself failed; treated as "has data".
	Unknown string
}

func (d namespaceData) present() bool {
	return len(d.PVCs) > 0 || len(d.Secrets) > 0 || d.Unknown != ""
}

// keyMaterialTokens mark a Secret (by name or data key) as key material:
// dv-pod's <release>-enr-key / -enr-provided-key and the legacy
// charon-enr-private-key, validator keystores, signer keys.
var keyMaterialTokens = []string{
	"enr", "private-key", "privatekey", "private_key", "keystore", "keystores",
	"validator", "signer", "key-share", "keyshare", "mnemonic", "seed", "wallet",
}

// ignoredSecretTypes are Secret types that never hold app key material.
var ignoredSecretTypes = map[string]bool{
	"helm.sh/release.v1":                  true,
	"kubernetes.io/service-account-token": true,
	"kubernetes.io/dockerconfigjson":      true,
	"kubernetes.io/dockercfg":             true,
}

// looksLikeKeyMaterial reports whether a Secret's name or data keys look
// like key material.
func looksLikeKeyMaterial(name, secretType string, keys []string) bool {
	if ignoredSecretTypes[secretType] {
		return false
	}

	candidates := append([]string{name}, keys...)
	for _, c := range candidates {
		c = strings.ToLower(c)

		for _, tok := range keyMaterialTokens {
			if tok == "enr" || tok == "seed" {
				// Whole-token match only ("enr" inside "henry" doesn't count).
				parts := strings.FieldsFunc(c, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
				if slices.Contains(parts, tok) {
					return true
				}

				continue
			}

			if strings.Contains(c, tok) {
				return true
			}
		}
	}

	return false
}

// secretsTemplate lists Secret names, types and data KEYS. Values never
// leave kubectl.
const secretsTemplate = `{{range .items}}{{.metadata.name}}{{"\t"}}{{.type}}{{"\t"}}{{range $k, $v := .data}}{{$k}},{{end}}{{"\n"}}{{end}}`

// secretMeta is a Secret's name, type and data keys (no values).
type secretMeta struct {
	Name string
	Type string
	Keys []string
}

// listSecretMeta lists Secrets in a namespace without reading values.
func listSecretMeta(cfg *config.Config, namespace string) ([]secretMeta, error) {
	out, err := kubectl(cfg, nil, "get", "secrets", "-n", namespace, "-o", "go-template="+secretsTemplate)
	if err != nil {
		return nil, err
	}

	var secrets []secretMeta

	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.SplitN(line, "\t", 3)
		s := secretMeta{Name: parts[0]}

		if len(parts) > 1 {
			s.Type = parts[1]
		}

		if len(parts) > 2 {
			for k := range strings.SplitSeq(parts[2], ",") {
				if k != "" {
					s.Keys = append(s.Keys, k)
				}
			}
		}

		secrets = append(secrets, s)
	}

	return secrets, nil
}

// inspectNamespaceData checks a namespace for PVCs and key-material
// Secrets (read-only). A failed check is recorded as Unknown, never as
// "no data".
func inspectNamespaceData(cfg *config.Config, namespace string) namespaceData {
	var data namespaceData

	out, err := kubectl(cfg, nil, "get", "pvc", "-n", namespace, "-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	if err != nil {
		data.Unknown = "could not list PVCs: " + err.Error()
	} else {
		for name := range strings.FieldsSeq(string(out)) {
			data.PVCs = append(data.PVCs, name)
		}
	}

	secrets, err := listSecretMeta(cfg, namespace)
	if err != nil {
		if data.Unknown == "" {
			data.Unknown = "could not list Secrets: " + err.Error()
		}
	} else {
		for _, s := range secrets {
			if looksLikeKeyMaterial(s.Name, s.Type, s.Keys) {
				data.Secrets = append(data.Secrets, s.Name)
			}
		}
	}

	return data
}

// namespaceExists checks a namespace. Only a NotFound answer means "no";
// any other failure is returned so callers fail closed.
func namespaceExists(cfg *config.Config, namespace string) (bool, error) {
	_, err := kubectl(cfg, nil, "get", "namespace", namespace, "-o", "name")
	if err == nil {
		return true, nil
	}

	if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "not found") {
		return false, nil
	}

	return false, err
}

// Delete removes an application deployment.
//
//   - Namespace without data: deletes the namespace and the config
//     directory (confirm, or Force).
//   - Namespace with PVCs or key-material Secrets (or KeepData): removes
//     only the helm release; the namespace, PVCs, Secrets and config
//     directory stay, so 'obol app sync' redeploys onto the same data.
//   - DeleteData: deletes everything, after typing the app identifier on a
//     TTY, or with Yes.
func Delete(cfg *config.Config, u *ui.UI, deploymentIdentifier string, opts DeleteOptions) error {
	if opts.DeleteData && opts.KeepData {
		return errors.New("--delete-data and --keep-data are mutually exclusive")
	}

	appName, id, err := parseDeploymentIdentifier(deploymentIdentifier)
	if err != nil {
		return err
	}

	deploymentDir := deploymentDirFor(cfg, appName, id)

	configExists := false
	if _, err := os.Stat(deploymentDir); err == nil {
		configExists = true
	}

	meta := &AppMeta{Namespace: appName + "-" + id}

	if configExists {
		if m, err := loadAppMeta(deploymentDir, appName, id); err == nil {
			meta = m
		}
	}

	namespace := meta.Namespace

	u.Infof("Deleting application: %s/%s", appName, id)
	u.Detail("Namespace", namespace)
	u.Detail("Config directory", deploymentDir)

	nsExists := false
	data := namespaceData{}

	if clusterConfigured(cfg) {
		exists, err := namespaceExists(cfg, namespace)

		switch {
		case err != nil && !opts.DeleteData:
			return fmt.Errorf("cannot check namespace %s for key material (%v)\n"+
				"Start the stack and retry, or pass --delete-data to delete the local config directory anyway", namespace, err)
		case err != nil:
			u.Warnf("Cannot reach the cluster (%v); only the local config directory will be deleted", err)
		default:
			nsExists = exists
		}

		if nsExists {
			data = inspectNamespaceData(cfg, namespace)
		}
	}

	if !nsExists && !configExists {
		return fmt.Errorf("deployment not found: %s", deploymentIdentifier)
	}

	if opts.KeepData && !nsExists {
		u.Printf("Namespace %s not found: no helm release to remove; the config directory is kept.", namespace)
		return nil
	}

	// A namespace shared with other helm releases (e.g. an adopted
	// release in a hand-made namespace) is never deleted as a whole.
	var others []string

	if nsExists {
		releases, err := releasesIn(cfg, namespace)
		if err != nil {
			data.Unknown = firstNonEmpty(data.Unknown, "could not list helm releases: "+err.Error())
		}

		for _, r := range releases {
			if r != meta.Release {
				others = append(others, r)
			}
		}
	}

	if len(others) > 0 && opts.DeleteData {
		return fmt.Errorf("namespace %s also holds helm releases %s; deleting it would remove them too\n"+
			"Remove only this app's release with: obol app delete %s --keep-data, then delete its PVCs/Secrets by name (obol kubectl delete pvc … -n %s)",
			namespace, strings.Join(others, ", "), deploymentIdentifier, namespace)
	}

	if nsExists && (opts.KeepData || len(others) > 0 || (data.present() && !opts.DeleteData)) {
		if len(others) > 0 && !opts.KeepData {
			u.Warnf("Namespace %s also holds helm releases %s; only this app's release is removed.", namespace, strings.Join(others, ", "))
		}

		return deleteReleaseOnly(cfg, u, deploymentIdentifier, deploymentDir, configExists, meta, data, opts)
	}

	// Full delete.
	u.Blank()
	u.Print("Resources to be deleted:")

	if nsExists {
		u.Printf("  [x] Kubernetes namespace: %s", namespace)

		for _, pvc := range data.PVCs {
			u.Printf("  [x] PersistentVolumeClaim: %s (its data is lost)", pvc)
		}

		for _, s := range data.Secrets {
			u.Printf("  [x] Secret: %s (looks like key material)", s)
		}
	} else {
		u.Printf("  [ ] Kubernetes namespace: %s (not found)", namespace)
	}

	if configExists {
		u.Printf("  [x] Configuration directory: %s", deploymentDir)
	} else {
		u.Printf("  [ ] Configuration directory: %s (not found)", deploymentDir)
	}

	if opts.DeleteData && data.present() {
		if err := confirmDataDeletion(u, deploymentIdentifier, opts.Yes); err != nil {
			return err
		}
	} else if !opts.Force && !opts.Yes {
		u.Blank()

		ok, err := u.ConfirmOrFlag("Proceed with deletion?", "--yes")
		if err != nil {
			return err
		}

		if !ok {
			u.Print("Deletion cancelled")
			return nil
		}
	}

	if nsExists {
		u.Infof("Deleting namespace %s...", namespace)

		if _, err := kubectl(cfg, nil, "delete", "namespace", namespace, "--force", "--grace-period=0"); err != nil {
			return fmt.Errorf("failed to delete namespace: %w", err)
		}

		u.Successf("Namespace %s deleted", namespace)
	}

	if configExists {
		if err := removeConfigDir(cfg, appName, deploymentDir); err != nil {
			return err
		}

		u.Success("Configuration deleted")
	}

	u.Blank()
	u.Successf("Application %s/%s deleted successfully!", appName, id)

	return nil
}

// confirmDataDeletion requires typing the app identifier on a TTY, or
// --yes when no one can be asked.
func confirmDataDeletion(u *ui.UI, identifier string, yes bool) error {
	if yes {
		return nil
	}

	if !u.CanPrompt() {
		return errors.New("deleting PVCs/key material needs confirmation and there is no terminal to ask on: pass --delete-data --yes")
	}

	u.Blank()
	u.Warn("This permanently deletes the data above (key shares cannot be recovered from this machine).")

	typed, err := u.Input(fmt.Sprintf("Type %s to confirm", identifier), "")
	if err != nil {
		return err
	}

	if strings.TrimSpace(typed) != identifier {
		return errors.New("confirmation did not match; nothing deleted")
	}

	return nil
}

// deleteReleaseOnly uninstalls the helm release and keeps the namespace,
// its PVCs and Secrets, and the config directory.
func deleteReleaseOnly(cfg *config.Config, u *ui.UI, identifier, deploymentDir string, configExists bool, meta *AppMeta, data namespaceData, opts DeleteOptions) error {
	u.Blank()

	if !opts.KeepData {
		u.Warnf("Namespace %s holds data that is not deleted by default:", meta.Namespace)

		for _, pvc := range data.PVCs {
			u.Printf("  PersistentVolumeClaim: %s", pvc)
		}

		for _, s := range data.Secrets {
			u.Printf("  Secret: %s (looks like key material)", s)
		}

		if data.Unknown != "" {
			u.Printf("  (%s; assuming it holds data)", data.Unknown)
		}

		u.Blank()
	}

	release := meta.Release
	if !configExists || release == "" {
		release = singleReleaseIn(cfg, meta.Namespace)
	}

	u.Print("Only the helm release is removed; the namespace, its PVCs and Secrets, and the config directory are kept:")

	if release != "" {
		u.Printf("  [x] Helm release: %s", release)
	} else {
		u.Print("  [ ] Helm release: none found")
	}

	u.Printf("  [ ] Kubernetes namespace: %s (kept)", meta.Namespace)

	if configExists {
		u.Printf("  [ ] Configuration directory: %s (kept)", deploymentDir)
	}

	u.Printf("To delete everything, data included: obol app delete %s --delete-data", identifier)

	if !opts.Force && !opts.Yes {
		u.Blank()

		ok, err := u.ConfirmOrFlag("Remove the helm release?", "--yes")
		if err != nil {
			return err
		}

		if !ok {
			u.Print("Deletion cancelled")
			return nil
		}
	}

	if release != "" {
		live, err := liveRelease(cfg, meta.Namespace, release)
		if err != nil {
			return fmt.Errorf("check helm release %s: %w", release, err)
		}

		if live != nil {
			if _, err := helmCluster(cfg, "uninstall", release, "-n", meta.Namespace); err != nil {
				return fmt.Errorf("helm uninstall %s: %w", release, err)
			}

			u.Successf("Helm release %s removed", release)
		}
	}

	if configExists {
		// The release is gone: a release that appears later is not ours.
		meta.SyncedRevision = 0
		meta.SyncedChartVersion = ""

		if err := saveAppMeta(deploymentDir, meta); err != nil {
			u.Warnf("Could not update %s: %v", metaFileName, err)
		}

		u.Printf("Redeploy onto the kept data with: obol app sync %s", identifier)
	}

	return nil
}

// releasesIn lists the helm releases in a namespace (deployed and failed;
// no -a, which helm 4 removed).
func releasesIn(cfg *config.Config, namespace string) ([]string, error) {
	out, err := helmCluster(cfg, "list", "-n", namespace, "-q")
	if err != nil {
		return nil, err
	}

	return strings.Fields(string(out)), nil
}

// singleReleaseIn returns the only helm release in a namespace, or "".
func singleReleaseIn(cfg *config.Config, namespace string) string {
	names, err := releasesIn(cfg, namespace)
	if err != nil || len(names) != 1 {
		return ""
	}

	return names[0]
}

func removeConfigDir(cfg *config.Config, appName, deploymentDir string) error {
	if err := os.RemoveAll(deploymentDir); err != nil {
		return fmt.Errorf("failed to delete config directory: %w", err)
	}

	appDir := filepath.Join(cfg.ConfigDir, "applications", appName)
	if entries, err := os.ReadDir(appDir); err == nil && len(entries) == 0 {
		_ = os.Remove(appDir)
	}

	return nil
}
