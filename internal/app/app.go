package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/fsutil"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/rollout"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	petname "github.com/dustinkirkland/golang-petname"
)

// InstallOptions contains options for the install command
type InstallOptions struct {
	Name        string   // Optional app name override
	Version     string   // Chart version (empty = latest for repo/chart, extracted for URL)
	ID          string   // Deployment ID (empty = generate petname)
	Release     string   // Helm release name (empty = <name>-<id>, or the existing one with --force)
	Namespace   string   // Kubernetes namespace (empty = <name>-<id>, or the existing one with --force)
	Force       bool     // Overwrite existing deployment
	Adopt       bool     // Take over an existing helm release of the same name (values seeded from it)
	ValuesFiles []string // Values files merged into values.yaml (in order)
	Set         []string // key.path=value overrides applied after values files
}

// ListOptions contains options for the list command
type ListOptions struct {
	Verbose bool // Show detailed information
}

// newArtifactHubResolver is the ArtifactHub lookup used by Install
// (replaced in tests).
var newArtifactHubResolver = func() func(string) (*ChartInfo, error) {
	return NewArtifactHubClient().ResolveChart
}

// Install scaffolds a new application from a Helm chart reference.
//
// values.yaml records only the operator's overrides (from --values/--set),
// starting from the chart's values-examples/obol-stack.yaml when the chart
// ships one. Chart defaults are read from the chart at every sync, so a
// chart upgrade brings its new defaults (image tags included).
func Install(cfg *config.Config, u *ui.UI, chartRef string, opts InstallOptions) error {
	u.Infof("Installing application from: %s", chartRef)

	// 1. Parse chart reference
	chart, err := ParseChartReference(chartRef)
	if err != nil {
		return err
	}

	if opts.Version != "" {
		chart.Version = opts.Version
	}

	// 2. Resolve repo/chart: local helm repo alias → built-in alias → ArtifactHub
	if chart.NeedsResolution() {
		source, err := resolveRepoChart(cfg, chart, newArtifactHubResolver())
		if err != nil {
			return err
		}

		u.Detail("Resolved", fmt.Sprintf("%s/%s version %s (via %s)", chart.RepoName, chart.ChartName, chart.Version, source))
		u.Detail("Repository URL", chart.RepoURL)
	}

	// 3. Determine app name and ID
	appName := opts.Name
	if appName == "" {
		appName = chart.GetChartName()
	}

	id := opts.ID
	if id == "" {
		id = petname.Generate(2, "-")
		u.Detail("Generated deployment ID", id)
	} else {
		u.Detail("Using deployment ID", id)
	}

	defaultName := fmt.Sprintf("%s-%s", appName, id)
	deploymentDir := deploymentDirFor(cfg, appName, id)

	// 4. Existing deployment: refuse, or with --force keep its release name
	// (a new default would install a second release next to the old one).
	existingRelease := ""
	existingNamespace := ""
	carriedRevision := 0 // sync record of the deployment being overwritten
	carryLegacy := false

	if _, err := os.Stat(deploymentDir); err == nil {
		if !opts.Force {
			return fmt.Errorf("deployment already exists: %s/%s\n"+
				"Directory: %s\n"+
				"Use --force or -f to overwrite", appName, id, deploymentDir)
		}

		if old, err := loadAppMeta(deploymentDir, appName, id); err == nil {
			existingRelease = old.Release
			existingNamespace = old.Namespace
			carriedRevision = old.SyncedRevision
			carryLegacy = old.Legacy()
		}

		u.Warnf("Overwriting existing deployment at %s", deploymentDir)
	}

	// 5. Namespace and release name: flags, else the overwritten
	// deployment's, else <name>-<id>.
	namespace := firstNonEmpty(opts.Namespace, existingNamespace, defaultName)
	if err := ValidateNamespace(namespace); err != nil {
		if opts.Namespace == "" {
			return fmt.Errorf("%w\nPick another --name, or set --namespace", err)
		}

		return err
	}

	if existingNamespace != "" && namespace != existingNamespace {
		u.Warnf("This deployment was in namespace %s; syncing will install into %s. The old release stays in %s until you remove it.",
			existingNamespace, namespace, existingNamespace)

		existingRelease = "" // nothing obol manages in the new namespace yet
	}

	release := firstNonEmpty(opts.Release, existingRelease, defaultName)

	if err := ValidateReleaseName(release); err != nil {
		return err
	}

	if existingRelease != "" && release != existingRelease {
		u.Warnf("This deployment's release was %q; syncing will install %q next to it in namespace %s. Remove the old one with: obol helm uninstall %s -n %s",
			existingRelease, release, namespace, existingRelease, namespace)
	}

	u.Detail("Application", appName)
	u.Detail("Release", release)
	u.Detail("Namespace", namespace)

	// 6. A helm release with this name already in the namespace that obol
	// app doesn't manage: syncing would take it over and revert whatever
	// was set with helm directly.
	adoptedRevision := 0

	var adoptedValues []byte

	if clusterConfigured(cfg) {
		live, err := liveRelease(cfg, namespace, release)

		switch {
		case err != nil:
			u.Warnf("Could not check for an existing helm release %s in %s: %v", release, namespace, err)
		case live != nil && existingRelease == release && !opts.Adopt:
			// Re-install (--force) of an app obol already manages: keep its
			// sync record so the next sync neither calls the release foreign
			// nor misses drift. A pre-v0.19 app has no record; its current
			// revision becomes the baseline.
			if carryLegacy || carriedRevision == 0 {
				carriedRevision = live.Revision
			}
		case live != nil && !opts.Adopt:
			return fmt.Errorf("helm release %q already exists in namespace %s (revision %d) and is not managed by obol app\n"+
				"Syncing would take it over and revert changes made with helm directly. Either:\n"+
				"  - keep managing it with helm: obol helm upgrade %s <chart> -n %s ...\n"+
				"  - or let obol app take it over, seeding values.yaml from the release:\n"+
				"      obol app install %s --name %s --id %s --namespace %s --release %s --adopt",
				release, namespace, live.Revision, release, namespace, chartRef, appName, id, namespace, release)
		case live != nil:
			values, err := helmCluster(cfg, "get", "values", release, "-n", namespace, "-o", "yaml")
			if err != nil {
				return fmt.Errorf("read values of release %s: %w", release, err)
			}

			adoptedValues = values
			adoptedRevision = live.Revision
			u.Infof("Adopting helm release %s (revision %d); values.yaml starts from its current values", release, live.Revision)
		case opts.Adopt:
			return fmt.Errorf("--adopt: no helm release %q in namespace %s", release, namespace)
		}
	}

	// 7. Pull the chart (validates it and reads its Obol Stack example values)
	u.Info("Fetching chart...")

	pulled, err := pullChart(cfg, chart)
	if err != nil {
		return fmt.Errorf("failed to fetch chart: %w", err)
	}
	defer pulled.cleanup()

	if chart.Version == "" {
		chart.Version = pulled.chartVersion()
	}

	// 8. Build values.yaml: overrides only
	base := []byte(nil)
	baseNote := ""

	switch {
	case adoptedValues != nil:
		base = adoptedValues
		baseNote = fmt.Sprintf("Seeded from helm release %s (revision %d) when obol app adopted it.", release, adoptedRevision)
	case pulled.stackExampleValues() != nil:
		base = pulled.stackExampleValues()
		baseNote = "Started from the chart's " + stackExampleValuesPath + " (Obol Stack settings)."

		u.Detail("Values", "starting from the chart's "+stackExampleValuesPath)
	}

	doc, err := parseValuesNode(base, "chart values")
	if err != nil {
		return err
	}

	if err := applyOverridesToNode(doc, opts.ValuesFiles, opts.Set); err != nil {
		return fmt.Errorf("failed to apply value overrides: %w", err)
	}

	header := valuesHeader(appName, id, chart, baseNote)
	if doc.HeadComment != "" {
		// Keep the example's own leading comments (how-to notes) below ours.
		header += "\n\n" + doc.HeadComment
	}

	doc.HeadComment = header

	values, err := encodeValuesNode(doc)
	if err != nil {
		return err
	}

	// 9. Write the deployment directory
	if err := os.MkdirAll(deploymentDir, 0o755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	fail := func(err error) error {
		os.RemoveAll(deploymentDir)
		return err
	}

	if err := fsutil.WriteSecret(filepath.Join(deploymentDir, "values.yaml"), values); err != nil {
		return fail(fmt.Errorf("failed to write values.yaml: %w", err))
	}

	if err := generateRemoteHelmfile(deploymentDir, chart, release, namespace); err != nil {
		return fail(fmt.Errorf("failed to generate helmfile: %w", err))
	}

	if release != existingRelease {
		carriedRevision = 0 // a different release has no sync record yet
	}

	meta := &AppMeta{
		Release:            release,
		Namespace:          namespace,
		ChartRef:           chartRef,
		ChartName:          chart.ChartName,
		ValuesMode:         ValuesModeOverrides,
		ValuesChartVersion: chart.Version,
		SyncedRevision:     max(adoptedRevision, carriedRevision),
	}
	if err := saveAppMeta(deploymentDir, meta); err != nil {
		return fail(fmt.Errorf("failed to write %s: %w", metaFileName, err))
	}

	// 10. Print success message
	u.Blank()
	u.Successf("Application installed successfully!")
	u.Detail("Deployment", fmt.Sprintf("%s/%s", appName, id))
	u.Detail("Location", deploymentDir)
	u.Blank()
	u.Print("Files created:")
	u.Print("  - helmfile.yaml: Deployment configuration")
	u.Print("  - values.yaml: Your overrides of the chart defaults (edit to customize)")
	u.Printf("  - %s: Release name and sync record (managed by obol)", metaFileName)
	u.Blank()
	u.Printf("All chart defaults: %s", showValuesCommand(chart))
	u.Printf("To deploy, run: obol app sync %s/%s", appName, id)

	return nil
}

// valuesHeader is the comment block at the top of a new values.yaml.
func valuesHeader(appName, id string, chart *ChartReference, baseNote string) string {
	lines := []string{
		fmt.Sprintf("Values for app %s/%s (chart %s %s).", appName, id, chart.ChartName, chart.Version),
		"Only your overrides belong here: chart defaults are read from the chart on every",
		"'obol app sync', so a chart upgrade brings its new defaults (image tags included).",
		"All defaults: " + showValuesCommand(chart),
	}
	if baseNote != "" {
		lines = append(lines, baseNote)
	}

	return strings.Join(lines, "\n")
}

// generateRemoteHelmfile creates a helmfile.yaml that references the chart remotely
func generateRemoteHelmfile(dir string, chart *ChartReference, release, namespace string) error {
	var tmpl string

	switch chart.Format {
	case FormatURL, FormatOCI:
		tmpl = `# Installed from: {{ .Original }}

releases:
  - name: {{ .Release }}
    namespace: {{ .Namespace }}
    createNamespace: true
    chart: {{ .ChartURL }}
{{- if .Version }}
    version: "{{ .Version }}"
{{- end }}
    values:
      - values.yaml
`
	case FormatRepoChart:
		// Repository reference (helmfile handles repo inline)
		tmpl = `# Installed from: {{ .Original }}

repositories:
  - name: {{ .RepoName }}
    url: {{ .RepoURL }}

releases:
  - name: {{ .Release }}
    namespace: {{ .Namespace }}
    createNamespace: true
    chart: {{ .RepoName }}/{{ .ChartName }}
    version: "{{ .Version }}"
    values:
      - values.yaml
`
	}

	data := map[string]any{
		"Original":  chart.Original,
		"ChartURL":  chart.ChartURL,
		"RepoName":  chart.RepoName,
		"RepoURL":   chart.RepoURL,
		"ChartName": chart.ChartName,
		"Version":   chart.Version,
		"Release":   release,
		"Namespace": namespace,
	}

	t, err := template.New("helmfile").Parse(tmpl)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "helmfile.yaml"), buf.Bytes(), 0o600)
}

