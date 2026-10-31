// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package vcard

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/board"
)

// Limits of the parser.
const (
	// MaxCardBytes is the largest input Parse and Patch read (ErrTooLarge).
	MaxCardBytes = 1 << 20
	// MaxProperties caps the properties of the card (ErrTooManyProperties).
	MaxProperties = 500
	// MaxLineBytes caps an unfolded line whose value is decoded; a longer
	// line is kept by Patch but never decoded. PHOTO has MaxPhotoLineBytes.
	MaxLineBytes = 256 << 10
	// MaxItems caps each list of the model (emails, phones, addresses,
	// URLs); further items are ignored by Parse and kept by Patch.
	MaxItems = 100

	// Caps of cleaned strings, in bytes.
	MaxFieldBytes = 1024
	MaxNoteBytes  = 32 << 10
	MaxLabelBytes = 64
	MaxURLBytes   = 2048
	MaxEmailBytes = 320
	MaxPhoneBytes = 128
	MaxUIDBytes   = 255
)

// Errors of Parse and Patch.
var (
	ErrTooLarge          = errors.New("vcard: card too large")
	ErrTooManyProperties = errors.New("vcard: too many properties")
	ErrNoCard            = errors.New("vcard: no BEGIN:VCARD")
	ErrNilCard           = errors.New("vcard: nil card")
	ErrMalformed         = errors.New("vcard: malformed card")
)

// Normalised labels. Any other non-empty label is a custom one (Apple's
// X-ABLabel), kept as its cleaned text.
const (
	LabelHome   = "home"
	LabelWork   = "work"
	LabelMobile = "mobile"
	LabelFax    = "fax"
	LabelOther  = "other"
)

// Name is the structured name (N).
type Name struct {
	Given, Family, Middle, Prefix, Suffix string
}

// Email is one EMAIL.
type Email struct {
	Label, Address string
}

// Phone is one TEL.
type Phone struct {
	Label, Number string
}

// Address is one ADR (post office box and extended address are not in the
// model; Patch keeps them).
type Address struct {
	Label, Street, City, Region, Postcode, Country string
}

// URL is one URL. It is text: whoever shows it as a link checks the scheme.
type URL struct {
	Label, URL string
}

// Birthday is BDAY. Year 0 is an unknown year (--MMDD, Apple's 1604);
// Month 0 is no birthday.
type Birthday struct {
	Year, Month, Day int
}

// IsZero reports whether there is no birthday.
func (b Birthday) IsZero() bool { return b.Month == 0 }

// Card is the model of one contact card (ContactCard of the API).
type Card struct {
	Version     string
	UID         string
	Name        Name
	DisplayName string // FN
	Nickname    string
	Org         string
	Title       string
	Emails      []Email
	Phones      []Phone
	Addresses   []Address
	URLs        []URL
	Birthday    Birthday
	Note        string
	// Photo is the embedded picture of the first PHOTO, nil when there is
	// none; PhotoURI: that PHOTO names a picture by a link instead, which
	// is never fetched (docs/plans/address-book.md F4). Neither is ever
	// written: Patch copies PHOTO lines byte for byte, Write has none.
	Photo    *Photo
	PhotoURI bool
}

// now is the clock of REV.
var now = time.Now

// Clean returns a cleaned copy of c, the form Parse returns: every string
// cleaned and capped, labels normalised, items without a value dropped,
// each list capped at MaxItems, an invalid birthday dropped. Clean of a
// cleaned card is the same card. Nil is an empty card.
func Clean(c *Card) *Card {
	out := &Card{}
	if c == nil {
		return out
	}
	out.Version = cleanLine(c.Version, 16)
	out.UID = cleanLine(c.UID, MaxUIDBytes)
	out.Name = Name{
		Given:  cleanLine(c.Name.Given, MaxFieldBytes),
		Family: cleanLine(c.Name.Family, MaxFieldBytes),
		Middle: cleanLine(c.Name.Middle, MaxFieldBytes),
		Prefix: cleanLine(c.Name.Prefix, MaxFieldBytes),
		Suffix: cleanLine(c.Name.Suffix, MaxFieldBytes),
	}
	out.DisplayName = cleanLine(c.DisplayName, MaxFieldBytes)
	out.Nickname = cleanLine(c.Nickname, MaxFieldBytes)
	out.Org = cleanLine(c.Org, MaxFieldBytes)
	out.Title = cleanLine(c.Title, MaxFieldBytes)
	for _, e := range c.Emails {
		if len(out.Emails) == MaxItems {
			break
		}
		if a := cleanEmail(e.Address); a != "" {
			out.Emails = append(out.Emails, Email{Label: normalizeLabel(e.Label), Address: a})
		}
	}
	for _, p := range c.Phones {
		if len(out.Phones) == MaxItems {
			break
		}
		if n := cleanPhone(p.Number); n != "" {
			out.Phones = append(out.Phones, Phone{Label: normalizeLabel(p.Label), Number: n})
		}
	}
	for _, a := range c.Addresses {
		if len(out.Addresses) == MaxItems {
			break
		}
		ca := Address{
			Label:    normalizeLabel(a.Label),
			Street:   cleanBlock(a.Street, MaxFieldBytes),
			City:     cleanLine(a.City, MaxFieldBytes),
			Region:   cleanLine(a.Region, MaxFieldBytes),
			Postcode: cleanLine(a.Postcode, MaxFieldBytes),
			Country:  cleanLine(a.Country, MaxFieldBytes),
		}
		if ca.Street != "" || ca.City != "" || ca.Region != "" || ca.Postcode != "" || ca.Country != "" {
			out.Addresses = append(out.Addresses, ca)
		}
	}
	for _, u := range c.URLs {
		if len(out.URLs) == MaxItems {
			break
		}
		if v := cleanLine(u.URL, MaxURLBytes); v != "" {
			out.URLs = append(out.URLs, URL{Label: normalizeLabel(u.Label), URL: v})
		}
	}
	out.Birthday = cleanBirthday(c.Birthday)
	out.Note = cleanBlock(c.Note, MaxNoteBytes)
	if c.Photo != nil && len(c.Photo.Data) > 0 {
		out.Photo = &Photo{MIME: cleanMIME(c.Photo.MIME), Data: c.Photo.Data}
	}
	out.PhotoURI = c.PhotoURI && out.Photo == nil
	return out
}

