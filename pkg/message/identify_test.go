package message_test

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"mingest/pkg/message"
)

func TestIdentify_ExtractsSender(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		from string
		want string
	}{
		"a bare address": {
			from: "phillip.allen@enron.com",
			want: "phillip.allen@enron.com",
		},
		"a display name": {
			from: `"Allen, Phillip" <phillip.allen@enron.com>`,
			want: "phillip.allen@enron.com",
		},
		"the first of a list": {
			from: "a@enron.com, b@enron.com",
			want: "a@enron.com",
		},
		"uppercase and surrounding whitespace": {
			from: "  Phillip.Allen@ENRON.com \t",
			want: "phillip.allen@enron.com",
		},
		"A and Z are lowercased": {
			from: "AZ@ENRON.COM",
			want: "az@enron.com",
		},
		"recovered text that parses is canonical": {
			from: `x <"John"@ENRON.com >`,
			want: "john@enron.com",
		},
		"recovered text that parses as a list gives its first": {
			from: "<A@enron.com,>",
			want: "a@enron.com",
		},
		"an unclosed angle bracket keeps the whole value": {
			from: "x..y@enron.com <unclosed",
			want: "x..y@enron.com <unclosed",
		},
		"a leading dot, recovered from the angle brackets": {
			from: "ets <.nelson@enron.com>",
			want: ".nelson@enron.com",
		},
		"a double dot, recovered as written": {
			from: "John..Doe@enron.com",
			want: "john..doe@enron.com",
		},
		"no @ is no Sender": {
			from: "pep <performance.>",
		},
		"a garbled address with an @ is a Sender": {
			from: `<"d@piassick".@enron@enron.com>`,
			want: `"d@piassick".@enron@enron.com`,
		},
		"a quoted local part stays quoted": {
			from: `<"John Doe"@Enron.com>`,
			want: `"john doe"@enron.com`,
		},
		"needless quotes go": {
			from: `"john.doe"@enron.com`,
			want: "john.doe@enron.com",
		},
		"only ASCII is lowercased": {
			from: "<ÉTS\x8F@ENRON.com>",
			want: "Éts\x8f@enron.com",
		},
		"the last angle brackets": {
			from: "a <x> <.b@enron.com>",
			want: ".b@enron.com",
		},
		"an empty group": {
			from: "undisclosed-recipients:;",
		},
		"missing": {},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := message.Identify(message.Scanned{From: []byte(tt.from)})
			assert.Equal(t, tt.want, got.Sender)
		})
	}
}

func TestIdentify_DecodesSubject(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in, want string
	}{
		"plain text": {
			in:   "Re: the plan",
			want: "Re: the plan",
		},
		"case and Re: are kept": {
			in:   "RE: The Plan",
			want: "RE: The Plan",
		},
		"whitespace runs collapse and the ends are trimmed": {
			in:   " \tRe:\t the \v\f plan\r\n ",
			want: "Re: the plan",
		},
		"UTF-8": {
			in:   "=?UTF-8?Q?Caf=C3=A9_menu?=",
			want: "Café menu",
		},
		"ISO-8859-1": {
			in:   "=?ISO-8859-1?B?Q2Fm6Q==?=",
			want: "Café",
		},
		"words among text": {
			in:   "Re: =?utf-8?q?caf=C3=A9?= =?utf-8?q?_now?= later",
			want: "Re: café now later",
		},
		"KOI8-R is kept as written": {
			in:   "=?KOI8-R?B?8NLJ18XU?=",
			want: "=?KOI8-R?B?8NLJ18XU?=",
		},
		"one unknown charset keeps the whole value": {
			in:   "=?KOI8-R?B?8NLJ18XU?= =?UTF-8?Q?caf=C3=A9?=",
			want: "=?KOI8-R?B?8NLJ18XU?= =?UTF-8?Q?caf=C3=A9?=",
		},
		"the ANSI_X3.4-1968 alias of US-ASCII": {
			in:   "=?ANSI_X3.4-1968?Q?Re:_Samsung_Event?=",
			want: "Re: Samsung Event",
		},
		"US-ASCII turns bytes past 0x7F into U+FFFD": {
			in:   "=?ascii?Q?caf=7F=80=E9?=",
			want: "caf\x7f\uFFFD\uFFFD",
		},
		"the ISO8859-1 misspelling": {
			in:   "=?ISO8859-1?Q?.49=A2_each?=",
			want: ".49¢ each",
		},
		"the latin1 alias": {
			in:   "=?Latin1?Q?caf=E9?=",
			want: "café",
		},
		"a language suffix": {
			in:   "=?us-ascii*en?Q?Hello?=",
			want: "Hello",
		},
		"an empty charset is kept as written": {
			in:   "=??Q?Grade=20=93A=94=20Sale?=",
			want: "=??Q?Grade=20=93A=94=20Sale?=",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := message.Identify(message.Scanned{Subject: []byte(tt.in)})
			assert.Equal(t, encode(identity{Subject: tt.want}), got.Fingerprint)
		})
	}
}

