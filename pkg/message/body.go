package message

import "hash"

// isSpace is the body's whitespace: space, tab, CR, VT and FF.
var isSpace = [256]bool{
	' ':  true,
	'\t': true,
	'\r': true,
	'\v': true,
	'\f': true,
}

// space is the one space a run of whitespace in a line becomes.
var space = []byte{' '}

// newline is written once for each pending line break.
var newline = []byte{'\n'}

// normalizer writes the body to the hash with each line trimmed, each run
// of whitespace in a line as one space, and the blank lines at the end
// dropped. It holds no part of the body: line breaks and spaces are counted
// and written only when text follows them.
type normalizer struct {
	hash hash.Hash

	// breaks is the number of line breaks not yet written.
	breaks int

	// space is set by whitespace after text on the current line.
	space bool

	// text is set once the current line has text.
	text bool
}

// write takes the next fragment of the body. A fragment may end anywhere,
// even inside a word, so a word split across two calls is hashed the same as
// one that arrives whole. For example, the body "a  b\n" written in three
// calls as "a ", " b" and "\n" is hashed as "a b", exactly as if it had been
// written in one call as "a  b\n".
func (n *normalizer) write(b []byte) {
	for len(b) > 0 {
		// A word is preceded by its gap: the pending line breaks, or one
		// space after text on the same line.
		if w := wordLen(b); w > 0 {
			for ; n.breaks > 0; n.breaks-- {
				n.hash.Write(newline)
			}

			if n.space {
				n.hash.Write(space)
			}

			n.hash.Write(b[:w])
			n.text, n.space = true, false
			b = b[w:]

			continue
		}

		if b[0] == '\n' {
			n.breaks++
			n.text = false
		}

		n.space = n.text
		b = b[1:]
	}
}

// wordLen returns the length of the text before the first whitespace or
// line break.
func wordLen(b []byte) int {
	for i, c := range b {
		if isSpace[c] || c == '\n' {
			return i
		}
	}

	return len(b)
}
