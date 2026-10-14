package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// PodStatus is a pod in the network namespace, condensed.
type PodStatus struct {
	Name      string `json:"name"`
	Component string `json:"component"`
	Phase     string `json:"phase"`
	Ready     bool   `json:"ready"`
	Restarts  int32  `json:"restarts"`
	// Reason is the current waiting/terminated reason (CrashLoopBackOff,
	// ContainerCreating, OOMKilled …).
	Reason string `json:"reason,omitempty"`
	// LastTermination is the most recent container exit (lastState), the
	// cause behind a CrashLoopBackOff.
	LastTermination *Termination `json:"last_termination,omitempty"`
	// Unschedulable is the scheduler's message while the pod can't be
	// placed (e.g. "0/1 nodes are available: 1 Insufficient memory.").
	Unschedulable string `json:"unschedulable,omitempty"`
	MemoryRequest string `json:"memory_request,omitempty"`
	MemoryLimit   string `json:"memory_limit,omitempty"`
}

// Termination is one container exit.
type Termination struct {
	Reason     string    `json:"reason,omitempty"`
	ExitCode   int32     `json:"exit_code"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
}

// Pod problem states.
const (
	ProblemRestarting  = "restarting"
	ProblemRestarted   = "restarted"
	ProblemPending     = "pending"
	ProblemImagePull   = "image_pull"
	ProblemStarting    = "starting"
	ProblemFailed      = "failed"
	recentRestartAfter = time.Hour
)

// PodProblem is one pod-level issue, phrased once as state + cause + hint
// ("Restarting — OOMKilled 9× (last 3m ago)").
type PodProblem struct {
	Component string     `json:"component"`
	Pod       string     `json:"pod"`
	State     string     `json:"state"`
	Cause     string     `json:"cause,omitempty"`
	Restarts  int32      `json:"restarts,omitempty"`
	LastAt    *time.Time `json:"last_at,omitempty"`
	Message   string     `json:"message"`
	Hint      string     `json:"hint,omitempty"`
}

// Severe reports whether the problem stops the pod from serving (as
// opposed to a pod that is starting, or recovered from a recent restart).
func (p PodProblem) Severe() bool {
	switch p.State {
	case ProblemRestarting, ProblemPending, ProblemImagePull, ProblemFailed:
		return true
	default:
		return false
	}
}

// memoryValueKey is the values.yaml key (and install flag) that sets a
// component's memory request.
func memoryValueKey(component string) string {
	switch component {
	case componentExecution:
		return "executionMemory"
	case componentBeacon:
		return "consensusMemory"
	default:
		return ""
	}
}

// PodProblems turns pod state into problems, at most one per pod. Healthy
// pods (ready, no restart within the last hour) produce none.
func PodProblems(d Deployment, pods []PodStatus, now time.Time) []PodProblem {
	out := []PodProblem{}

	for _, p := range pods {
		if pr, ok := podProblem(d, p, now); ok {
			out = append(out, pr)
		}
	}

	return out
}

func podProblem(d Deployment, p PodStatus, now time.Time) (PodProblem, bool) {
	pr := PodProblem{Component: p.Component, Pod: p.Name, Restarts: p.Restarts}
	describe := fmt.Sprintf("obol kubectl describe pod -n %s %s", d.Namespace, p.Name)
	memKey := memoryValueKey(p.Component)
	memHint := func(prefix string) string {
		if memKey == "" {
			return prefix
		}

		return fmt.Sprintf("%s set %s: <size> in the deployment's values.yaml, then obol network sync %s", prefix, memKey, d.Identifier())
	}

	var lastAt *time.Time
	if t := p.LastTermination; t != nil && !t.FinishedAt.IsZero() {
		at := t.FinishedAt
		lastAt = &at
	}

	lastCause := ""
	if t := p.LastTermination; t != nil {
		lastCause = t.Reason
		if lastCause == "" || lastCause == "Error" {
			lastCause = fmt.Sprintf("exit code %d", t.ExitCode)
		}
	}

	switch {
	case p.Unschedulable != "":
		pr.State = ProblemPending
		pr.Cause = schedulerCause(p.Unschedulable)
		pr.Message = "Pending — can't be scheduled: " + pr.Cause
		if strings.Contains(strings.ToLower(p.Unschedulable), "memory") {
			req := p.MemoryRequest
			if req == "" {
				req = "its"
			}
			pr.Hint = memHint(fmt.Sprintf("Not enough free memory for %s request; free memory, or lower it:", req))
		} else {
			pr.Hint = describe
		}
	case isImagePullReason(p.Reason):
		pr.State = ProblemImagePull
		pr.Cause = p.Reason
		pr.Message = "Image pull failing — " + p.Reason
		pr.Hint = "Check internet access from the cluster; " + describe
	case p.Phase == "Failed" || isConfigErrorReason(p.Reason):
		pr.State = ProblemFailed
		pr.Cause = firstNonEmpty(p.Reason, p.Phase)
		pr.Message = "Failed — " + pr.Cause
		pr.Hint = describe
	case p.Restarts > 0 && !p.Ready:
		pr.State = ProblemRestarting
		pr.Cause = firstNonEmpty(lastCause, p.Reason)
		pr.LastAt = lastAt
		pr.Message = "Restarting — " + restartPhrase(pr.Cause, p.Restarts, lastAt, now)
		pr.Hint = restartHint(p, d, memHint)
	case p.Restarts > 0 && lastAt != nil && now.Sub(*lastAt) < recentRestartAfter:
		pr.State = ProblemRestarted
		pr.Cause = lastCause
		pr.LastAt = lastAt
		pr.Message = "Recovered — " + restartPhrase(lastCause, p.Restarts, lastAt, now)
		pr.Hint = restartHint(p, d, memHint)
	case !p.Ready:
		pr.State = ProblemStarting
		pr.Cause = firstNonEmpty(p.Reason, p.Phase)
		pr.Message = "Starting"
		if pr.Cause != "" && pr.Cause != "Running" {
			pr.Message += " — " + pr.Cause
		} else {
			pr.Message += " — not ready yet"
		}
	default:
		return PodProblem{}, false
	}

	return pr, true
}

func restartPhrase(cause string, restarts int32, lastAt *time.Time, now time.Time) string {
	s := fmt.Sprintf("%d×", restarts)
	if cause != "" {
		s = cause + " " + s
	}

	if lastAt != nil {
		s += " (last " + Ago(now.Sub(*lastAt)) + " ago)"
	}

	return s
}

func restartHint(p PodStatus, d Deployment, memHint func(string) string) string {
	if p.LastTermination != nil && p.LastTermination.Reason == "OOMKilled" {
		limit := "no memory limit"
		if p.MemoryLimit != "" {
			limit = "limit " + p.MemoryLimit
		}

		req := "no request"
		if p.MemoryRequest != "" {
			req = "request " + p.MemoryRequest
		}

		return memHint(fmt.Sprintf("Out of memory (%s, %s): free host memory (e.g. unload models: ollama ps), or raise the request:", req, limit))
	}

	return fmt.Sprintf("Logs: obol kubectl logs -n %s %s --previous", d.Namespace, p.Name)
}

// Ago renders a duration as "45s", "3m", "2h", "4d".
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// schedulerCause trims "0/1 nodes are available: 1 Insufficient memory.
// preemption: …" down to "Insufficient memory".
func schedulerCause(msg string) string {
	s := msg
	if _, after, ok := strings.Cut(s, "are available:"); ok {
		s = after
	}

	s, _, _ = strings.Cut(s, ". preemption")
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "."))

	// Drop the node counts ("1 Insufficient memory, 1 Too many pods").
	parts := strings.Split(s, ",")
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if n, rest, ok := strings.Cut(part, " "); ok && isDigits(n) {
			part = rest
		}
		parts[i] = part
	}

	if out := strings.Join(parts, ", "); out != "" {
		return out
	}

	return msg
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func isImagePullReason(r string) bool {
	switch r {
	case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "ErrImageNeverPull":
		return true
	default:
		return false
	}
}

func isConfigErrorReason(r string) bool {
	switch r {
	case "CreateContainerConfigError", "CreateContainerError", "RunContainerError":
		return true
	default:
		return false
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}

// crashCause returns why a component's pod can't run ("OOMKilled",
// "CrashLoopBackOff", "Insufficient memory" …), or "" when it isn't
// crash-looping, failing to pull or unschedulable.
func crashCause(pods []PodStatus, component string) string {
	for _, p := range pods {
		if p.Component != component || p.Ready {
			continue
		}

		switch {
		case p.Unschedulable != "":
			return "pending: " + schedulerCause(p.Unschedulable)
		case isImagePullReason(p.Reason), isConfigErrorReason(p.Reason):
			return p.Reason
		case p.Restarts > 0:
			if t := p.LastTermination; t != nil && t.Reason != "" && t.Reason != "Error" {
				return t.Reason
			}

			return firstNonEmpty(p.Reason, "crash-looping")
		}
	}

	return ""
}

// podWarnings keeps the v0.16 JSON verdict.warnings for not-ready pods.
// Human output renders Problems instead.
func podWarnings(pods []PodStatus) []string {
	var out []string

	for _, p := range pods {
		if !p.Ready && (p.Reason != "" || p.Restarts > 0) {
			msg := fmt.Sprintf("%s pod %s not ready", p.Component, p.Name)
			switch p.Reason {
			case "":
			case "OOMKilled":
				msg += " (OOMKilled: out of memory — check host memory and the pod's limits)"
			default:
				msg += " (" + p.Reason + ")"
			}
			if p.Restarts > 0 {
				msg += fmt.Sprintf(", %d restarts", p.Restarts)
			}
			out = append(out, msg)
		}
	}

	return out
}

func firstUnhealthyPod(pods []PodStatus) string {
	for _, p := range pods {
		if p.Ready {
			continue
		}

		reason := p.Reason
		if reason == "" {
			reason = p.Phase
		}

		return fmt.Sprintf("%s pod %s", p.Component, reason)
	}

	return ""
}

// kubectlPods lists pods in namespace via obol's kubeconfig.
func kubectlPods(ctx context.Context, bin, kubeconfig, namespace string) ([]PodStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "get", "pods", "-n", namespace, "-o", "json", "--request-timeout=4s")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}

		return nil, err
	}

	return parsePodList(out)
}

type podTermination struct {
	Reason     string    `json:"reason"`
	ExitCode   int32     `json:"exitCode"`
	FinishedAt time.Time `json:"finishedAt"`
}

// parsePodList condenses `kubectl get pods -o json`.
func parsePodList(raw []byte) ([]PodStatus, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Resources struct {
						Requests map[string]string `json:"requests"`
						Limits   map[string]string `json:"limits"`
					} `json:"resources"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase      string `json:"phase"`
				Conditions []struct {
					Type    string `json:"type"`
					Status  string `json:"status"`
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"conditions"`
				ContainerStatuses []struct {
					Ready        bool  `json:"ready"`
					RestartCount int32 `json:"restartCount"`
					State        struct {
						Waiting *struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
						Terminated *podTermination `json:"terminated"`
					} `json:"state"`
					LastState struct {
						Terminated *podTermination `json:"terminated"`
					} `json:"lastState"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse pod list: %w", err)
	}

	pods := make([]PodStatus, 0, len(list.Items))
	for _, it := range list.Items {
		p := PodStatus{
			Name:      it.Metadata.Name,
			Component: it.Metadata.Labels["app.kubernetes.io/name"],
			Phase:     it.Status.Phase,
			Ready:     len(it.Status.ContainerStatuses) > 0,
		}
		if p.Component == "" {
			p.Component = p.Name
		}

		if len(it.Spec.Containers) > 0 {
			res := it.Spec.Containers[0].Resources
			p.MemoryRequest = res.Requests["memory"]
			p.MemoryLimit = res.Limits["memory"]
		}

		for _, c := range it.Status.Conditions {
			if c.Type == "PodScheduled" && c.Status == "False" && c.Reason == "Unschedulable" {
				p.Unschedulable = c.Message
			}
		}

		for _, cs := range it.Status.ContainerStatuses {
			p.Ready = p.Ready && cs.Ready
			p.Restarts += cs.RestartCount

			if p.Reason == "" && cs.State.Waiting != nil {
				p.Reason = cs.State.Waiting.Reason
			}

			if p.Reason == "" && cs.State.Terminated != nil {
				p.Reason = cs.State.Terminated.Reason
			}

			for _, t := range []*podTermination{cs.State.Terminated, cs.LastState.Terminated} {
				if t == nil {
					continue
				}

				if p.LastTermination == nil || t.FinishedAt.After(p.LastTermination.FinishedAt) {
					p.LastTermination = &Termination{Reason: t.Reason, ExitCode: t.ExitCode, FinishedAt: t.FinishedAt}
				}
			}
		}

		pods = append(pods, p)
	}

	return pods, nil
}