func TestIdentify_BuildsRecipientSets(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in     message.Scanned
		to, cc []string
	}{
		"order, case and whitespace": {
			in: message.Scanned{To: []byte(" B@enron.com,a@ENRON.com ")},
			to: []string{"a@enron.com", "b@enron.com"},
		},
		"duplicates": {
			in: message.Scanned{To: []byte("a@enron.com, A@enron.com")},
			to: []string{"a@enron.com"},
		},
		"a display name with a quoted comma": {
			in: message.Scanned{
				Cc: []byte(`"Doe, John" <j@enron.com>, k@enron.com`),
			},
			cc: []string{"j@enron.com", "k@enron.com"},
		},
		"To and Cc": {
			in: message.Scanned{
				To: []byte("a@enron.com"),
				Cc: []byte("b@enron.com"),
			},
			to: []string{"a@enron.com"},
			cc: []string{"b@enron.com"},
		},
		"a strict-parse failure recovers each element": {
			in: message.Scanned{To: []byte(
				`a..b@enron.com, "Doe, <x>" <.c@enron.com>, , d@enron.com`,
			)},
			to: []string{".c@enron.com", "a..b@enron.com", "d@enron.com"},
		},
		"an escaped quote doesn't end a quoted comma": {
			in: message.Scanned{To: []byte(
				`"Doe \"J, r\"" <.x@enron.com>, y@enron.com`,
			)},
			to: []string{".x@enron.com", "y@enron.com"},
		},
		"a truncated field drops its last element": {
			in: message.Scanned{
				To:        []byte("a@enron.com, b@enron.com, c@enr"),
				Truncated: message.To,
			},
			to: []string{"a@enron.com", "b@enron.com"},
		},
		"a truncated recovered field drops its last element": {
			in: message.Scanned{
				Cc:        []byte("a..b@enron.com, c@enr"),
				Truncated: message.Cc,
			},
			cc: []string{"a..b@enron.com"},
		},
		"an untruncated field keeps its last element": {
			in: message.Scanned{To: []byte("a@enron.com, c@enr")},
			to: []string{"a@enron.com", "c@enr"},
		},
		"a truncated empty group": {
			in: message.Scanned{To: []byte("x:;"), Truncated: message.To},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := encode(identity{To: tt.to, Cc: tt.cc})
			assert.Equal(t, want, message.Identify(tt.in).Fingerprint)
		})
	}
}

func TestIdentify_NormalizesDate(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		a, b string
		same bool
	}{
		"one instant in two zones": {
			a:    "Thu, 21 Jun 2001 21:35:40 -0700 (PDT)",
			b:    "Fri, 22 Jun 2001 04:35:40 +0000",
			same: true,
		},
		"the 1979 placeholder is 1980-01-01 in UTC": {
			a:    "Mon, 31 Dec 1979 16:00:00 -0800 (PST)",
			b:    "Tue, 1 Jan 1980 00:00:00 GMT",
			same: true,
		},
		"another instant": {
			a: "Thu, 21 Jun 2001 21:35:40 -0700",
			b: "Thu, 21 Jun 2001 21:35:41 -0700",
		},
		"an unparseable Date is trimmed": {
			a:    "?????????????????????????????????????=",
			b:    "  ?????????????????????????????????????=\t",
			same: true,
		},
		"an unparseable Date is kept as written": {
			a: "?????=",
			b: "??????=",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			a := message.Identify(message.Scanned{Date: []byte(tt.a)})
			b := message.Identify(message.Scanned{Date: []byte(tt.b)})
			assert.Equal(t, tt.same, a.Fingerprint == b.Fingerprint)
		})
	}
}

