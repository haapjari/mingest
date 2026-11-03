package harness_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"mingest/test/harness"
)

func TestContract_Check_FailsOnResponseNeverSeen(t *testing.T) {
	excluded := documented()
	delete(excluded, topSenders)

	err := newContract(t).Check(excluded)

	assert.ErrorContains(t, err,
		"getTopSenders 200 application/json was never seen")
}

func TestContract_Check_ListsProblemsInOrder(t *testing.T) {
	excluded := documented()
	delete(excluded, topSenders405)
	delete(excluded, status405)
	delete(excluded, topSenders)

	err := newContract(t).Check(excluded)

	assert.EqualError(t, err, "contract coverage:\n"+
		"  getStatus 405 application/problem+json was never seen\n"+
		"  getTopSenders 200 application/json was never seen\n"+
		"  getTopSenders 405 application/problem+json was never seen")
}

func TestContract_Check_Counts405OnUnlistedMethod(t *testing.T) {
	c := newContract(t)
	url := stubHeader(t, http.StatusMethodNotAllowed, problemHeader("GET"),
		methodNotAllowed)
	excluded := documented()
	delete(excluded, topSenders405)

	assert.NoError(t, do(t, c.Client(), http.MethodDelete,
		url+"/top-senders"))

	assert.NoError(t, c.Check(excluded))
}

func TestContract_Check_FailsOnStaleEntry(t *testing.T) {
	tests := []struct {
		name  string
		entry harness.Response
		why   string
		want  string
	}{
		{
			"undocumented",
			harness.Response{Operation: "getTopSenders", Status: 418},
			"a reason",
			"getTopSenders 418 is excluded but not documented",
		},
		{
			"without a reason",
			topSenders,
			"",
			"getTopSenders 200 application/json is excluded without a reason",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			excluded := documented()
			delete(excluded, tt.entry)

			excluded[tt.entry] = tt.why

			err := newContract(t).Check(excluded)

			assert.ErrorContains(t, err, tt.want)
		})
	}
}

var (
	topSenders = harness.Response{
		Operation: "getTopSenders", Status: 200,
		ContentType: "application/json",
	}
	topSenders405 = harness.Response{
		Operation: "getTopSenders", Status: 405, ContentType: problem,
	}
	status405 = harness.Response{
		Operation: "getStatus", Status: 405, ContentType: problem,
	}
)

const problem = "application/problem+json"

// documented returns every response docs/openapi.yaml documents, written out
// by hand, each pending with a placeholder ticket.
func documented() map[harness.Response]string {
	all := []harness.Response{
		{
			Operation:   "startIngestion",
			Status:      202,
			ContentType: "application/json",
		},
		{Operation: "startIngestion", Status: 405, ContentType: problem},
		{Operation: "startIngestion", Status: 409, ContentType: problem},
		{Operation: "startIngestion", Status: 500, ContentType: problem},
		{Operation: "getStatus", Status: 200, ContentType: "application/json"},
		{Operation: "getStatus", Status: 200, ContentType: "text/event-stream"},
		{Operation: "getStatus", Status: 204},
		status405,
		{Operation: "getStatus", Status: 406, ContentType: problem},
		{Operation: "getStatus", Status: 500, ContentType: problem},
		{Operation: "getStatus", Status: 503, ContentType: problem},
		topSenders,
		topSenders405,
		{Operation: "getTopSenders", Status: 500, ContentType: problem},
	}

	out := make(map[harness.Response]string, len(all))
	for _, r := range all {
		out[r] = "a ticket"
	}

	return out
}
