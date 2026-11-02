package message_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/pkg/message"
)

func TestScanner_Scan_FollowsLineRules(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   string
		want fields
	}{
		"lf": {
			in:   "From: f\nDate: d\nSubject: s\nTo: t\nCc: c\n\nbody\n",
			want: fields{"f", "d", "s", "t", "c"},
		},
		"crlf": {
			in:   "From: f\r\nDate: d\r\nSubject: s\r\nTo: t\r\nCc: c\r\n\r\n",
			want: fields{"f", "d", "s", "t", "c"},
		},
		"continuation lines unfold": {
			in:   "Subject: one\r\n two\n\tthree\n\n",
			want: fields{Subject: "one two\tthree"},
		},
		"a folded From": {
			in:   "From: f\n <f@example.com>\n\n",
			want: fields{From: "f <f@example.com>"},
		},
		"whitespace before a name that isn't a continuation": {
			in:   "\rFrom: f\n\vTo: t\n\n",
			want: fields{From: "f", To: "t"},
		},
		"continuation after a colon-less line": {
			in:   "Subject: one\nno colon here\n two\n\n",
			want: fields{Subject: "one two"},
		},
		"colon-less and continuation lines before any field": {
			in:   "no colon here\n stray\nFrom: f\n\n",
			want: fields{From: "f"},
		},
		"no empty line": {
			in:   "From: f\nSubject: s",
			want: fields{From: "f", Subject: "s"},
		},
		"no header": {
			in: "\nFrom: f\n",
		},
		"the first occurrence wins": {
			in:   "From: f\nSubject: s\nFROM: g\n\n",
			want: fields{From: "f", Subject: "s"},
		},
		"names are trimmed and case-insensitive": {
			in:   "FROM : f\nsUbJeCt\t: s\n\n",
			want: fields{From: "f", Subject: "s"},
		},
		"names that only look like kept ones": {
			in: "Fr om: f\nSubjects: s\nX-To: t\n: c\n\n",
		},
		"another field ends the current one": {
			in:   "Subject: s\nX-Mailer: m\n more\n\n",
			want: fields{Subject: "s"},
		},
		"the whitespace after the colon goes": {
			in:   "Subject: \t s \nTo:\n \t t\n\n",
			want: fields{Subject: "s ", To: "t"},
		},
		"a colon in the value stays": {
			in:   "Subject: re: s\n\n",
			want: fields{Subject: "re: s"},
		},
		"a CR inside a value stays": {
			in:   "Subject: a\rb\r\r\n\n",
			want: fields{Subject: "a\rb\r"},
		},
		"a 70 KB line": {
			in:   "X-Long: " + strings.Repeat("x", 70*1024) + "\nFrom: f\n\n",
			want: fields{From: "f"},
		},
		"a 70 KB name": {
			in:   strings.Repeat("x", 70*1024) + ": v\n From\nTo: t\n\n",
			want: fields{To: "t"},
		},
		"a 70 KB value": {
			in:   "Subject: " + strings.Repeat("s", 70*1024) + "\n\n",
			want: fields{Subject: strings.Repeat("s", 70*1024)},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := scan(t, tt.in)

			assert.Equal(t, tt.want, fieldsOf(got))
			assert.Zero(t, got.Truncated)
		})
	}
}

func TestScanner_Scan_KeepsCRBeforeTextAcrossFragments(t *testing.T) {
	t.Parallel()

	// Lengths around the Scanner's 32 KiB buffer put each CR at the end of
	// a fragment for one of them.
	for n := 32*1024 - 16; n < 32*1024+16; n++ {
		value := strings.Repeat("s", n)
		got := scan(t, "Subject:"+value+"\rx\r\n continued\r\n\r\n")

		require.Equal(t, value+"\rx continued", string(got.Subject), n)
	}
}

