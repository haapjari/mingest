//go:build acceptance

package acceptance_test

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/dataset"
	"mingest/test/harness"
)

// The expected counts for enron_mail_20150507.tar.gz (sha256 b3da1b3f…48ca7)
// under the Message rules.
const (
	wantMailFiles     = 517_401
	wantMessages      = 254_791
	wantSenders       = 20_325
	wantWithoutSender = 1
)

// wantTopSenders are the ten Senders with the most Messages, by Messages
// descending and then address ascending.
var wantTopSenders = []dataset.TopSender{
	{Sender: "jeff.dasovich@enron.com", Messages: 5_530},
	{Sender: "kay.mann@enron.com", Messages: 4_887},
	{Sender: "sara.shackleton@enron.com", Messages: 4_449},
	{Sender: "tana.jones@enron.com", Messages: 4_171},
	{Sender: "vince.kaminski@enron.com", Messages: 3_975},
	{Sender: "pete.davis@enron.com", Messages: 3_933},
	{Sender: "chris.germany@enron.com", Messages: 3_638},
	{Sender: "matthew.lenhart@enron.com", Messages: 2_781},
	{Sender: "debra.perlingiere@enron.com", Messages: 2_447},
	{Sender: "gerald.nemec@enron.com", Messages: 2_231},
}

// ingestTimeout bounds the whole Ingestion of the Dataset.
const ingestTimeout = 30 * time.Minute

func TestIngestion_MatchesKnownCounts(t *testing.T) {
	path := os.Getenv("TEST_DATASET")
	require.NotEmpty(t, path, "TEST_DATASET names the Dataset; "+
		"run make test-acceptance")

	p := harness.Start(t, bin, "MINGEST_DATASET_SOURCE=file://"+path)

	require.Equal(
		t,
		http.StatusAccepted,
		harness.Send(
			t,
			contract.Client(),
			http.MethodPost,
			p.URL+"/start",
			nil,
		),
	)

	s := harness.WaitStatus(t, contract.Client(), p.URL, ingestTimeout,
		harness.Status.Ended)
	require.Equal(t, "done", s.State, p.Log())
	assert.Equal(t, int64(wantMailFiles), s.MailFiles)
	assert.Equal(t, int64(wantMessages), s.Messages)
	assert.Equal(t, int64(wantSenders), s.Senders)
	assert.Equal(t, int64(wantWithoutSender), s.WithoutSender)

	var top harness.TopSenders
	require.Equal(
		t,
		http.StatusOK,
		harness.Send(
			t,
			contract.Client(),
			http.MethodGet,
			p.URL+"/top-senders",
			&top,
		),
	)
	assert.Equal(t, "done", top.State)
	assert.Equal(t, wantTopSenders, top.Senders)

	p.Stop(t)
}
