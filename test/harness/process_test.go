package harness_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/harness"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("HARNESS_HELPER"); mode != "" {
		fakeMingest(mode)
	}

	os.Exit(m.Run())
}

func TestBuild_FailsWhereItCannotWrite(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	_, err := harness.Build(file)

	assert.ErrorContains(t, err, "go build")
}

func TestStart_FailsWhenListenAddressIsIgnored(t *testing.T) {
	ft := &fakeTB{TB: t}

	ft.run(func() { harness.Start(ft, self(t), "HARNESS_HELPER=deaf") })

	assert.True(t, ft.Failed())
	assert.Contains(t, ft.output(), "MINGEST_LISTEN_ADDRESS")
}

func TestStart_FailsOnMissingBinary(t *testing.T) {
	ft := &fakeTB{TB: t}

	ft.run(func() {
		harness.Start(ft, filepath.Join(t.TempDir(), "none"))
	})

	assert.True(t, ft.Failed())
	assert.Contains(t, ft.output(), "no such file")
}

func TestStart_ReadsOnlyFirstListeningLine(t *testing.T) {
	p := harness.Start(t, self(t), "HARNESS_HELPER=chatty")

	assert.Equal(t, "ok", read(t, p.URL+"/"))

	p.Stop(t)
}

func TestProcess_Stop_FailsOnUncleanExit(t *testing.T) {
	ft := &fakeTB{TB: t}

	ft.run(func() {
		harness.Start(ft, self(t), "HARNESS_HELPER=unclean").Stop(ft)
	})

	assert.True(t, ft.Failed())
	assert.Contains(t, ft.output(), "exited with code 1")
}

func TestProcess_Stop_FailsOnEndedProcess(t *testing.T) {
	ft := &fakeTB{TB: t}

	ft.run(func() {
		p := harness.Start(ft, self(t), "HARNESS_HELPER=serve")
		p.Kill(ft)
		p.Stop(ft)
	})

	assert.True(t, ft.Failed())
	assert.Contains(t, ft.output(), "process already finished")
}

func TestProcess_State_IsNilUntilExit(t *testing.T) {
	p := harness.Start(t, self(t), "HARNESS_HELPER=serve")
	assert.Positive(t, p.Pid())
	assert.Nil(t, p.State())

	p.Stop(t)

	require.NotNil(t, p.State())
	assert.True(t, p.State().Success())
	assert.Equal(t, p.Pid(), p.State().Pid())
}

func TestStart_ShowsLogWhenProcessExits(t *testing.T) {
	ft := &fakeTB{TB: t}

	ft.run(func() { harness.Start(ft, self(t), "HARNESS_HELPER=exit") })

	assert.True(t, ft.Failed())
	assert.Contains(t, ft.output(), "exit status 3")
	assert.Contains(t, ft.output(), "startup failed")
}

// fakeMingest stands in for the binary. It logs the listening line and
// serves until SIGTERM, then exits 0. Modes:
//   - unclean: exits 1 instead of 0
//   - chatty: logs another line before the listening line and repeats it
//   - deaf: ignores MINGEST_LISTEN_ADDRESS
//   - exit: fails at startup
func fakeMingest(mode string) {
	if mode == "exit" {
		fmt.Println(`{"level":"ERROR","msg":"startup failed"}`)
		os.Exit(3)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	logListening(mode)

	<-ctx.Done()

	// Exit 1 for unclean, so the harness sees a non-zero code after the
	// SIGTERM; every other mode exits 0, as the map lookup yields zero.
	os.Exit(map[string]int{"unclean": 1}[mode])
}

// logListening binds both addresses and logs the listening line.
func logListening(mode string) {
	public := listen(publicAddress(mode), publicMux())
	admin := listen(os.Getenv("MINGEST_ADMIN_LISTEN_ADDRESS"), adminMux())

	line, _ := json.Marshal(map[string]string{
		"level": "INFO", "msg": "listening",
		"listen_address": public, "admin_listen_address": admin,
	})
	if mode == "chatty" {
		fmt.Println(`{"level":"INFO","msg":"starting"}`)
		fmt.Println(string(line))
	}

	fmt.Println(string(line))
}

// publicAddress is MINGEST_LISTEN_ADDRESS, which deaf ignores for a
// default of all interfaces.
func publicAddress(mode string) string {
	if mode == "deaf" {
		return ":0"
	}

	return os.Getenv("MINGEST_LISTEN_ADDRESS")
}

func listen(address string, h http.Handler) string {
	l, err := (&net.ListenConfig{}).Listen(
		context.Background(), "tcp", address,
	)
	if err != nil {
		panic(err)
	}

	go func() { _ = http.Serve(l, h) }() //nolint:gosec // a test stand-in

	return l.Addr().String()
}

func publicMux() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})

	// A test stand-in that echoes its own environment as plain text.
	mux.HandleFunc("/env", func(w http.ResponseWriter, r *http.Request) {
		value := os.Getenv(r.URL.Query().Get("name"))

		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, value) //nolint:gosec // test stand-in
	})

	return mux
}

func adminMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "admin")
	})

	return mux
}

// self is the test binary, run again as fakeMingest.
func self(t *testing.T) string {
	t.Helper()

	bin, err := os.Executable()
	require.NoError(t, err)

	return bin
}

func read(t *testing.T, url string) string {
	t.Helper()

	resp, err := request(t, http.DefaultClient, url)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return string(body)
}

// fakeTB records failures and logs instead of failing the real test. Its
// Fatal ends the goroutine run started, as testing's does.
type fakeTB struct {
	// Embedded so fakeTB satisfies testing.TB. Methods not overridden
	// below panic on the nil value.
	testing.TB

	mu       sync.Mutex // guards the fields below
	failed   bool       // set by Errorf and Fatal
	logs     []string   // messages captured by Logf
	cleanups []func()   // registered by Cleanup, run manually by the test
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Cleanup(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.cleanups = append(f.cleanups, fn)
}

func (f *fakeTB) Failed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.failed
}

func (f *fakeTB) Logf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.Logf(format, args...)

	f.mu.Lock()
	defer f.mu.Unlock()

	f.failed = true
}

func (f *fakeTB) Fatal(args ...any) {
	f.Errorf("%s", fmt.Sprint(args...))
	runtime.Goexit()
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.Errorf(format, args...)
	runtime.Goexit()
}

// run executes fn on a separate goroutine and then runs the registered
// cleanups.
//
// The separate goroutine lets Fatal and Fatalf stop fn early through
// runtime.Goexit without ending the calling test. Cleanups run after fn has
// finished, in last-in, first-out order, just like testing.T.
func (f *fakeTB) run(fn func()) {
	done := make(chan struct{})

	go func() {
		defer close(done)

		fn()
	}()

	<-done

	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

func (f *fakeTB) output() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return strings.Join(f.logs, "\n")
}
