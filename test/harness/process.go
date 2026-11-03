package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	// startTimeout bounds the wait for the listening line.
	startTimeout = 10 * time.Second

	// stopTimeout bounds the wait for an exit after SIGTERM.
	stopTimeout = 10 * time.Second

	// runTimeout bounds a run of the binary that should exit by itself.
	runTimeout = 15 * time.Second
)

// Process is a running binary. URL and AdminURL are the bound public and
// admin addresses, as http://host:port.
type Process struct {
	URL      string
	AdminURL string

	cmd    *exec.Cmd
	out    *output
	exited chan struct{}
	err    error // cmd.Wait's result, set before exited closes

	// alive ends when the process exits, with the exit as its cause.
	alive context.Context
	gone  context.CancelCauseFunc
}

// Start runs bin with its own data directory and both listen addresses at
// 127.0.0.1:0, then env, which can override them, and waits for the
// listening line: both sockets are bound then, so a request is answered.
// When the test ends, the process is killed if it still runs, and its log
// is shown if the test failed.
func Start(t testing.TB, bin string, env ...string) *Process {
	t.Helper()

	p := &Process{
		out:    &output{addrs: make(chan addresses, 1)},
		exited: make(chan struct{}),
	}
	p.alive, p.gone = context.WithCancelCause(context.Background())

	p.cmd = exec.CommandContext(context.Background(), bin)
	p.cmd.Env = append(baseEnv(t), env...)
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out

	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}

	go p.wait()
	t.Cleanup(func() { p.cleanup(t) })

	if err := p.ready(); err != nil {
		t.Fatalf("mingest didn't get ready: %v", err)
	}

	return p
}

// Stop sends SIGTERM and fails the test unless the process exits with
// code 0.
func (p *Process) Stop(t testing.TB) {
	t.Helper()

	code, err := p.terminate()
	if err != nil {
		t.Fatalf("mingest didn't stop: %v", err)
	}

	if code != 0 {
		t.Fatalf("mingest exited with code %d after SIGTERM, want 0", code)
	}
}

// Log returns what the process has written so far, its log lines.
func (p *Process) Log() string {
	return p.out.String()
}

// Pid is the process's id.
func (p *Process) Pid() int {
	return p.cmd.Process.Pid
}

// State is how the process exited, with its resource usage, or nil while it
// still runs.
func (p *Process) State() *os.ProcessState {
	select {
	case <-p.exited:
		return p.cmd.ProcessState
	default:
		return nil
	}
}

// Kill sends SIGKILL and waits for the process to end.
func (p *Process) Kill(t testing.TB) {
	t.Helper()

	err := p.cmd.Process.Kill()
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}

	<-p.exited
}

func (p *Process) wait() {
	p.err = p.cmd.Wait()
	p.gone(fmt.Errorf("it exited: %w", p.err))
	close(p.exited)
}

func (p *Process) cleanup(t testing.TB) {
	_ = p.cmd.Process.Kill()
	<-p.exited

	if t.Failed() {
		t.Logf("mingest's log:\n%s", p.out.String())
	}
}

// ready waits for the listening line, unless the process exits or
// startTimeout passes first.
func (p *Process) ready() error {
	ctx, cancel := context.WithTimeoutCause(
		p.alive, startTimeout, errors.New("it didn't listen in time"),
	)
	defer cancel()

	a, err := p.listening(ctx)
	if err != nil {
		return err
	}

	if err := a.local(); err != nil {
		return err
	}

	p.URL, p.AdminURL = "http://"+a.Listen, "http://"+a.Admin

	return nil
}

func (p *Process) listening(ctx context.Context) (addresses, error) {
	select {
	case a := <-p.out.addrs:
		return a, nil
	case <-ctx.Done():
		return addresses{}, context.Cause(ctx)
	}
}

// terminate sends SIGTERM and returns the exit code.
func (p *Process) terminate() (int, error) {
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return 0, err
	}

	return p.waitExit()
}

// waitExit returns the exit code, -1 if a signal ended the process.
func (p *Process) waitExit() (int, error) {
	select {
	case <-p.exited:
		return p.cmd.ProcessState.ExitCode(), nil
	case <-time.After(stopTimeout):
		return 0, errors.New("it still runs after SIGTERM")
	}
}

// addresses are the bound addresses from the listening line.
type addresses struct {
	Msg    string `json:"msg"`
	Listen string `json:"listen_address"`
	Admin  string `json:"admin_listen_address"`
}

// local checks that the process listens where the harness told it to,
// which proves it read the variables.
func (a addresses) local() error {
	for env, addr := range map[string]string{
		"MINGEST_LISTEN_ADDRESS":       a.Listen,
		"MINGEST_ADMIN_LISTEN_ADDRESS": a.Admin,
	} {
		if !onLoopback(addr) {
			return fmt.Errorf(
				"%s was ignored: it listens on %q, not 127.0.0.1", env, addr,
			)
		}
	}

	return nil
}

// output keeps the process's log, and sends the addresses of the first
// listening line.
type output struct {
	mu      sync.Mutex
	log     bytes.Buffer
	partial []byte
	addrs   chan addresses
	found   bool
}

// Write keeps p and reads each complete line in it.
func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.log.Write(p)
	o.partial = append(o.partial, p...)

	for {
		line, rest, ok := bytes.Cut(o.partial, []byte("\n"))
		if !ok {
			return len(p), nil
		}

		o.line(line)
		o.partial = rest
	}
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.log.String()
}

func (o *output) line(l []byte) {
	var a addresses
	if json.Unmarshal(l, &a) != nil || a.Msg != "listening" || o.found {
		return
	}

	o.found = true
	o.addrs <- a
}

// Result is how a run of the binary ended.
type Result struct {
	// Code is the exit code.
	Code int

	// Output is what it wrote to stdout and stderr.
	Output string
}

// Run runs bin with args and the environment Start gives, then env, and
// waits for it to exit by itself, for at most runTimeout. It's for runs
// that end on their own: a failed startup, a usage error, a healthcheck.
func Run(t testing.TB, bin string, args []string, env ...string) Result {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	// bin is the binary Build made, and args come from the test.
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // above
	cmd.Env = append(baseEnv(t), env...)

	out, err := cmd.CombinedOutput()

	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("running %s: %v", bin, err)
	}

	return Result{Code: cmd.ProcessState.ExitCode(), Output: string(out)}
}

// Build compiles cmd/mingest into dir and returns the path of the binary.
//
// The binary is built with -trimpath. When the test binary itself runs under
// -race, the build adds -race too, so the process under test has the same
// race coverage as the tests driving it.
//
// The build runs from the repository root. A failed build returns the go
// toolchain's combined output in the error.
func Build(dir string) (string, error) {
	root, err := ModuleDir()
	if err != nil {
		return "", err
	}

	bin := filepath.Join(dir, "mingest")
	args := slices.Concat(
		[]string{"build", "-trimpath"}, raceFlags,
		[]string{"-o", bin, "./cmd/mingest"},
	)

	// The args are fixed here; only the output path varies.
	cmd := exec.CommandContext( //nolint:gosec // above
		context.Background(), "go", args...,
	)
	cmd.Dir = root

	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, out)
	}

	return bin, nil
}

func baseEnv(t testing.TB) []string {
	return []string{
		"MINGEST_DATA_DIR=" + t.TempDir(),
		"MINGEST_LISTEN_ADDRESS=127.0.0.1:0",
		"MINGEST_ADMIN_LISTEN_ADDRESS=127.0.0.1:0",
	}
}

func onLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)

	return err == nil && host == "127.0.0.1"
}
