# Third-party code and data

Libraries in `go.mod` carry their own licences; this file records the ones a feature design asked
to note, and data embedded in the binary (VISION: record the source and licence of every content item).

| Item | Used for | Licence | Source |
|---|---|---|---|
| `github.com/arran4/golang-ical` v0.3.8 | Parsing iCalendar files (F9a calendar import) | Apache-2.0 | github.com/arran4/golang-ical |
| `github.com/teambition/rrule-go` v1.8.2 | Expanding RRULE recurrences (F9a) | MIT | github.com/teambition/rrule-go |
| Windows zone names → IANA (subset, `internal/icsimport/zones.go`) | Outlook's TZID names (F9a) | Unicode License v3 | CLDR `common/supplemental/windowsZones.xml`, territory "001" |