// ErrDrift is returned (wrapped) when sync refuses because the live
// release was changed outside obol app.
var ErrDrift = errors.New("release drift")

// SyncOptions controls Sync.
type SyncOptions struct {
	// Force syncs even when the live release was changed outside obol app
	// (those changes are reverted to values.yaml).
	Force bool
	// ValuesFiles and Set are merged into values.yaml after the drift check
	// and before deploying.
	ValuesFiles []string
	Set         []string
}

// Sync deploys or updates an application to the cluster. It refuses when
// the live release was changed outside obol app; see SyncWithOptions.
func Sync(cfg *config.Config, u *ui.UI, deploymentIdentifier string) error {
	return SyncWithOptions(cfg, u, deploymentIdentifier, SyncOptions{})
}

// SyncWithOptions deploys or updates an application.
//
// Before deploying it compares the live helm release with obol's record:
//   - a revision above the one obol last synced means someone ran helm
//     upgrade/rollback directly; syncing would revert that, so it refuses
//     without Force and names the keys that differ;
//   - a release that exists although this deployment was never synced is
//     a foreign release (refused without Force);
//   - pre-v0.19 deployments have no record: it warns when the live values
//     differ from values.yaml and adopts the current revision.
func SyncWithOptions(cfg *config.Config, u *ui.UI, deploymentIdentifier string, opts SyncOptions) error {
	appName, id, err := parseDeploymentIdentifier(deploymentIdentifier)
	if err != nil {
		return err
	}

	u.Infof("Syncing application: %s/%s", appName, id)

	deploymentDir := deploymentDirFor(cfg, appName, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s\nDirectory: %s", deploymentIdentifier, deploymentDir)
	}

	helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml")
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		return fmt.Errorf("helmfile.yaml not found in: %s", deploymentDir)
	}

	valuesPath := filepath.Join(deploymentDir, "values.yaml")
	if _, err := os.Stat(valuesPath); os.IsNotExist(err) {
		return fmt.Errorf("values.yaml not found in: %s", deploymentDir)
	}

	if !clusterConfigured(cfg) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	helmfileBinary := cfg.ToolPath("helmfile")
	if _, err := os.Stat(helmfileBinary); os.IsNotExist(err) {
		return fmt.Errorf("helmfile not found at %s; run 'obol upgrade' to install missing tools", helmfileBinary)
	}

	meta, err := loadAppMeta(deploymentDir, appName, id)
	if err != nil {
		return err
	}

	u.Detail("Deployment directory", deploymentDir)
	u.Detail("Release", meta.Release)
	u.Detail("Namespace", meta.Namespace)

	// Drift check runs before values.yaml is touched by --values/--set.
	live, err := liveRelease(cfg, meta.Namespace, meta.Release)
	if err != nil {
		u.Warnf("Could not read helm release %s: %v", meta.Release, err)
	}

	if live != nil {
		if err := checkDrift(cfg, u, meta, live, valuesPath, appName, id, opts.Force); err != nil {
			return err
		}

		warnStaleDefaults(u, meta, live, helmfilePath, appName, id)
	}

	if err := ApplyOverrides(cfg, deploymentIdentifier, opts.ValuesFiles, opts.Set); err != nil {
		return err
	}

	if paths, err := bareHexPaths(valuesPath); err == nil && len(paths) > 0 {
		u.Warnf("values.yaml has unquoted hex values that helm reads as numbers (a long address or hash becomes a float): %s. Quote them, e.g. key: \"0x…\"",
			formatPaths(paths))
	}

	syncArgs := append([]string{"-f", helmfilePath, "sync"}, helmcmd.SyncFlagsForVersion(cfg.ToolPath("helm"))...)
	cmd := helmcmd.Helmfile(helmfileBinary, cfg.ToolPath("helm"), syncArgs...)
	cmd.Dir = deploymentDir
	cmd.Env = append(os.Environ(), "KUBECONFIG="+stackKubeconfig(cfg))

	syncErr := u.Exec(ui.ExecConfig{Name: "Running helmfile sync", Cmd: cmd})

	// Record the revision this sync produced, even after a failed sync
	// (a failed upgrade still adds a revision, and it was ours).
	recordSync(cfg, u, deploymentDir, meta)

	if syncErr != nil {
		return fmt.Errorf("helmfile sync failed: %w", syncErr)
	}

	// A StatefulSet never replaces a pod that is not Ready, so a pod parked
	// in Init (e.g. a dv-pod waiting for its DKG) would keep the old spec.
	// Only pods still in Init are replaced: none of their main containers
	// (charon, a validator client) has started, so nothing is interrupted.
	rollout.Unstick(func(args ...string) (string, error) {
		out, err := kubectl(cfg, nil, args...)
		return string(out), err
	}, u, meta.Namespace, rollout.Options{InitOnly: true, Wait: unstickWait})

	u.Blank()
	u.Success("Application synced successfully!")
	u.Detail("Namespace", meta.Namespace)
	u.Blank()
	u.Printf("To check status: obol kubectl get all -n %s", meta.Namespace)
	u.Printf("To view logs: obol kubectl logs -n %s <pod-name>", meta.Namespace)

	return nil
}