func TestScanner_Scan_CapsEachField(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in            string
		want          int
		wantTruncated bool
	}{
		"under the cap": {
			in:   "Subject: " + strings.Repeat("s", message.FieldCap-1),
			want: message.FieldCap - 1,
		},
		"at the cap": {
			in:   "Subject: " + strings.Repeat("s", message.FieldCap),
			want: message.FieldCap,
		},
		"over the cap": {
			in: "Subject: " + strings.Repeat(
				"s",
				message.FieldCap+1,
			),
			want:          message.FieldCap,
			wantTruncated: true,
		},
		"over the cap by continuation lines": {
			in: "Subject: " + strings.Repeat("s", message.FieldCap/2) +
				"\r\n " + strings.Repeat("s", message.FieldCap/2) +
				"\r\n " + strings.Repeat("s", message.FieldCap/2) + "\r\n",
			want:          message.FieldCap,
			wantTruncated: true,
		},
		"at the cap before a CRLF": {
			in:   "Subject: " + strings.Repeat("s", message.FieldCap) + "\r\n",
			want: message.FieldCap,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := scan(t, "From: f\n"+tt.in+"\n\nbody")

			assert.Len(t, got.Subject, tt.want)
			assert.Equal(
				t,
				tt.wantTruncated,
				got.Truncated.Has(message.Subject),
			)
			assert.False(t, got.Truncated.Has(message.From))
		})
	}
}

func TestScanner_Scan_StreamsUnkeptFieldsAndBody(t *testing.T) {
	// Not parallel: it counts the bytes the whole process allocates.
	huge := strings.Repeat("x ", 8*1024*1024)
	tests := map[string]string{
		"a large unkept field": "From: f\nX-Junk: " + huge + "\n\nbody",
		"a large body":         "From: f\n\n" + huge,
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			var s message.Scanner
			_, err := s.Scan(strings.NewReader(in))
			require.NoError(t, err)

			allocated := allocatedBy(func() {
				_, err = s.Scan(strings.NewReader(in))
			})
			require.NoError(t, err)

			assert.Less(t, allocated, uint64(64*1024))
		})
	}
}

func TestScanner_Scan_NormalizesBody(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		body string
		want string
	}{
		"lf":                   {"a\nb\n", "a\nb"},
		"crlf":                 {"a\r\nb\r\n", "a\nb"},
		"whitespace runs":      {"a \t\v\f\r b  c", "a b c"},
		"leading and trailing": {"  a  \n\tb\t\r\n", "a\nb"},
		"inner blank lines":    {"a\n\n \t\r\nb", "a\n\n\nb"},
		"trailing blank lines": {"a\n\n  \n\r\n\n", "a"},
		"leading blank lines":  {"\n \na", "\n\na"},
		"many blank lines": {
			"a" + strings.Repeat("\n", 40) + "b",
			"a" + strings.Repeat("\n", 40) + "b",
		},
		"whitespace only": {" \n\t\r\n ", ""},
		"empty":           {"", ""},
		"a word over 32 KiB": {
			strings.Repeat("w", 40*1024) + " \t x",
			strings.Repeat("w", 40*1024) + " x",
		},
		"quoted-printable kept": {"a=\n b=3D", "a=\nb=3D"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := scan(t, "From: f\r\n\r\n"+tt.body)

			assert.Equal(t, sha256.Sum256([]byte(tt.want)), got.Body)
		})
	}
}

func TestScanner_Scan_LeavesBodyEmptyWithoutEmptyLine(t *testing.T) {
	t.Parallel()

	got := scan(t, "From: f\nbody: not a body\n")

	assert.Equal(t, sha256.Sum256(nil), got.Body)
}

func TestScanner_Scan_ResetsBetweenMailFiles(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"From: f\nSubject: " + strings.Repeat("s", message.FieldCap+1) +
			"\n\n a  b \n\n",
		"From: g\n\nc",
		"To: t\n continued",
	}

	var s message.Scanner
	reused := make([]message.Scanned, 0, len(inputs))
	for _, in := range inputs {
		got, err := s.Scan(strings.NewReader(in))
		require.NoError(t, err)

		reused = append(reused, got)
	}

	for i, in := range inputs {
		assert.Equal(t, scan(t, in), reused[i], in)
	}
}

