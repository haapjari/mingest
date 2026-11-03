package harness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"mingest/test/dataset"
)

// waitInterval is how often WaitStatus reads the status.
const waitInterval = 50 * time.Millisecond

// Status is the IngestionStatus body, as far as the suites read it.
type Status struct {
	IngestionID   string `json:"ingestion_id"`
	State         string `json:"state"`
	MailFiles     int64  `json:"mail_files_processed"`
	Messages      int64  `json:"messages_processed"`
	Senders       int64  `json:"senders"`
	WithoutSender int64  `json:"messages_without_sender"`
	Resumes       int64  `json:"resumes"`
	Error         *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// Ended reports whether the Ingestion is done or failed.
func (s Status) Ended() bool {
	return s.State == "done" || s.State == "failed"
}

// TopSenders is the TopSenders body, as far as the suites read it.
type TopSenders struct {
	State   string              `json:"state"`
	Senders []dataset.TopSender `json:"top_senders"`
}

// WaitStatus reads GET url/status through c until until holds, and returns
// that status. The test fails if it doesn't hold within timeout.
func WaitStatus(
	t testing.TB,
	c *http.Client,
	url string,
	timeout time.Duration,
	until func(Status) bool,
) Status {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var s Status
		require.Equal(t, http.StatusOK,
			Send(t, c, http.MethodGet, url+"/status", &s))

		if until(s) {
			return s
		}

		time.Sleep(waitInterval)
	}

	t.Fatalf("the status didn't get there within %s", timeout)

	return Status{}
}

// Send sends a request without a body through c, decodes the JSON
// response into v unless v is nil, and returns the status code.
func Send(t testing.TB, c *http.Client, method, url string, v any) int {
	t.Helper()

	req, err := http.NewRequestWithContext(
		context.Background(), method, url, nil)
	require.NoError(t, err)

	resp, err := c.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	if v == nil {
		_, err = io.Copy(io.Discard, resp.Body)
	} else {
		err = json.NewDecoder(resp.Body).Decode(v)
	}

	require.NoError(t, err)

	return resp.StatusCode
}
