package harness

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Setup builds the binary into a new temporary directory and loads the
// contract, once in a suite's TestMain. cleanup removes the directory.
func Setup() (bin string, contract *Contract, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "mingest-")
	if err != nil {
		return "", nil, nil, err
	}

	cleanup = func() { _ = os.RemoveAll(dir) }

	if bin, err = Build(dir); err == nil {
		contract, err = OpenAPI()
	}

	if err != nil {
		cleanup()

		return "", nil, nil, err
	}

	return bin, contract, cleanup, nil
}

// OpenAPI loads the service's contract, docs/openapi.yaml.
func OpenAPI() (*Contract, error) {
	root, err := ModuleDir()
	if err != nil {
		return nil, err
	}

	return LoadContract(filepath.Join(root, "docs", "openapi.yaml"))
}

// ServeTLS serves h over HTTPS until the test ends. It returns the
// environment that points the binary at h as its Dataset and makes it trust
// the test certificate through SSL_CERT_FILE. Go ignores SSL_CERT_FILE on
// macOS, so there the test is skipped.
func ServeTLS(t *testing.T, h http.Handler) []string {
	t.Helper()

	if runtime.GOOS == "darwin" {
		t.Skip("Go ignores SSL_CERT_FILE on macOS, so the binary can't " +
			"trust a test HTTPS source")
	}

	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)

	cert := filepath.Join(t.TempDir(), "cert.pem")

	err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: srv.Certificate().Raw,
	}), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return []string{
		"MINGEST_DATASET_SOURCE=" + srv.URL + "/dataset.tar.gz",
		"SSL_CERT_FILE=" + cert,
	}
}