// checkDrift compares the live release with obol's sync record.
func checkDrift(cfg *config.Config, u *ui.UI, meta *AppMeta, live *releaseState, valuesPath, appName, id string, force bool) error {
	changedKeys := func() string {
		fileValues, ferr := loadValuesMap(valuesPath)
		liveVals, lerr := liveValues(cfg, meta.Namespace, meta.Release)

		if ferr != nil || lerr != nil {
			return ""
		}

		if diff := valuesDiff(fileValues, liveVals); len(diff) > 0 {
			return "\nValues that differ between the live release and values.yaml: " + formatPaths(diff)
		}

		return ""
	}

	foldHint := fmt.Sprintf("Fold those changes into %s (see: obol helm get values %s -n %s), then run 'obol app sync %s/%s --force'.\n"+
		"Or pass --force now to overwrite them with values.yaml.", valuesPath, meta.Release, meta.Namespace, appName, id)

	switch {
	case meta.Legacy():
		if keys := changedKeys(); keys != "" {
			u.Warnf("Helm release %s (revision %d) was deployed before obol app kept a sync record.%s\n"+
				"Expected if you edited values.yaml. If someone changed the release with helm directly, this sync reverts that.",
				meta.Release, live.Revision, keys)
		}

		return nil
	case meta.SyncedRevision == 0:
		msg := fmt.Sprintf("helm release %s already exists in namespace %s (revision %d) but obol app never synced it: it was installed outside obol app.%s",
			meta.Release, meta.Namespace, live.Revision, changedKeys())
		if force {
			u.Warnf("%s\n--force: taking it over with values.yaml.", msg)
			return nil
		}

		return fmt.Errorf("%w: %s\n%s", ErrDrift, msg, foldHint)
	case live.Revision > meta.SyncedRevision:
		msg := fmt.Sprintf("helm release %s was changed outside obol app: it is at revision %d (%s), obol app last synced revision %d.%s",
			meta.Release, live.Revision, live.Status, meta.SyncedRevision, changedKeys())
		if force {
			u.Warnf("%s\n--force: overwriting with values.yaml.", msg)
			return nil
		}

		return fmt.Errorf("%w: %s\n%s", ErrDrift, msg, foldHint)
	}

	return nil
}