func TestIdentify_CollapsesCopies(t *testing.T) {
	t.Parallel()

	inbox := "Message-ID: <1.JavaMail.evans@thyme>\n" +
		"Date: Mon, 14 May 2001 16:39:00 -0700 (PDT)\n" +
		"From: Phillip.Allen@enron.com\n" +
		"To: tim.belden@enron.com, john.lavorato@enron.com\n" +
		"Subject: Re: the plan\n" +
		"X-Folder: \\Phillip_Allen_Jan2002_1\\Allen, Phillip K.\\Inbox\n" +
		"X-FileName: pallen (Non-Privileged).pst\n" +
		"\n" +
		"Here is our forecast.\n" +
		"\n" +
		"Phillip\n"
	sent := "Message-ID: <2.JavaMail.evans@thyme>\r\n" +
		"Date: Mon, 14 May 2001 23:39:00 +0000\r\n" +
		"From: phillip.allen@enron.com\r\n" +
		"To: John.Lavorato@enron.com,\r\n\ttim.belden@enron.com\r\n" +
		"Subject:  Re:  the plan\r\n" +
		"X-Folder: \\pallen\\Sent Mail\r\n" +
		"X-FileName: pallen.nsf\r\n" +
		"\r\n" +
		"  Here  is our\tforecast. \r\n" +
		"\r\n" +
		"Phillip\r\n\r\n\r\n"

	assert.Equal(t, identify(t, inbox), identify(t, sent))
}

func TestIdentify_KeepsDifferentEmailsApart(t *testing.T) {
	t.Parallel()

	header := "From: a@enron.com\nTo: b@enron.com\nSubject: s\n" +
		"Date: Mon, 14 May 2001 16:39:00 -0700\n\n"

	tests := map[string]struct {
		a, b message.Scanned
	}{
		"different bodies": {
			a: scan(t, header+"one\n"),
			b: scan(t, header+"two\n"),
		},
		"a comma moved between Date and Subject": {
			a: message.Scanned{Date: []byte("a,b"), Subject: []byte("c")},
			b: message.Scanned{Date: []byte("a"), Subject: []byte("b,c")},
		},
		"an address moved between To and Cc": {
			a: message.Scanned{
				To: []byte("a@enron.com, b@enron.com"),
				Cc: []byte("c@enron.com"),
			},
			b: message.Scanned{
				To: []byte("a@enron.com"),
				Cc: []byte("b@enron.com, c@enron.com"),
			},
		},
		"a Sender moved into the Date": {
			a: message.Scanned{From: []byte("a@enron.com")},
			b: message.Scanned{Date: []byte("a@enron.com")},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.NotEqual(t,
				message.Identify(tt.a).Fingerprint,
				message.Identify(tt.b).Fingerprint,
			)
		})
	}
}

func TestIdentify_EncodesFingerprint(t *testing.T) {
	t.Parallel()

	in := scan(t, "From: A <a@enron.com>\n"+
		"Date: Mon, 14 May 2001 16:39:00 -0700\n"+
		"Subject: s\n"+
		"To: c@enron.com, b@enron.com\n"+
		"Cc: d@enron.com\n\nbody\n")

	want := encode(identity{
		Sender:  "a@enron.com",
		Date:    "2001-05-14T23:39:00Z",
		Subject: "s",
		To:      []string{"b@enron.com", "c@enron.com"},
		Cc:      []string{"d@enron.com"},
		Body:    sha256.Sum256([]byte("body")),
	})
	assert.Equal(t, want, message.Identify(in).Fingerprint)
}

