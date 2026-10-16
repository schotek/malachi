// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package tz resolves the TZID of iCalendar and Graph objects to Go
// locations and writes VTIMEZONE components for IANA zones
// (docs/calendar.md D4).
//
// The source of truth is Go's IANA tzdata (cmd/malachid imports
// time/tzdata, so it is the same on every platform). A VTIMEZONE of a
// received object is never interpreted as rules: the caller passes only
// its STANDARD/DAYLIGHT offsets as plain numbers (Offsets), which are
// used to pick a matching zone or, as the last resort, a fixed offset.
package tz

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Kind says how a TZID was resolved.
type Kind int

const (
	// KindIANA: the TZID is an IANA zone name (or UTC).
	KindIANA Kind = iota + 1
	// KindWindows: the TZID is a Windows zone name (CLDR table).
	KindWindows
	// KindPrefix: the TZID is an IANA name behind a known vendor prefix
	// such as /mozilla.org/20050126_1/Europe/Prague.
	KindPrefix
	// KindMatched: the TZID is unknown; a zone whose offsets match the
	// caller's VTIMEZONE offsets at the sample instant was chosen.
	KindMatched
	// KindFixed: the TZID is unknown; a fixed offset built from the
	// caller's standard offset is returned (the caller logs a problem).
	KindFixed
)

func (k Kind) String() string {
	switch k {
	case KindIANA:
		return "iana"
	case KindWindows:
		return "windows"
	case KindPrefix:
		return "prefix"
	case KindMatched:
		return "matched"
	case KindFixed:
		return "fixed"
	}
	return "unknown"
}

// ErrUnknownZone is returned when a TZID cannot be resolved and no
// usable offsets were given.
var ErrUnknownZone = errors.New("tz: unknown time zone")

// ErrNotIANA is returned by WriteVTIMEZONE for a location that is not a
// loadable IANA zone (Local, a fixed zone).
var ErrNotIANA = errors.New("tz: location is not an IANA zone")

// MaxOffset bounds the offsets accepted from a VTIMEZONE (UTC−18..+18,
// the range of ISO 8601 / java.time; real zones stay within −12..+14).
const MaxOffset = 18 * time.Hour

// maxTZIDBytes bounds the TZID looked at; longer values are unknown.
const maxTZIDBytes = 256

// Offsets are the UTC offsets of a parsed VTIMEZONE, as plain numbers.
// Daylight is meaningful only when HasDaylight is set. At, when not
// zero, is an instant at which the zone is used (the event's start); it
// enables picking an IANA zone whose offsets match in At's year.
type Offsets struct {
	Standard    time.Duration
	Daylight    time.Duration
	HasDaylight bool
	At          time.Time
}

// prefixes are vendor TZID prefixes followed by a version segment (or
// none) and an IANA name.
var prefixes = []string{
	"/mozilla.org/",
	"/softwarestudio.org/",
	"/freeassociation.sourceforge.net/",
	"/citadel.org/",
}

// Resolve resolves an iCalendar or Graph TZID: an IANA name, then a
// Windows name, then an IANA name behind a known prefix, then (with
// hints) an IANA zone whose standard and daylight offsets match the
// first hint in its At year, then a fixed zone of the hint's standard
// offset. Without a usable hint an unresolvable TZID is ErrUnknownZone.
// "" and "Local" are never resolved (floating time is the caller's).
func Resolve(tzid string, hints ...Offsets) (*time.Location, Kind, error) {
	name := clean(tzid)
	if name != "" {
		if loc, ok := loadIANA(name); ok {
			return loc, KindIANA, nil
		}
		if loc, ok := loadWindows(name); ok {
			return loc, KindWindows, nil
		}
		if loc, ok := loadPrefixed(name); ok {
			return loc, KindPrefix, nil
		}
	}
	if len(hints) == 0 {
		return nil, 0, fmt.Errorf("%w: %q", ErrUnknownZone, short(tzid))
	}
	h := hints[0]
	if !validOffset(h.Standard) || (h.HasDaylight && !validOffset(h.Daylight)) {
		return nil, 0, fmt.Errorf("%w: %q: offsets out of range", ErrUnknownZone, short(tzid))
	}
	if !h.At.IsZero() {
		if loc, ok := matchOffsets(h); ok {
			return loc, KindMatched, nil
		}
	}
	return time.FixedZone(fixedName(h.Standard), int(h.Standard/time.Second)), KindFixed, nil
}

// preferred is the zone SetPreferred chose (nil = none).
var preferred atomic.Pointer[time.Location]

// SetPreferred sets the zone Resolve returns for a Windows name when it
// is one of the IANA zones CLDR maps to that name in any territory (the
// user's own zone: "Central Europe Standard Time" is Europe/Prague for a
// user in Prague, not CLDR's territory 001 Europe/Budapest). Other
// Windows names keep their territory 001 zone. nil, Local or a location
// that is not a loadable IANA zone clears the preference. The daemon
// sets it once at start; it is safe for concurrent use. WindowsName is
// not affected.
func SetPreferred(loc *time.Location) {
	if loc == nil {
		preferred.Store(nil)
		return
	}
	l, ok := loadIANA(loc.String())
	if !ok || l == time.UTC {
		preferred.Store(nil)
		return
	}
	preferred.Store(l)
}

