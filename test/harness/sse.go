package harness

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"uuid"
)

// Event is one status event of GET /status's event stream.
type Event struct {
	ID   string
	Data json.RawMessage
}

// ReadEvents reads an event stream to its end and checks its envelope:
// retry: 3000 first, then status events, closed after the first done or
// failed one.
func (c *Contract) ReadEvents(r io.Reader) ([]Event, error) {
	d := &decoder{contract: c, fields: map[string]string{}, revision: -1}

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if err := d.line(sc.Text()); err != nil {
			return nil, err
		}
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}

	return d.end()
}

// statusEvent checks an event's name and that its data is an
// IngestionStatus. advance checks its id.
func (c *Contract) statusEvent(fields map[string]string) (Event, error) {
	if fields["event"] != "status" {
		return Event{}, fmt.Errorf(
			"sse: event %q, want status",
			fields["event"],
		)
	}

	data := []byte(fields["data"])

	err := c.validateSchema("sse data", "IngestionStatus", data)
	if err != nil {
		return Event{}, err
	}

	return Event{ID: fields["id"], Data: data}, nil
}

// decoder holds the fields of the event being read and the events so far.
type decoder struct {
	contract *Contract
	fields   map[string]string
	events   []Event
	retried  bool
	revision int64
	terminal bool
}

// line takes one line of the stream. An empty line ends a block, and a line
// starting with a colon is a comment.
func (d *decoder) line(l string) error {
	if l == "" {
		return d.dispatch()
	}

	if !strings.HasPrefix(l, ":") {
		name, value, _ := strings.Cut(l, ":")
		d.fields[name] = strings.TrimPrefix(value, " ")
	}

	return nil
}

// dispatch ends the block being read.
func (d *decoder) dispatch() error {
	fields := d.fields
	d.fields = map[string]string{}

	if len(fields) == 0 {
		return nil
	}

	if d.terminal {
		return errors.New("sse: a block after the last event")
	}

	return d.block(fields)
}

// block takes a block of fields: the retry, or an event.
func (d *decoder) block(fields map[string]string) error {
	if _, ok := fields["retry"]; ok {
		return d.retry(fields)
	}

	return d.event(fields)
}

func (d *decoder) retry(fields map[string]string) error {
	if fields["retry"] != "3000" || len(fields) != 1 || d.retried {
		return fmt.Errorf(
			"sse: retry %q, want retry: 3000 once, before the first event",
			fields["retry"],
		)
	}

	d.retried = true

	return nil
}

func (d *decoder) event(fields map[string]string) error {
	if !d.retried {
		return errors.New("sse: an event before retry: 3000")
	}

	e, err := d.contract.statusEvent(fields)
	if err != nil {
		return err
	}

	if err := d.advance(e.ID); err != nil {
		return err
	}

	d.events = append(d.events, e)
	d.terminal = terminal(e.Data)

	return nil
}

// advance moves to the revision in id, which must be past the last one.
func (d *decoder) advance(id string) error {
	rev, err := parseEventID(id)
	if err != nil {
		return err
	}

	if rev <= d.revision {
		return fmt.Errorf("sse: revision %d after %d", rev, d.revision)
	}

	d.revision = rev

	return nil
}

// end returns the events once the stream is over, which must be right after
// the last event.
func (d *decoder) end() ([]Event, error) {
	if !d.terminal {
		return nil, errors.New("sse: the stream ended before the last event")
	}

	return d.events, nil
}

// parseEventID returns the revision in an event's id, <ingestion_id>:<revision>.
// The ingestion id is a UUID as the server writes it, lowercase with hyphens,
// and the revision a decimal number without a sign that fits an int64.
func parseEventID(id string) (int64, error) {
	ingestion, revision, _ := strings.Cut(id, ":")

	u, err := uuid.Parse(ingestion)
	if err != nil || u.String() != ingestion {
		return 0, fmt.Errorf("sse: id %q, want <ingestion_id>:<revision>", id)
	}

	rev, err := strconv.ParseUint(revision, 10, 63)
	if err != nil {
		return 0, fmt.Errorf("sse: id %q, want <ingestion_id>:<revision>", id)
	}

	return int64(rev), nil
}

// terminal reports whether the status in data is done or failed.
func terminal(data []byte) bool {
	var s struct {
		State string `json:"state"`
	}

	_ = json.Unmarshal(data, &s)

	return s.State == "done" || s.State == "failed"
}
