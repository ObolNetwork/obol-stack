package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"gopkg.in/yaml.v3"
)

// deploymentOverridesFile is the optional user values layer in a deployment
// dir. Sync merges it last into the ethereum release (see
// helmfile.yaml.gotmpl), and refreshDeploymentFiles keeps it.
const deploymentOverridesFile = "overrides.yaml"

// silverFormatAlpha is the first Silver alpha with the current on-disk
// format: alpha.18 changed it, so older beacon data must be resynced.
const silverFormatAlpha = 18

// liveImagesFn returns the container images of the StatefulSets in a
// namespace, keyed by container name. Swappable for tests.
var liveImagesFn = func(cfg *config.Config, namespace string) (map[string]string, error) {
	bin, kubeconfig := kubectl.Paths(cfg)
	if _, err := os.Stat(kubeconfig); err != nil {
		return nil, err
	}

	out, err := kubectl.Output(bin, kubeconfig, "get", "statefulset", "-n", namespace,
		"--request-timeout=5s", "-o", "json")
	if err != nil {
		return nil, err
	}

	return parseStatefulSetImages([]byte(out))
}

func parseStatefulSetImages(raw []byte) (map[string]string, error) {
	var list struct {
		Items []struct {
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Name  string `json:"name"`
							Image string `json:"image"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}

	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}

	images := map[string]string{}

	for _, it := range list.Items {
		for _, c := range it.Spec.Template.Spec.Containers {
			images[c.Name] = c.Image
		}
	}

	return images, nil
}

// clientBranch matches a client branch of the image tag block in the
// ethereum helmfile, e.g. `eq .Values.consensusClient "silver"`.
var (
	clientBranch = regexp.MustCompile(`eq \.Values\.(?:execution|consensus)Client "([a-z0-9-]+)"`)
	tagLine      = regexp.MustCompile(`^\s*tag:\s*"?([A-Za-z0-9._+-]+)"?\s*$`)
)

// pinnedImageTags reads the image tag the ethereum helmfile pins for each
// client: the first `tag:` line after a client's branch, before any other
// branch.
func pinnedImageTags(helmfile []byte) map[string]string {
	tags := map[string]string{}
	client := ""

	for line := range strings.SplitSeq(string(helmfile), "\n") {
		if m := clientBranch.FindStringSubmatch(line); m != nil {
			client = m[1]
			continue
		}

		if client == "" {
			continue
		}

		if m := tagLine.FindStringSubmatch(line); m != nil {
			if _, seen := tags[client]; !seen {
				tags[client] = m[1]
			}

			client = ""
		}
	}

	return tags
}

// overrideImageTags reads <client>.image.tag from a deployment's
// overrides.yaml. A missing file is no overrides.
func overrideImageTags(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}

	if err != nil {
		return nil, err
	}

	var doc map[string]struct {
		Image struct {
			Tag any `yaml:"tag"`
		} `yaml:"image"`
	}

	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	tags := map[string]string{}

	for client, v := range doc {
		if v.Image.Tag != nil {
			tags[client] = fmt.Sprint(v.Image.Tag)
		}
	}

	return tags, nil
}

// imageTag returns the tag of an image reference ("" for none), ignoring
// any @sha256 digest.
func imageTag(image string) string {
	image, _, _ = strings.Cut(image, "@")

	slash := strings.LastIndex(image, "/")
	if colon := strings.LastIndex(image, ":"); colon > slash {
		return image[colon+1:]
	}

	return ""
}

// silverAlpha returns N of a "v0.1.0-alpha.N" tag.
func silverAlpha(tag string) (int, bool) {
	rest, ok := strings.CutPrefix(tag, "v0.1.0-alpha.")
	if !ok {
		return 0, false
	}

	n, err := strconv.Atoi(rest)

	return n, err == nil
}

// imageDrift is a live client image that sync will replace although it is
// neither the old nor the new pin (a hand override such as kubectl set image).
type imageDrift struct {
	Client, Live, Target string
}

// syncImagePlan compares the live images of d with what sync will render.
type syncImagePlan struct {
	Drift []imageDrift
	// SilverResync is set when the live Silver predates the current data
	// format and the target does not.
	SilverResync       bool
	SilverLive, Silver string
}

func planSyncImages(d Deployment, live, oldPins, newPins, overrides map[string]string) syncImagePlan {
	var plan syncImagePlan

	for _, client := range []string{d.ExecutionClient, d.ConsensusClient} {
		image, ok := live[client]
		if client == "" || !ok {
			continue
		}

		liveTag := imageTag(image)
		target := newPins[client]
		if t, ok := overrides[client]; ok {
			target = t
		}

		if liveTag != "" && target != "" && liveTag != target && liveTag != oldPins[client] && liveTag != overrides[client] {
			plan.Drift = append(plan.Drift, imageDrift{Client: client, Live: image, Target: target})
		}

		if client == "silver" {
			from, okFrom := silverAlpha(liveTag)
			to, okTo := silverAlpha(target)
			if okFrom && okTo && from < silverFormatAlpha && to >= silverFormatAlpha {
				plan.SilverResync, plan.SilverLive, plan.Silver = true, liveTag, target
			}
		}
	}

	return plan
}

// guardSyncImages runs before an ethereum sync refreshes the deployment's
// files: it warns about hand-set images sync will revert, and asks before
// moving Silver across its data-format change. It does nothing when the
// cluster cannot be asked. oldHelmfile is the deployment's current copy.
func guardSyncImages(cfg *config.Config, u *ui.UI, d Deployment, deploymentDir string, oldHelmfile, newHelmfile []byte, yes bool) error {
	overrides, err := overrideImageTags(filepath.Join(deploymentDir, deploymentOverridesFile))
	if err != nil {
		return err
	}

	live, err := liveImagesFn(cfg, d.Namespace)
	if err != nil || len(live) == 0 {
		return nil //nolint:nilerr // not deployed or cluster unreachable: nothing live to compare
	}

	plan := planSyncImages(d, live, pinnedImageTags(oldHelmfile), pinnedImageTags(newHelmfile), overrides)
	ident := d.Identifier()

	for _, dr := range plan.Drift {
		u.Warnf("%s runs %s, which is not the pinned image; sync will set %s to %s", d.Namespace, dr.Live, dr.Client, dr.Target)
		u.Printf("  To keep it, set it in %s:", filepath.Join(deploymentDir, deploymentOverridesFile))
		u.Printf("    %s:\n      image:\n        tag: %s", dr.Client, imageTag(dr.Live))
	}

	if !plan.SilverResync {
		return nil
	}

	steps := silverResyncSteps(cfg, d)
	u.Warnf("%s runs Silver %s; this sync moves it to %s. Silver alpha.%d changed its data format, so the existing beacon data cannot be read and needs a fresh checkpoint sync (minutes on Hoodi).",
		ident, plan.SilverLive, plan.Silver, silverFormatAlpha)
	u.Print(steps)

	ok, err := confirmOrYes(u, yes, "Sync anyway? Silver will not start until its old data is removed")
	if err != nil {
		return fmt.Errorf("%w\n\n%s", err, steps)
	}

	if !ok {
		return fmt.Errorf("sync of %s cancelled; nothing changed", ident)
	}

	return nil
}

func confirmOrYes(u *ui.UI, yes bool, msg string) (bool, error) {
	if yes {
		return true, nil
	}

	return u.ConfirmOrFlag(msg, "--yes")
}

// silverResyncSteps are the manual steps to move a Silver node to the new
// data format. obol never deletes node data itself.
func silverResyncSteps(cfg *config.Config, d Deployment) string {
	ident := d.Identifier()
	dataDir := filepath.Join(cfg.DataDir, d.Namespace, "consensus-silver-"+d.Network)

	return fmt.Sprintf(`To resync Silver from a checkpoint (the execution client's data is kept):
  1. obol network stop %[1]s
  2. obol network sync %[1]s --yes      (new image, still stopped)
  3. Remove the old beacon data (owned by the container user, so sudo may be needed):
       find %[2]q -mindepth 1 -delete
  4. obol network start %[1]s           (Silver checkpoint-syncs from scratch)`, ident, dataDir)
}
