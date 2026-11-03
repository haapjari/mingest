package harness_test

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"

	"mingest/test/harness"
)

func TestParseEventID_RejectsOtherShapes(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"no revision", id},
		{"an empty revision", id + ":"},
		{"a signed revision", id + ":+1"},
		{"a negative revision", id + ":-1"},
		{"a revision past int64", id + ":9223372036854775808"},
		{"a revision with letters", id + ":1a"},
		{"no ingestion id", ":1"},
		{"an ingestion id that isn't a UUID", "not-a-uuid:1"},
		{"an uppercase UUID", strings.ToUpper(id) + ":1"},
		{"a UUID without hyphens", strings.ReplaceAll(id, "-", "") + ":1"},
		{"a UUID in braces", "{" + id + "}:1"},
		{"a UUID as a URN", "urn:uuid:" + id + ":1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := harness.ParseEventID(tt.id)

			assert.ErrorContains(t, err, "want <ingestion_id>:<revision>")
		})
	}
}

func TestContract_ReadEvents_FailsWhenStreamFails(t *testing.T) {
	_, err := newContract(t).ReadEvents(iotest.ErrReader(io.ErrClosedPipe))

	assert.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestContract_ReadEvents_RejectsBadStream(t *testing.T) {
	const retry = "retry: 3000\n\n"

	tests := []struct {
		name   string
		stream string
		want   string
	}{
		{
			"an id without a revision",
			retry + event(id, done),
			"id",
		},
		{
			"a revision that doesn't grow",
			retry + event(id+":2", running) + event(id+":2", done),
			"revision",
		},
		{
			"another event name",
			retry + "event: progress\nid: " + id + ":1\ndata: " + done + "\n\n",
			"progress",
		},
		{
			"data that isn't an Ingestion status",
			retry + event(id+":1", `{"state":"done"}`),
			"resumes",
		},
		{
			"no retry",
			event(id+":1", done),
			"retry",
		},
		{
			"another retry",
			"retry: 1000\n\n" + event(id+":1", done),
			`retry "1000"`,
		},
		{
			"a revision too large",
			retry + event(id+":99999999999999999999", done),
			"want <ingestion_id>:<revision>",
		},
		{
			"an event after the last",
			retry + event(id+":1", done) + event(id+":2", done),
			"after",
		},
		{
			"an end before the last event",
			retry + event(id+":1", running),
			"ended",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newContract(t).ReadEvents(strings.NewReader(tt.stream))

			assert.ErrorContains(t, err, tt.want)
		})
	}
}

const id = "01a19671-f0a0-7d41-aa5e-39c0b4f2d6e1"

const running = `{"ingestion_id":"` + id + `","state":"running",` +
	`"mail_files_processed":3,"messages_processed":2,"senders":1,` +
	`"messages_without_sender":0,"bytes_read":10,"bytes_total":20,` +
	`"progress_percent":50.0,"resumes":0,` +
	`"started_at":"2026-11-01T09:30:12Z",` +
	`"updated_at":"2026-11-01T09:30:13Z","finished_at":null,"error":null}`

const done = `{"ingestion_id":"` + id + `","state":"done",` +
	`"mail_files_processed":6,"messages_processed":4,"senders":2,` +
	`"messages_without_sender":0,"bytes_read":20,"bytes_total":20,` +
	`"progress_percent":100.0,"resumes":0,` +
	`"started_at":"2026-11-01T09:30:12Z",` +
	`"updated_at":"2026-11-01T09:30:14Z",` +
	`"finished_at":"2026-11-01T09:30:14Z","error":null}`

// event is one status event in the stream's framing.
func event(id, data string) string {
	return "event: status\nid: " + id + "\ndata: " + data + "\n\n"
}