// WindowsName returns the Windows time zone name of an IANA zone, for
// writing to Graph. "UTC" and "Etc/UTC" map to "UTC".
func WindowsName(iana string) (string, bool) {
	switch iana {
	case "UTC", "Etc/UTC", "Etc/GMT", "GMT", "Etc/Universal", "Etc/Zulu":
		return "UTC", true
	}
	w, ok := ianaToWindows[iana]
	return w, ok
}

// IANAName returns the IANA zone of a Windows time zone name (territory
// 001 of the CLDR table), matching case-insensitively.
func IANAName(windows string) (string, bool) {
	if v, ok := windowsToIANA[windows]; ok {
		return v, true
	}
	v, ok := windowsFolded[strings.ToLower(windows)]
	return v, ok
}

var windowsFolded = func() map[string]string {
	m := make(map[string]string, len(windowsToIANA))
	for k, v := range windowsToIANA {
		m[strings.ToLower(k)] = v
	}
	return m
}()

// matchCandidates are the territory 001 zones of the Windows table in a
// stable order, the zones tried by matchOffsets.
var matchCandidates = func() []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range windowsToIANA {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}()

func clean(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if len(s) > maxTZIDBytes {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return ""
		}
	}
	return s
}

// ianaShaped accepts the characters of IANA names; it keeps names such
// as "../../etc/passwd" or "Local" away from time.LoadLocation.
func ianaShaped(s string) bool {
	if s == "" || s == "Local" || s[0] == '/' || strings.Contains(s, "..") ||
		strings.Contains(s, "//") || strings.HasSuffix(s, "/") || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '/', c == '_', c == '-', c == '+':
		default:
			return false
		}
	}
	return true
}

func loadIANA(name string) (*time.Location, bool) {
	switch strings.ToUpper(name) {
	case "UTC", "Z", "GMT", "ETC/UTC", "ETC/GMT":
		return time.UTC, true
	}
	if !ianaShaped(name) {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	return loc, true
}

func loadWindows(name string) (*time.Location, bool) {
	iana, ok := IANAName(name)
	if !ok {
		return nil, false
	}
	if p := preferred.Load(); p != nil {
		if w, ok := ianaToWindows[p.String()]; ok && strings.EqualFold(w, name) {
			return p, true
		}
	}
	if iana == "Etc/UTC" || iana == "Etc/GMT" {
		return time.UTC, true
	}
	return loadIANA(iana)
}

func loadPrefixed(name string) (*time.Location, bool) {
	lower := strings.ToLower(name)
	for _, p := range prefixes {
		if !strings.HasPrefix(lower, p) {
			continue
		}
		rest := name[len(p):]
		// The tail is either the IANA name or a version segment
		// (20050126_1, Olson_20011030_5, Tzfile) and the name.
		for i := 0; i < 3 && rest != ""; i++ {
			if loc, ok := loadIANA(rest); ok {
				return loc, true
			}
			slash := strings.IndexByte(rest, '/')
			if slash < 0 {
				break
			}
			rest = rest[slash+1:]
		}
		return nil, false
	}
	return nil, false
}

func validOffset(d time.Duration) bool {
	return d >= -MaxOffset && d <= MaxOffset && d%time.Second == 0
}

// matchOffsets picks the first candidate zone whose standard and
// daylight offsets in h.At's year equal the hint's.
func matchOffsets(h Offsets) (*time.Location, bool) {
	year := h.At.UTC().Year()
	if year < 1900 || year > 2200 {
		return nil, false
	}
	wantStd, wantDST := h.Standard, h.Standard
	if h.HasDaylight {
		wantDST = h.Daylight
	}
	for _, name := range matchCandidates {
		loc, ok := loadIANA(name)
		if !ok {
			continue
		}
		std, dst := yearOffsets(loc, year)
		if std == wantStd && dst == wantDST {
			return loc, true
		}
	}
	return nil, false
}

// yearOffsets returns the standard and daylight offsets of loc in year:
// the offsets at mid-January and mid-July, the one marked DST being the
// daylight offset (equal when the zone has no DST that year).
func yearOffsets(loc *time.Location, year int) (std, dst time.Duration) {
	a := time.Date(year, time.January, 15, 12, 0, 0, 0, loc)
	b := time.Date(year, time.July, 15, 12, 0, 0, 0, loc)
	_, oa := a.Zone()
	_, ob := b.Zone()
	da, db := time.Duration(oa)*time.Second, time.Duration(ob)*time.Second
	switch {
	case a.IsDST() && !b.IsDST():
		return db, da
	case b.IsDST() && !a.IsDST():
		return da, db
	}
	return da, db
}

func fixedName(off time.Duration) string {
	sign := '+'
	if off < 0 {
		sign, off = '-', -off
	}
	h := int(off / time.Hour)
	m := int(off%time.Hour) / int(time.Minute)
	return fmt.Sprintf("UTC%c%02d:%02d", sign, h, m)
}

// short bounds a hostile TZID quoted in an error message.
func short(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	return strings.ToValidUTF8(s, "?")
}