func FuzzSender(f *testing.F) {
	for _, data := range mailFiles(f) {
		f.Add(string(scan(f, string(data)).From))
	}

	for _, from := range []string{
		"",
		"phillip.allen@enron.com",
		"ets <.nelson@enron.com>",
		"john..doe@enron.com",
		"pep <performance.>",
		`<"d@piassick".@enron@enron.com>`,
		`"a, b" <c@d>, e@f`,
		"a <b> <",
		"=?utf-8?q?caf=C3=A9?= <x@y>",
	} {
		f.Add(from)
	}

	f.Fuzz(func(t *testing.T, from string) {
		got := message.Identify(message.Scanned{From: []byte(from)}).Sender
		if got == "" {
			return
		}

		again := message.Identify(message.Scanned{From: []byte(got)}).Sender
		assert.Equal(t, got, again)
	})
}

func FuzzIdentify(f *testing.F) {
	for _, data := range mailFiles(f) {
		header, body, _ := strings.Cut(string(data), "\r\n\r\n")
		f.Add(header, body)
	}

	f.Add("From: a@enron.com\nSubject: s", "one  two\n\nthree\n")
	f.Add("", " \t\r\n\v\f")
	f.Add("To: x", "a\rb\n\n\n")

	f.Fuzz(func(t *testing.T, header, body string) {
		spaced := strings.NewReplacer(
			" ", " \t ",
			"\t", "\f\t",
			"\n", " \r\n\v",
		).Replace(body)
		spaced = " " + spaced + "\n\n\r\n"

		a := identify(t, header+"\n\n"+body)
		b := identify(t, header+"\n\n"+spaced)
		assert.Equal(t, a.Fingerprint, b.Fingerprint)
	})
}

func BenchmarkIdentify(b *testing.B) {
	b.Run("typical", benchmarkTypical)

	// The seed Mail files are chosen for their odd cases, two with about
	// 450 recipients each, so they cost more than a typical one.
	b.Run("seeds", benchmarkSeeds)
}

// identity holds the values a fingerprint is taken over.
type identity struct {
	Sender, Date, Subject string
	To, Cc                []string
	Body                  [sha256.Size]byte
}

// encode builds the fingerprint by hand: SHA-256 over each value with a
// uvarint length prefix, each set with a uvarint count prefix, truncated
// to 16 bytes.
func encode(id identity) [16]byte {
	var buf []byte

	str := func(v string) {
		buf = binary.AppendUvarint(buf, uint64(len(v)))
		buf = append(buf, v...)
	}
	set := func(vs []string) {
		buf = binary.AppendUvarint(buf, uint64(len(vs)))
		for _, v := range vs {
			str(v)
		}
	}

	str(id.Sender)
	str(id.Date)
	str(id.Subject)
	set(id.To)
	set(id.Cc)
	str(string(id.Body[:]))

	sum := sha256.Sum256(buf)

	return [16]byte(sum[:16])
}

func identify(t testing.TB, in string) message.Identity {
	t.Helper()

	return message.Identify(scan(t, in))
}

func benchmarkTypical(b *testing.B) {
	typical := scan(b, "Message-ID: <1.JavaMail.evans@thyme>\r\n"+
		"Date: Mon, 14 May 2001 16:39:00 -0700 (PDT)\r\n"+
		"From: phillip.allen@enron.com\r\n"+
		"To: tim.belden@enron.com, john.lavorato@enron.com,\r\n"+
		"\tgreg.whalley@enron.com\r\n"+
		"Subject: Re: West Power forecast\r\n"+
		"Cc: jeff.skilling@enron.com\r\n"+
		"\r\n"+
		"Here is our forecast.\r\n")

	b.ReportAllocs()

	for b.Loop() {
		message.Identify(typical)
	}
}

func benchmarkSeeds(b *testing.B) {
	var seeds []message.Scanned
	for _, data := range mailFiles(b) {
		seeds = append(seeds, scan(b, string(data)))
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, s := range seeds {
			message.Identify(s)
		}
	}

	perFile := float64(b.Elapsed().Nanoseconds()) / float64(b.N*len(seeds))
	b.ReportMetric(perFile, "ns/mail_file")
}
