package harness

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
)

// Response is one documented response: an operation, a status and a media
// type, "" when the response has no content.
type Response struct {
	Operation   string
	Status      int
	ContentType string
}

// String names r in the coverage report.
func (r Response) String() string {
	return strings.TrimSpace(
		fmt.Sprintf("%s %d %s", r.Operation, r.Status, r.ContentType),
	)
}

// Check fails unless every documented response was seen by a Client, or is
// excluded because it can't be caused from outside, with its reason. It also
// fails on an excluded entry that isn't documented or has no reason.
func (c *Contract) Check(excluded map[Response]string) error {
	documented := c.documented()
	seen := c.seenResponses()

	problems := slices.Concat(
		stale("excluded", excluded, documented),
		unseen(documented, seen, excluded),
	)
	if len(problems) == 0 {
		return nil
	}

	slices.Sort(problems)

	return fmt.Errorf(
		"contract coverage:\n  %s", strings.Join(problems, "\n  "),
	)
}

// record notes the response to req as seen. A path the contract doesn't
// list has no documented response to count.
func (c *Contract) record(req *http.Request, resp *http.Response) {
	item := c.doc.Paths.PathItems.GetOrZero(req.URL.Path)
	if item == nil {
		return
	}

	r := Response{
		Operation:   listedOperation(item, req.Method).OperationId,
		Status:      resp.StatusCode,
		ContentType: mediaType(resp),
	}

	// record is called from concurrent requests, so guard the shared map.
	c.mu.Lock()
	defer c.mu.Unlock()

	c.seen[r] = ""
}

func (c *Contract) seenResponses() map[Response]string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return maps.Clone(c.seen)
}

// documented returns every response the contract documents.
func (c *Contract) documented() map[Response]string {
	out := map[Response]string{}

	// Collect every operation, status and content type the spec documents.
	for _, item := range c.doc.Paths.PathItems.FromOldest() {
		for _, op := range item.GetOperations().FromOldest() {
			addResponses(out, op)
		}
	}

	return out
}

func addResponses(out map[Response]string, op *v3.Operation) {
	for code, resp := range op.Responses.Codes.FromOldest() {
		status, _ := strconv.Atoi(code)

		for _, ct := range contentTypes(resp) {
			out[Response{op.OperationId, status, ct}] = ""
		}
	}
}

// contentTypes returns resp's media types, or "" when it has no content.
func contentTypes(resp *v3.Response) []string {
	if orderedmap.Len(resp.Content) == 0 {
		return []string{""}
	}

	return slices.Collect(resp.Content.KeysFromOldest())
}

// stale reports the entries of a list that aren't documented or have no
// reason.
func stale(list string, entries, documented map[Response]string) []string {
	var out []string

	for r, why := range entries {
		if _, ok := documented[r]; !ok {
			out = append(
				out,
				fmt.Sprintf("%s is %s but not documented", r, list),
			)
		}

		if why == "" {
			out = append(out, fmt.Sprintf("%s is %s without a reason", r, list))
		}
	}

	return out
}

func unseen(
	documented map[Response]string,
	accounted ...map[Response]string,
) []string {
	all := map[Response]string{}
	for _, m := range accounted {
		maps.Copy(all, m)
	}

	var out []string

	for r := range documented {
		if _, ok := all[r]; !ok {
			out = append(out, fmt.Sprintf("%s was never seen", r))
		}
	}

	return out
}
