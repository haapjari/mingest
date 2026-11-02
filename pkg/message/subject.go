package message

import (
	"errors"
	"io"
	"mime"
	"strings"
	"unicode/utf8"
)

// errCharset is a charset the Subject's decoder doesn't know.
var errCharset = errors.New("unknown charset")

// charsets maps the labels of US-ASCII and ISO-8859-1 to their decoders:
// the IANA aliases and common misspellings, and the two names, which mime
// knows only exactly, for when padding or a language suffix comes with them.
var charsets = map[string]func(byte) rune{
	"ansi_x3.4-1968":   asciiRune,
	"ansi_x3.4-1986":   asciiRune,
	"ascii":            asciiRune,
	"cp367":            asciiRune,
	"csascii":          asciiRune,
	"ibm367":           asciiRune,
	"iso-ir-6":         asciiRune,
	"iso646-us":        asciiRune,
	"iso_646.irv:1991": asciiRune,
	"us":               asciiRune,
	"us-ascii":         asciiRune,

	"cp819":           latin1Rune,
	"csisolatin1":     latin1Rune,
	"ibm819":          latin1Rune,
	"iso-8859-1":      latin1Rune,
	"iso-ir-100":      latin1Rune,
	"iso8859-1":       latin1Rune,
	"iso_8859-1":      latin1Rune,
	"iso_8859-1:1987": latin1Rune,
	"l1":              latin1Rune,
	"latin1":          latin1Rune,
}

var subjectDecoder = mime.WordDecoder{CharsetReader: charsetReader}

// subject decodes the RFC 2047 words of v, or keeps v as written when a
// word won't decode. Then each run of whitespace becomes one space, and
// the ends are trimmed.
func subject(v []byte) string {
	s := string(v)
	if decoded, err := subjectDecoder.DecodeHeader(s); err == nil {
		s = decoded
	}

	return collapse(s)
}

// charsetReader decodes the charsets in charsets. The label is trimmed,
// compared case-insensitively, and an RFC 2231 language suffix is dropped.
func charsetReader(label string, input io.Reader) (io.Reader, error) {
	label, _, _ = strings.Cut(label, "*")

	toRune, ok := charsets[strings.ToLower(strings.TrimSpace(label))]
	if !ok {
		return nil, errCharset
	}

	b, err := io.ReadAll(input)

	out := make([]byte, 0, len(b))
	for _, c := range b {
		out = utf8.AppendRune(out, toRune(c))
	}

	return strings.NewReader(string(out)), err
}

// asciiRune turns a byte past 0x7F into U+FFFD, as mime does for US-ASCII.
func asciiRune(c byte) rune {
	if c >= utf8.RuneSelf {
		return utf8.RuneError
	}

	return rune(c)
}

func latin1Rune(c byte) rune {
	return rune(c)
}

// collapse turns each run of ASCII whitespace in s into one space and
// trims the ends.
func collapse(s string) string {
	return strings.Join(strings.FieldsFunc(s, isWhitespace), " ")
}

// isWhitespace reports whether r is ASCII whitespace: the body's, and LF.
func isWhitespace(r rune) bool {
	return r == '\n' || r < utf8.RuneSelf && isSpace[r]
}
