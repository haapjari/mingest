# Sources

Real Mail files used as fuzz seeds and unit-test cases. Each is copied byte for byte from the public
CMU Enron Dataset, `enron_mail_20150507.tar.gz` (https://www.cs.cmu.edu/~enron/). Only the name
changes: the path joined with `__`, without the trailing dot, plus `.eml`.

| File | Dataset path | Why |
|---|---|---|
| `bass-e__all_documents__1633.eml` | `maildir/bass-e/all_documents/1633.` | 1979 placeholder Date |
| `baughman-d__calendar__19.eml` | `maildir/baughman-d/calendar/19.` | colon-less header line; `net/mail` rejects it |
| `baughman-d__calendar__4.eml` | `maildir/baughman-d/calendar/4.` | colon-less header line; `net/mail` rejects it |
| `baughman-d__calendar__5.eml` | `maildir/baughman-d/calendar/5.` | colon-less header line; `net/mail` rejects it |
| `baughman-d__deleted_items__303.eml` | `maildir/baughman-d/deleted_items/303.` | colon-less header line; `net/mail` rejects it |
| `baughman-d__tasks__4.eml` | `maildir/baughman-d/tasks/4.` | colon-less header line; `net/mail` rejects it |
| `buy-r__contacts__1.eml` | `maildir/buy-r/contacts/1.` | From local part with `..` |
| `campbell-l__inbox__1040.eml` | `maildir/campbell-l/inbox/1040.` | colon-less header line; `net/mail` rejects it |
| `farmer-d__calendar__6.eml` | `maildir/farmer-d/calendar/6.` | From local part with `..` |
| `geaccone-t__inbox__537.eml` | `maildir/geaccone-t/inbox/537.` | colon-less header line; `net/mail` rejects it |
| `haedicke-m__calendar__37.eml` | `maildir/haedicke-m/calendar/37.` | colon-less header line; `net/mail` rejects it |
| `hendrickson-s__deleted_items__67.eml` | `maildir/hendrickson-s/deleted_items/67.` | colon-less header line; `net/mail` rejects it |
| `hyatt-k__personal__252.eml` | `maildir/hyatt-k/personal/252.` | From `ets <.nelson@enron.com>`, leading dot |
| `kaminski-v__calendar__3.eml` | `maildir/kaminski-v/calendar/3.` | colon-less header line that folds into one with a colon; `net/mail` accepts it |
| `kaminski-v__calendar__40.eml` | `maildir/kaminski-v/calendar/40.` | colon-less header line; `net/mail` rejects it |
| `kitchen-l__calendar__35.eml` | `maildir/kitchen-l/calendar/35.` | colon-less header line; `net/mail` rejects it |
| `kuykendall-t__deleted_items__22.eml` | `maildir/kuykendall-t/deleted_items/22.` | colon-less header line; `net/mail` rejects it |
| `lay-k__deleted_items__532.eml` | `maildir/lay-k/deleted_items/532.` | garbled quoted From |
| `lenhart-m__deleted_items__2.eml` | `maildir/lenhart-m/deleted_items/2.` | longest header line (70,350 B) |
| `lenhart-m__deleted_items__351.eml` | `maildir/lenhart-m/deleted_items/351.` | colon-less header line; `net/mail` rejects it |
| `lewis-a__deleted_items__371.eml` | `maildir/lewis-a/deleted_items/371.` | RFC 2047 Subject, ISO8859-1 (the only one; a misspelling Mingest decodes) |
| `lokey-t__calendar__28.eml` | `maildir/lokey-t/calendar/28.` | colon-less header line; `net/mail` rejects it |
| `mclaughlin-e__sent__104.eml` | `maildir/mclaughlin-e/sent/104.` | RFC 2047 Subject, ANSI_X3.4-1968 (an alias Mingest decodes) |
| `sager-e__calendar__34.eml` | `maildir/sager-e/calendar/34.` | colon-less header line; `net/mail` rejects it |
| `saibi-e__inbox__1023.eml` | `maildir/saibi-e/inbox/1023.` | garbled quoted From |
| `saibi-e__inbox__468.eml` | `maildir/saibi-e/inbox/468.` | largest header block (96,230 B) |
| `schwieger-j__calendar__1.eml` | `maildir/schwieger-j/calendar/1.` | colon-less header line; `net/mail` rejects it |
| `schwieger-j__calendar__2.eml` | `maildir/schwieger-j/calendar/2.` | colon-less header line; `net/mail` rejects it |
| `schwieger-j__calendar__3.eml` | `maildir/schwieger-j/calendar/3.` | colon-less header line; `net/mail` rejects it |
| `schwieger-j__calendar__4.eml` | `maildir/schwieger-j/calendar/4.` | colon-less header line; `net/mail` rejects it |
| `schwieger-j__calendar__6.eml` | `maildir/schwieger-j/calendar/6.` | colon-less header line; `net/mail` rejects it |
| `scott-s__inbox__202.eml` | `maildir/scott-s/inbox/202.` | RFC 2047 word with an empty charset; kept as written |
| `skilling-j__inbox__223.eml` | `maildir/skilling-j/inbox/223.` | From `pep <performance.>`, no @ |
| `whalley-g__calendar__128.eml` | `maildir/whalley-g/calendar/128.` | colon-less header line; `net/mail` rejects it |
| `whalley-g__calendar__139.eml` | `maildir/whalley-g/calendar/139.` | colon-less header line; `net/mail` rejects it |
| `whalley-g__calendar__142.eml` | `maildir/whalley-g/calendar/142.` | colon-less header line; `net/mail` rejects it |
| `whalley-g__test__calendar__79.eml` | `maildir/whalley-g/test/calendar/79.` | colon-less header line; `net/mail` rejects it |
| `whalley-g__test__calendar__90.eml` | `maildir/whalley-g/test/calendar/90.` | colon-less header line; `net/mail` rejects it |
| `whalley-g__test__calendar__93.eml` | `maildir/whalley-g/test/calendar/93.` | colon-less header line; `net/mail` rejects it |
| `zipper-a__calendar__23.eml` | `maildir/zipper-a/calendar/23.` | colon-less header line; `net/mail` rejects it |
