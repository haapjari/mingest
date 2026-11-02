package message

import (
	"bytes"
	"net/mail"
	"time"
)

// date returns the Date as RFC 3339 in UTC, or the trimmed raw value when
// it doesn't parse.
func date(v []byte) string {
	t, err := mail.ParseDate(string(v))
	if err != nil {
		return string(bytes.TrimSpace(v))
	}

	return t.UTC().Format(time.RFC3339)
}