// warnStaleDefaults warns when a deployment whose values.yaml holds a
// chart's full defaults (pre-v0.19 install) moves to another chart version:
// the old defaults, image tags included, stay pinned as user values.
func warnStaleDefaults(u *ui.UI, meta *AppMeta, live *releaseState, helmfilePath, appName, id string) {
	if meta.ValuesMode != ValuesModeFullDefaults {
		return
	}

	info, err := ParseHelmfile(filepath.Dir(helmfilePath))
	if err != nil || info.Version == "" || live.ChartVersion == "" || info.Version == live.ChartVersion {
		return
	}

	u.Warnf("Chart version changes %s → %s, but values.yaml is a full copy of the chart defaults from install time: "+
		"those old defaults (image tags included) stay pinned and override the new chart's.\n"+
		"To slim it, keep only the keys you changed (compare with 'obol helm show values' for the old version), "+
		"or reinstall with only your overrides: obol app install <chart>@%s --name %s --id %s --force --values <your-overrides.yaml>",
		live.ChartVersion, info.Version, info.Version, appName, id)
}

// recordSync stores the live revision after a sync in obol-app.yaml.
func recordSync(cfg *config.Config, u *ui.UI, dir string, meta *AppMeta) {
	live, err := liveRelease(cfg, meta.Namespace, meta.Release)
	if err != nil || live == nil {
		return
	}

	meta.SyncedRevision = live.Revision
	meta.SyncedChartVersion = live.ChartVersion

	if err := saveAppMeta(dir, meta); err != nil {
		u.Warnf("Could not record the synced revision in %s: %v", metaFileName, err)
	}
}

