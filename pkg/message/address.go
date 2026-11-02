package message

import (
	"net/mail"
	"slices"
	"strings"
)

// sender returns the first address of from, lowercased, or "" when there
// is no Sender: the address is empty or has no @. When the strict parse
// fails, the whole value is recovered as one address.
func sender(from []byte) string {
	addr := ""
	if list, err := mail.ParseAddressList(string(from)); err != nil {
		addr = recoverAddress(string(from))
	} else {
		addr = firstAddress(list)
	}

	if !strings.Contains(addr, "@") {
		return ""
	}

	return addr
}

// recoverAddress takes the address as written: the text inside the last
// <…> if there is one, otherwise the whole value, trimmed and with its
// ASCII lowercased. Text that parses as an address list on its own gives
// its first address, in canonical form, as the strict parse would.
func recoverAddress(v string) string {
	v = strings.TrimSpace(inAngles(v))
	if list, err := mail.ParseAddressList(v); err == nil {
		return firstAddress(list)
	}

	return lower(v)
}

// inAngles returns the text inside the last <…> of v, or v when there is
// none.
func inAngles(v string) string {
	open := strings.LastIndexByte(v, '<')
	if open < 0 {
		return v
	}

	inside, _, closed := strings.Cut(v[open+1:], ">")
	if !closed {
		return v
	}

	return inside
}

// firstAddress returns the first address of a parsed list in canonical
// form, or "" when the list is empty, as an empty group is.
func firstAddress(list []*mail.Address) string {
	if len(list) == 0 {
		return ""
	}

	return canonical(list[0])
}

// canonical returns a parsed address lowercased, as an addr-spec whose
// local part is quoted only when it has to be. Parsing that form again
// gives the same address.
func canonical(a *mail.Address) string {
	spec := (&mail.Address{Address: lower(a.Address)}).String()

	return strings.TrimSuffix(strings.TrimPrefix(spec, "<"), ">")
}

// lower lowercases the ASCII letters of s and leaves every other byte as
// written, invalid UTF-8 included.
func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}

	return string(b)
}

// recipients returns the address list v as a sorted set of lowercased
// addresses. When the strict parse fails, each element between commas is
// recovered as written. A truncated list loses its last element, which
// the cut may have changed.
func recipients(v []byte, truncated bool) []string {
	addrs := elements(string(v))
	if truncated && len(addrs) > 0 {
		addrs = addrs[:len(addrs)-1]
	}

	addrs = slices.DeleteFunc(addrs, func(a string) bool { return a == "" })
	slices.Sort(addrs)

	return slices.Compact(addrs)
}

// elements returns each address of the list v in order, lowercased.
func elements(v string) []string {
	list, err := mail.ParseAddressList(v)
	if err != nil {
		parts := splitList(v)
		for i, p := range parts {
			parts[i] = recoverAddress(p)
		}

		return parts
	}

	addrs := make([]string, len(list))
	for i, a := range list {
		addrs[i] = canonical(a)
	}

	return addrs
}

// splitList splits v at the commas outside quotes and angle brackets. In a
// quoted string, a backslash escapes the next byte.
func splitList(v string) []string {
	var (
		parts                  []string
		quoted, escaped, angle bool
		start                  int
	)

	for i := range len(v) {
		c := v[i]
		if quoted {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				quoted = false
			}

			continue
		}

		switch c {
		case '"':
			quoted = true
		case '<', '>':
			angle = c == '<'
		case ',':
			if !angle {
				parts = append(parts, v[start:i])
				start = i + 1
			}
		}
	}

	return append(parts, v[start:])
}
