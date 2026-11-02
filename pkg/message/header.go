package message

import "bytes"

// lineKind is what the rest of the current header line is.
type lineKind uint8

const (
	// lineSkip is a line whose bytes are dropped: an unkept field, its
	// continuations, and a line with no colon.
	lineSkip lineKind = iota

	// linePending is a line whose colon hasn't come yet.
	linePending

	// lineValue is a line whose bytes go to the current kept field.
	lineValue
)

// header holds the header line rules' state. A line can come in several
// fragments, so it never needs to be held whole.
type header struct {
	values    [len(fieldNames)][]byte
	seen      Fields
	truncated Fields

	// cur is the index of the kept field that continuation lines go to,
	// or -1.
	cur  int
	kind lineKind
	name fieldName

	// start is set when the next fragment begins a line.
	start bool

	// lead is set while the whitespace after the colon is being dropped.
	lead bool

	// cr holds a CR that ended the previous fragment, kept only if the
	// line doesn't end right after it.
	cr bool
}

// line takes the next fragment of the header and reports whether it was the
// empty line that ends the header.
func (h *header) line(frag []byte) bool {
	if h.start {
		if isEmptyLine(frag) {
			return true
		}

		// A line that starts with a space or tab continues the current
		// field, or is skipped when no kept field is open.
		h.kind, h.name, h.cr = linePending, fieldName{}, false
		if frag[0] == ' ' || frag[0] == '\t' {
			h.kind = lineSkip
			if h.cur >= 0 {
				h.kind = lineValue
			}
		}
	}

	text, eol := bytes.CutSuffix(frag, []byte{'\n'})

	switch h.kind {
	case linePending:
		// A line is read up to its colon. A line that ends first is
		// skipped, and the current field goes on.
		name, rest, found := bytes.Cut(text, []byte{':'})
		h.name.add(name)

		if found && h.startField() {
			h.value(rest)
		}
	case lineValue:
		h.value(text)
	}

	h.start = eol

	return false
}

// startField ends the current field and reports whether the new one is
// kept: one of the five, seen for the first time.
func (h *header) startField() bool {
	// Whatever field was open is over. Until a new one is accepted, the
	// following lines are skipped.
	h.cur, h.kind = -1, lineSkip

	// index is negative when the name is not one of the five.
	i := h.name.index()
	if i < 0 || h.seen.Has(fieldBit(i)) {
		// Unknown name, or a repeat: the first occurrence wins.
		return false
	}

	// Accept the field. lead is set so the whitespace after the colon is
	// trimmed from the start of the value.
	h.cur, h.kind, h.lead = i, lineValue, true

	// Mark the field as seen: |= ORs the field's bit into the seen set, so
	// the bit is on from now on and the other bits are unchanged.
	h.seen |= fieldBit(i)

	return true
}

func (h *header) value(text []byte) {
	if h.lead {
		text = bytes.TrimLeft(text, " \t")
		h.lead = len(text) == 0
	}

	if h.cr && len(text) > 0 {
		h.keep([]byte{'\r'})
	}

	text, h.cr = bytes.CutSuffix(text, []byte{'\r'})
	h.keep(text)
}

// keep appends to the current field up to FieldCap, and marks it truncated
// if bytes had to be dropped.
func (h *header) keep(b []byte) {
	v := &h.values[h.cur]
	if room := FieldCap - len(*v); len(b) > room {
		b = b[:room]
		h.truncated |= fieldBit(h.cur)
	}

	*v = append(*v, b...)
}

// fieldName collects a field name, trimmed, up to the longest kept name.
type fieldName struct {
	buf [len("subject")]byte
	len int

	// gap is set by whitespace after the name has begun.
	gap bool

	// other is set once the name can't be a kept one.
	other bool
}

func (n *fieldName) add(b []byte) {
	for _, c := range b {
		switch {
		case isSpace[c]:
			n.gap = n.len > 0
		case n.gap || n.len == len(n.buf):
			n.other = true
		default:
			n.buf[n.len] = c
			n.len++
		}
	}
}

// index returns the kept field the name is, compared case-insensitively,
// or -1.
func (n *fieldName) index() int {
	if n.other {
		return -1
	}

	for i, name := range fieldNames {
		if bytes.EqualFold(n.buf[:n.len], []byte(name)) {
			return i
		}
	}

	return -1
}

// isEmptyLine reports whether a fragment that begins a line is an empty
// line. The empty fragment is the end of the Mail file.
func isEmptyLine(frag []byte) bool {
	switch string(frag) {
	case "", "\n", "\r\n":
		return true
	}

	return false
}