// unstickWait is how long a sync lets the StatefulSet controller roll pods
// parked in Init before replacing them (0 = the rollout package default).
// Tests shorten it.
var unstickWait time.Duration

// parseDeploymentIdentifier parses "app-name/id" format
func parseDeploymentIdentifier(identifier string) (appName, id string, err error) {
	// Try slash separator
	if strings.Contains(identifier, "/") {
		parts := strings.SplitN(identifier, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", errors.New("invalid format. Use: <app>/<id>")
		}

		return parts[0], parts[1], nil
	}

	return "", "", errors.New("please use <app>/<id> format (e.g., postgresql/eager-fox)")
}

// List displays installed applications
func List(cfg *config.Config, u *ui.UI, opts ListOptions) error {
	appsDir := filepath.Join(cfg.ConfigDir, "applications")

	// Check if applications directory exists
	if _, err := os.Stat(appsDir); os.IsNotExist(err) {
		u.Print("No applications installed")
		u.Blank()
		u.Print("To install an application:")
		u.Print("  obol app install bitnami/redis")
		u.Print("  obol app install https://charts.bitnami.com/bitnami/redis-19.0.0.tgz")
		u.Blank()
		u.Print("Find charts at https://artifacthub.io")

		return nil
	}

	// Walk through applications directory
	apps, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("failed to read applications directory: %w", err)
	}

	if len(apps) == 0 {
		u.Print("No applications installed")
		return nil
	}

	u.Bold("Installed applications:")
	u.Blank()

	count := 0

	for _, appDir := range apps {
		if !appDir.IsDir() {
			continue
		}

		appName := appDir.Name()
		appPath := filepath.Join(appsDir, appName)

		// List deployments for this app
		deployments, err := os.ReadDir(appPath)
		if err != nil {
			continue
		}

		for _, deployment := range deployments {
			if !deployment.IsDir() {
				continue
			}

			id := deployment.Name()
			deploymentPath := filepath.Join(appPath, id)

			// Parse helmfile for chart info
			info, err := ParseHelmfile(deploymentPath)
			if err != nil {
				// Helmfile not found - show basic info
				u.Printf("  %s/%s", appName, id)

				count++

				continue
			}

			// Show deployment info
			if opts.Verbose {
				u.Printf("  %s/%s", appName, id)
				u.Detail("    Chart", info.ChartRef)
				u.Detail("    Version", info.Version)

				if modTime, err := GetHelmfileModTime(deploymentPath); err == nil {
					u.Detail("    Modified", modTime)
				}

				u.Blank()
			} else {
				u.Printf("  %s/%s (chart: %s, version: %s)",
					appName, id, info.ChartRef, info.Version)
			}

			count++
		}
	}

	u.Blank()
	u.Printf("Total: %d application deployment(s)", count)

	return nil
}
