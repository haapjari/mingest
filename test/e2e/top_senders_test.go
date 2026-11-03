//go:build e2e

package e2e_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/dataset"
	"mingest/test/harness"
)

func TestTopSenders_ListsTheFixtureAtDone(t *testing.T) {
	t.Parallel()

	p := start(t)
	do(t, http.MethodPost, p.URL+"/start")
	waitEnded(t, p.URL)

	r := get(t, p.URL+"/top-senders")
	assert.Equal(t, "no-store", r.header.Get("Cache-Control"))

	top := topOf(t, r)
	assert.Equal(t, "done", top.State)
	assert.Equal(t, dataset.FixtureTopSenders, top.Senders,
		"the 10th place ties and goes to the lower address")

	post := do(t, http.MethodPost, p.URL+"/top-senders")
	assert.Equal(t, http.StatusMethodNotAllowed, post.status)
	assert.Equal(t, "GET", post.header.Get("Allow"))
}

func TestTopSenders_ListsTheLastCommitWhileRunning(t *testing.T) {
	t.Parallel()

	p := start(t,
		serveSlowly(t, dataset.Generate(generated(1)), generatedStep).env...)
	do(t, http.MethodPost, p.URL+"/start")
	waitCommitted(t, p.URL)

	running := topOf(t, get(t, p.URL+"/top-senders"))
	require.Equal(t, "running", running.State)
	require.NotEmpty(t, running.Senders)

	waitEnded(t, p.URL)
	final := topOf(t, get(t, p.URL+"/top-senders"))
	require.Len(t, final.Senders, 10)

	// A sender not in the final list ends with no more than its 10th place.
	counts := map[string]int{}
	for _, s := range final.Senders {
		counts[s.Sender] = s.Messages
	}

	for _, s := range running.Senders {
		most, ok := counts[s.Sender]
		if !ok {
			most = final.Senders[len(final.Senders)-1].Messages
		}

		assert.LessOrEqual(t, s.Messages, most, s.Sender)
	}
}

func topOf(t *testing.T, r reply) harness.TopSenders {
	t.Helper()

	var top harness.TopSenders
	require.NoError(t, json.Unmarshal([]byte(r.body), &top))

	return top
}
