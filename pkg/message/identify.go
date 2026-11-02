package message

import (
	"crypto/sha256"
	"encoding/binary"
)

// Identity is what Identify finds in one Mail file: the Message it holds
// and the Message's Sender.
type Identity struct {
	// Fingerprint identifies the Message. Copies of one email share it.
	Fingerprint [16]byte

	// Sender is the first From address, lowercased, or "" when the Mail
	// file has no Sender.
	Sender string
}

// Identify applies the Message rules to a scanned Mail file.
func Identify(s Scanned) Identity {
	var id Identity
	id.Sender = sender(s.From)

	var f fingerprint
	f.value(id.Sender)
	f.value(date(s.Date))
	f.value(subject(s.Subject))
	f.set(recipients(s.To, s.Truncated.Has(To)))
	f.set(recipients(s.Cc, s.Truncated.Has(Cc)))
	f.value(string(s.Body[:]))
	id.Fingerprint = f.sum()

	return id
}

// fingerprint encodes the Message's values unambiguously: each value is
// its length as a uvarint followed by its bytes, and each set is its count
// as a uvarint followed by its values.
type fingerprint struct {
	buf []byte
}

func (f *fingerprint) value(v string) {
	f.buf = binary.AppendUvarint(f.buf, uint64(len(v)))
	f.buf = append(f.buf, v...)
}

func (f *fingerprint) set(vs []string) {
	f.buf = binary.AppendUvarint(f.buf, uint64(len(vs)))
	for _, v := range vs {
		f.value(v)
	}
}

// sum returns the first 16 bytes of the encoding's SHA-256.
func (f *fingerprint) sum() [16]byte {
	full := sha256.Sum256(f.buf)

	return [16]byte(full[:16])
}
