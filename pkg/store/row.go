package store

import (
	"database/sql"
	"errors"
	"time"

	"mingest/pkg/ingest"
)

// timeFormat keeps nanoseconds, and sorts as text.
const timeFormat = time.RFC3339Nano

// progressRow holds the progress row as SQLite returns it.
type progressRow struct {
	id, state, url                 string
	size                           sql.NullInt64
	version                        sql.NullString
	mailFiles, messages, senders   int64
	withoutSender, bytesRead       int64
	resumes, revision              int64
	startedAt, updatedAt           string
	finishedAt, code, errorMessage sql.NullString
}

func scanStatus(row *sql.Row) (ingest.Status, error) {
	var r progressRow

	err := row.Scan(
		&r.id, &r.state, &r.url, &r.size,
		&r.version, &r.mailFiles, &r.messages, &r.senders, &r.withoutSender,
		&r.bytesRead, &r.resumes, &r.revision, &r.startedAt, &r.updatedAt,
		&r.finishedAt, &r.code, &r.errorMessage,
	)
	if err != nil {
		return ingest.Status{}, err
	}

	return r.status()
}

func (r *progressRow) status() (ingest.Status, error) {
	startedAt, startedAtErr := time.Parse(timeFormat, r.startedAt)
	updatedAt, updatedAtErr := time.Parse(timeFormat, r.updatedAt)
	var finishedAt time.Time

	var finishedAtErr error
	if r.finishedAt.Valid {
		finishedAt, finishedAtErr = time.Parse(timeFormat, r.finishedAt.String)
	}

	// An unknown size is NULL, and -1 in the Status.
	size := int64(-1)
	if r.size.Valid {
		size = r.size.Int64
	}

	if err := errors.Join(
		startedAtErr,
		updatedAtErr,
		finishedAtErr,
	); err != nil {
		return ingest.Status{}, err
	}

	return ingest.Status{
		ID:                    r.id,
		State:                 ingest.State(r.state),
		MailFiles:             r.mailFiles,
		Messages:              r.messages,
		Senders:               r.senders,
		MessagesWithoutSender: r.withoutSender,
		BytesRead:             r.bytesRead,
		BytesTotal:            size,
		Resumes:               r.resumes,
		Revision:              r.revision,
		StartedAt:             startedAt,
		UpdatedAt:             updatedAt,
		FinishedAt:            finishedAt,
		Error: ingest.Failure{
			Code:    ingest.Code(r.code.String),
			Message: r.errorMessage.String,
		},
		SourceURL:     r.url,
		SourceVersion: r.version.String,
	}, nil
}

// nullSize stores an unknown size, -1, as NULL.
func nullSize(n int64) sql.NullInt64 {
	return sql.NullInt64{Int64: n, Valid: n >= 0}
}

// nullString stores "" as NULL.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func formatTime(t time.Time) string {
	return t.UTC().Format(timeFormat)
}
