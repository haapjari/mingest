package server

import (
	"strconv"
	"time"

	"mingest/pkg/ingest"
)

// ingestionStatus is the IngestionStatus body of openapi.yaml.
type ingestionStatus struct {
	IngestionID           *string         `json:"ingestion_id"`
	State                 ingest.State    `json:"state"`
	MailFilesProcessed    int64           `json:"mail_files_processed"`
	MessagesProcessed     int64           `json:"messages_processed"`
	Senders               int64           `json:"senders"`
	MessagesWithoutSender int64           `json:"messages_without_sender"`
	BytesRead             int64           `json:"bytes_read"`
	BytesTotal            *int64          `json:"bytes_total"`
	ProgressPercent       *progress       `json:"progress_percent"`
	Resumes               int64           `json:"resumes"`
	StartedAt             *time.Time      `json:"started_at"`
	UpdatedAt             *time.Time      `json:"updated_at"`
	FinishedAt            *time.Time      `json:"finished_at"`
	Error                 *ingestionError `json:"error"`
}

// ingestionError is the IngestionError body: why an Ingestion failed.
type ingestionError struct {
	Code    ingest.Code `json:"code"`
	Message string      `json:"message"`
}

// progress is a percentage in tenths, written with one decimal, so 100
// reads as 100.0.
type progress int64

// MarshalJSON writes the percentage as a number with one decimal.
func (p progress) MarshalJSON() ([]byte, error) {
	return strconv.AppendFloat(nil, float64(p)/10, 'f', 1, 64), nil
}

// toWire maps a Status to its body. While idle there is no Ingestion, so
// every field but the state is null or zero.
func toWire(st ingest.Status) ingestionStatus {
	if st.State == ingest.StateIdle {
		return ingestionStatus{State: ingest.StateIdle}
	}

	return ingestionStatus{
		IngestionID:           &st.ID,
		State:                 st.State,
		MailFilesProcessed:    st.MailFiles,
		MessagesProcessed:     st.Messages,
		Senders:               st.Senders,
		MessagesWithoutSender: st.MessagesWithoutSender,
		BytesRead:             st.BytesRead,
		BytesTotal:            bytesTotal(st.BytesTotal),
		ProgressPercent:       progressOf(st.BytesRead, st.BytesTotal),
		Resumes:               st.Resumes,
		StartedAt:             timeOf(st.StartedAt),
		UpdatedAt:             timeOf(st.UpdatedAt),
		FinishedAt:            timeOf(st.FinishedAt),
		Error:                 errorOf(st),
	}
}

// bytesTotal is null when the Source's size is unknown.
func bytesTotal(n int64) *int64 {
	if n < 0 {
		return nil
	}

	return &n
}

// progressOf is read of total in tenths of a percent, rounded down, so
// 100.0 only ever means every byte was read. It's null without a total.
func progressOf(read, total int64) *progress {
	if total <= 0 {
		return nil
	}

	p := progress(min(read*1000/total, 1000))

	return &p
}

// timeOf is t in UTC, or null when it isn't set.
func timeOf(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	t = t.UTC()

	return &t
}

// errorOf is why the Ingestion failed, or null unless it did.
func errorOf(st ingest.Status) *ingestionError {
	if st.State != ingest.StateFailed {
		return nil
	}

	return &ingestionError{Code: st.Error.Code, Message: st.Error.Message}
}

// topSenders is the TopSenders body of openapi.yaml.
type topSenders struct {
	IngestionID *string      `json:"ingestion_id"`
	State       ingest.State `json:"state"`
	UpdatedAt   *time.Time   `json:"updated_at"`
	TopSenders  []topSender  `json:"top_senders"`
}

type topSender struct {
	Sender   string `json:"sender"`
	Messages int64  `json:"messages"`
}

// topSendersToWire maps the Top Senders to their body, in the Store's
// order. The list is never null: while idle it's empty.
func topSendersToWire(ts ingest.TopSenders) topSenders {
	out := topSenders{
		State:      ts.Status.State,
		TopSenders: make([]topSender, 0, len(ts.Senders)),
	}

	for _, s := range ts.Senders {
		out.TopSenders = append(out.TopSenders, topSender(s))
	}

	if ts.Status.State != ingest.StateIdle {
		out.IngestionID = &ts.Status.ID
		out.UpdatedAt = timeOf(ts.Status.UpdatedAt)
	}

	return out
}