func cleanEmail(s string) string {
	s = cleanLine(s, MaxEmailBytes)
	for len(s) >= 7 && strings.EqualFold(s[:7], "mailto:") {
		s = strings.TrimSpace(s[7:])
	}
	return s
}

func cleanPhone(s string) string {
	s = cleanLine(s, MaxPhoneBytes)
	for len(s) >= 4 && strings.EqualFold(s[:4], "tel:") {
		s = strings.TrimSpace(s[4:])
	}
	return s
}

func cleanBirthday(b Birthday) Birthday {
	y := b.Year
	if y == 1604 {
		y = 0
	}
	if y < 0 || y > 9999 || b.Month < 1 || b.Month > 12 || b.Day < 1 || b.Day > daysIn(b.Month, y) {
		return Birthday{}
	}
	return Birthday{Year: y, Month: b.Month, Day: b.Day}
}

func daysIn(m, y int) int {
	if y == 0 {
		y = 2000 // a leap year: 29 February without a year is a birthday
	}
	return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// standard label of a type or label token (lower case), "" when none.
var labelTokens = map[string]string{
	"home": LabelHome, "work": LabelWork, "other": LabelOther,
	"mobile": LabelMobile, "cell": LabelMobile, "iphone": LabelMobile,
	"fax": LabelFax, "homefax": LabelFax, "workfax": LabelFax, "otherfax": LabelFax,
}

// isCustom reports whether a normalised label is a custom one.
func isCustom(label string) bool {
	switch label {
	case "", LabelHome, LabelWork, LabelMobile, LabelFax, LabelOther:
		return false
	}
	return true
}

// normalizeLabel cleans a label and maps the standard ones (also Apple's
// "_$!<Mobile>!$_" form) to the small set; any other text is a custom
// label. Apple's HomePage is no label.
func normalizeLabel(s string) string {
	s = strings.TrimSpace(cleanLine(s, MaxLabelBytes))
	for strings.HasPrefix(s, "_$!<") && strings.HasSuffix(s, ">!$_") && len(s) >= 8 {
		s = strings.TrimSpace(s[4 : len(s)-4])
	}
	low := strings.ToLower(s)
	if l, ok := labelTokens[low]; ok {
		return l
	}
	if low == "homepage" {
		return ""
	}
	return s
}

// cleanLine makes one line of hostile text safe to show: board.CleanText,
// every whitespace run one space, trimmed, capped at max bytes (≤ 0: none).
func cleanLine(s string, max int) string {
	// Twice: the cap can leave a joiner at the end that a second pass drops.
	return lineOnce(lineOnce(s, max), max)
}

func lineOnce(s string, max int) string {
	if s == "" {
		return ""
	}
	return strings.TrimSpace(capBytes(strings.Join(strings.Fields(board.CleanText(s)), " "), max))
}

// cleanBlock cleans multi-line text: like cleanLine but lines kept ("\n"),
// trailing whitespace of each line and blank lines at either end dropped,
// at most one blank line in a row.
func cleanBlock(s string, max int) string {
	return blockOnce(blockOnce(s, max), max)
}

func blockOnce(s string, max int) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	blank := false
	for _, line := range strings.Split(board.CleanText(s), "\n") {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if strings.TrimSpace(line) == "" {
			blank = b.Len() > 0
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
			if blank {
				b.WriteByte('\n')
			}
		} else {
			line = strings.TrimLeftFunc(line, unicode.IsSpace)
		}
		blank = false
		b.WriteString(line)
		if max > 0 && b.Len() > max {
			break
		}
	}
	return strings.TrimRightFunc(capBytes(b.String(), max), unicode.IsSpace)
}

// capBytes cuts s to at most n bytes on a rune boundary (n ≤ 0: none).
func capBytes(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	i := n
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}
