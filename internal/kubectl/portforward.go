package kubectl

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Forward is a running `kubectl port-forward` bound to an OS-chosen
// loopback port. Close it when done.
type Forward struct {
	LocalPort int

	cmd    *exec.Cmd
	cancel context.CancelFunc
	once   sync.Once
}

// URL returns http://127.0.0.1:<local port>.
func (f *Forward) URL() string { return fmt.Sprintf("http://127.0.0.1:%d", f.LocalPort) }

// Close stops the port-forward and reaps the process.
func (f *Forward) Close() {
	f.once.Do(func() {
		f.cancel()
		_ = f.cmd.Wait()
	})
}

// PortForward starts `kubectl port-forward -n <namespace> <target> :<remotePort>`
// against the given kubeconfig (always obol's own stack kubeconfig — never the
// user's ambient KUBECONFIG) and waits up to readyTimeout for kubectl to
// report the local port. target is e.g. "svc/ethereum-execution".
//
// On failure the error carries kubectl's stderr (e.g. "pod is not running"),
// so callers can surface it as-is.
func PortForward(ctx context.Context, binary, kubeconfig, namespace, target string, remotePort int, readyTimeout time.Duration) (*Forward, error) {
	ctx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(ctx, binary,
		"port-forward", "--address", "127.0.0.1",
		"-n", namespace, target, fmt.Sprintf(":%d", remotePort))
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	// Don't let a lingering grandchild holding the pipes hang Close.
	cmd.WaitDelay = 2 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stderr lockedBuffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start port-forward: %w", err)
	}

	port, err := ParseForwardedPort(stdout, readyTimeout)
	if err != nil {
		cancel()
		_ = cmd.Wait()

		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("port-forward %s/%s: %s", namespace, target, lastLine(msg))
		}

		return nil, fmt.Errorf("port-forward %s/%s: %w", namespace, target, err)
	}

	// Keep draining stdout ("Handling connection for ...") so kubectl never
	// blocks on a full pipe.
	go func() { _, _ = io.Copy(io.Discard, stdout) }()

	return &Forward{LocalPort: port, cmd: cmd, cancel: cancel}, nil
}

// ParseForwardedPort reads kubectl port-forward output until it sees
// "Forwarding from 127.0.0.1:<port> -> <remote>" and returns <port>.
func ParseForwardedPort(r io.Reader, timeout time.Duration) (int, error) {
	type result struct {
		port int
		err  error
	}

	ch := make(chan result, 1)

	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			if port, ok := forwardedPort(scanner.Text()); ok {
				ch <- result{port: port}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- result{err: err}
			return
		}

		ch <- result{err: errors.New("port-forward exited before reporting a local port")}
	}()

	select {
	case res := <-ch:
		return res.port, res.err
	case <-time.After(timeout):
		return 0, fmt.Errorf("timed out after %s waiting for port-forward to bind", timeout)
	}
}

func forwardedPort(line string) (int, bool) {
	if !strings.Contains(line, "Forwarding from") {
		return 0, false
	}

	colon := strings.LastIndex(line, ":")
	arrow := strings.Index(line, " -> ")
	if colon < 0 || arrow < 0 || colon >= arrow {
		return 0, false
	}

	port, err := strconv.Atoi(strings.TrimSpace(line[colon+1 : arrow]))
	if err != nil || port <= 0 {
		return 0, false
	}

	return port, true
}

func lastLine(s string) string {
	if _, last, ok := strings.CutLast(s, "\n"); ok {
		return strings.TrimSpace(last)
	}

	return s
}

// lockedBuffer is a bytes.Buffer safe for exec's stderr copier goroutine and
// a concurrent reader.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