func TestScanner_Scan_ReturnsReaderError(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"in the header": "From: f\nSubj",
		"in the body":   "From: f\n\nbody",
		"at the start":  "",
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var s message.Scanner
			got, err := s.Scan(failingReader(in))

			require.ErrorIs(t, err, errRead)
			assert.Zero(t, got)
		})
	}
}

func FuzzScan(f *testing.F) {
	for _, data := range mailFiles(f) {
		f.Add(data)
	}

	for _, data := range []string{
		"",
		"\n",
		"\r\n\r\n",
		"From: f\r\nSubject: s\r\n\r\nbody\r\n",
		"Subject: a\r\n b\r\nno colon\r\n\tc\r\n\r\n a  b \r\n\r\n",
		" leading continuation\n:\n\x00:\x00\n",
		"Subject:" + strings.Repeat("s", message.FieldCap+1) + "\r\n",
	} {
		f.Add([]byte(data))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var s message.Scanner
		got, err := s.Scan(bytes.NewReader(data))
		require.NoError(t, err)

		for _, v := range values(got) {
			assert.LessOrEqual(t, len(v), message.FieldCap)
		}

		_, err = s.Scan(failingReader(string(data)))
		require.ErrorIs(t, err, errRead)

		again, err := s.Scan(bytes.NewReader(data))
		require.NoError(t, err)
		assert.Equal(t, got, again)
	})
}

func BenchmarkScan(b *testing.B) {
	files := mailFiles(b)

	var s message.Scanner

	b.SetBytes(size(files))
	b.ReportAllocs()

	for b.Loop() {
		scanAll(b, &s, files)
	}
}

// errRead is the error failingReader returns after its content.
var errRead = errors.New("read failed")

// fields holds the kept header fields as strings, "" when missing.
type fields struct {
	From, Date, Subject, To, Cc string
}

func fieldsOf(s message.Scanned) fields {
	return fields{
		string(s.From),
		string(s.Date),
		string(s.Subject),
		string(s.To),
		string(s.Cc),
	}
}

func values(s message.Scanned) [][]byte {
	return [][]byte{s.From, s.Date, s.Subject, s.To, s.Cc}
}

func scan(t testing.TB, in string) message.Scanned {
	t.Helper()

	var s message.Scanner
	got, err := s.Scan(strings.NewReader(in))
	require.NoError(t, err)

	return got
}

// failingReader reads in, then fails with errRead.
func failingReader(in string) io.Reader {
	return io.MultiReader(strings.NewReader(in), iotest.ErrReader(errRead))
}

// scanAll scans each file with s.
func scanAll(b *testing.B, s *message.Scanner, files map[string][]byte) {
	b.Helper()

	var r bytes.Reader
	for _, data := range files {
		r.Reset(data)
		if _, err := s.Scan(&r); err != nil {
			b.Fatal(err)
		}
	}
}

func size(files map[string][]byte) int64 {
	var n int64
	for _, data := range files {
		n += int64(len(data))
	}

	return n
}

// allocatedBy returns the bytes the process allocated while f ran.
func allocatedBy(f func()) uint64 {
	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)

	return after.TotalAlloc - before.TotalAlloc
}

// mailFiles reads the real Mail files in testdata/mail, by name.
func mailFiles(tb testing.TB) map[string][]byte {
	tb.Helper()

	paths, err := filepath.Glob(filepath.Join("testdata", "mail", "*.eml"))
	require.NoError(tb, err)
	require.NotEmpty(tb, paths)

	files := make(map[string][]byte, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(tb, err)

		files[filepath.Base(path)] = data
	}

	return files
}
