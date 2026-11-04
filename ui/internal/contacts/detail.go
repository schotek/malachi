// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contacts

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The contact's detail (W1): the heading (initials, name, "Title ·
// Company", the source line, Edit or Read Only), the actions (Write Email,
// Show Mail, Copy) and the rows of label and value in a fixed order —
// e-mail addresses, phones, postal addresses, birthday, websites,
// nickname, note. Everything is plain text: a website is opened only when
// it is http or https (OpenURL, through the client's "Open this link?"),
// a phone number only copied.

// FieldKind is a kind of labelled field of a card.
type FieldKind int

// The labelled fields.
const (
	FieldEmail FieldKind = iota
	FieldPhone
	FieldAddress
	FieldURL
)

// Labels are a field kind's label choices in the form's picker, in their
// order; "" (no label) is offered after them (NoLabel).
func Labels(k FieldKind) []api.ContactLabel {
	if k == FieldPhone {
		return []api.ContactLabel{api.ContactLabelMobile, api.ContactLabelWork, api.ContactLabelHome,
			api.ContactLabelMain, api.ContactLabelFax, api.ContactLabelPager, api.ContactLabelOther}
	}
	return []api.ContactLabel{api.ContactLabelWork, api.ContactLabelHome, api.ContactLabelOther}
}

// IsKnownLabel reports a label of Labels(k); any other non-empty label is
// the server's own (display text, shown as it is).
func IsKnownLabel(k FieldKind, l api.ContactLabel) bool {
	for _, x := range Labels(k) {
		if x == l {
			return true
		}
	}
	return false
}

// NoLabel is the picker's choice of no label.
func NoLabel(tr Translator) string {
	// TRANSLATORS: the contact form's label choice for an e-mail address,
	// phone or address without a label.
	return tr.T("No Label")
}

// LabelText is a label in the form's picker ("work ▾"): a known label
// translated by kind, the server's own cleaned, "" NoLabel.
func LabelText(k FieldKind, l api.ContactLabel, tr Translator) string {
	if l == "" {
		return NoLabel(tr)
	}
	if !IsKnownLabel(k, l) {
		return Clean(string(l), api.MaxContactLabelBytes)
	}
	switch k {
	case FieldEmail:
		switch l {
		case api.ContactLabelWork:
			// TRANSLATORS: the label of a work e-mail address in the
			// contact form (lower case, like the other labels).
			return tr.C("email label", "work")
		case api.ContactLabelHome:
			// TRANSLATORS: the label of a personal e-mail address.
			return tr.C("email label", "home")
		}
		return tr.C("email label", "other")
	case FieldPhone:
		return phoneLabel(l, tr)
	case FieldAddress:
		switch l {
		case api.ContactLabelWork:
			// TRANSLATORS: the label of a postal address at work.
			return tr.C("address label", "work")
		case api.ContactLabelHome:
			return tr.C("address label", "home")
		}
		return tr.C("address label", "other")
	}
	switch l {
	case api.ContactLabelWork:
		// TRANSLATORS: the label of a work website in the contact form.
		return tr.C("url label", "work")
	case api.ContactLabelHome:
		return tr.C("url label", "home")
	}
	return tr.C("url label", "other")
}

func phoneLabel(l api.ContactLabel, tr Translator) string {
	switch l {
	case api.ContactLabelMobile:
		// TRANSLATORS: the label of a mobile phone number.
		return tr.C("phone label", "mobile")
	case api.ContactLabelWork:
		return tr.C("phone label", "work")
	case api.ContactLabelHome:
		return tr.C("phone label", "home")
	case api.ContactLabelMain:
		// TRANSLATORS: the label of a company's main phone number.
		return tr.C("phone label", "main")
	case api.ContactLabelFax:
		return tr.C("phone label", "fax")
	case api.ContactLabelPager:
		return tr.C("phone label", "pager")
	}
	return tr.C("phone label", "other")
}

// DetailLabel is a labelled row's label in the detail ("work email",
// "mobile", "website"); the server's own label is shown as it is.
func DetailLabel(k FieldKind, l api.ContactLabel, tr Translator) string {
	if l != "" && !IsKnownLabel(k, l) {
		return Clean(string(l), api.MaxContactLabelBytes)
	}
	switch k {
	case FieldEmail:
		switch l {
		case api.ContactLabelWork:
			// TRANSLATORS: a row label of the contact detail (lower case).
			return tr.T("work email")
		case api.ContactLabelHome:
			return tr.T("home email")
		case api.ContactLabelOther:
			return tr.T("other email")
		}
		// TRANSLATORS: a row label of the contact detail: an e-mail address
		// without a label.
		return tr.T("email")
	case FieldPhone:
		if l == "" {
			// TRANSLATORS: a row label of the contact detail: a phone
			// number without a label.
			return tr.T("phone")
		}
		return phoneLabel(l, tr)
	case FieldAddress:
		if l == "" {
			// TRANSLATORS: a row label of the contact detail: a postal
			// address without a label.
			return tr.T("address")
		}
		return LabelText(k, l, tr)
	}
	switch l {
	case api.ContactLabelWork:
		return tr.T("work website")
	case api.ContactLabelHome:
		return tr.T("home website")
	}
	// TRANSLATORS: a row label of the contact detail: a web address.
	return tr.T("website")
}

// RowKind is what a detail row shows.
type RowKind int

// The detail rows, in their order.
const (
	RowEmail RowKind = iota
	RowPhone
	RowAddress
	RowBirthday
	RowURL
	RowNickname
	RowNote
)

// DetailRow is one row of the detail: label and value, plain text.
type DetailRow struct {
	Kind  RowKind
	Label string
	Value string
	// Address is a RowEmail's address (Write Email to it).
	Address string
	// URL is a RowURL's address to open (OpenURL); "" when it cannot be
	// opened (then the value is only text).
	URL string
}

// Detail is the detail of a contact.
type Detail struct {
	Title    string
	Subtitle string // "Job · Company", "" none
	Initials string
	Rows     []DetailRow
	// ReadOnly: the Read Only capsule instead of Edit.
	ReadOnly bool
	Actions  Actions
}

// Actions are the detail's buttons.
type Actions struct {
	// WriteEmail: the contact has an address; with several, Emails are
	// the choices (a menu).
	WriteEmail bool
	Emails     []DetailRow
	// ShowMail is the search of Show Mail ("" none: no address).
	ShowMail string
	// Edit and Delete: the book can be changed.
	Edit, Delete bool
}

// BirthdayText is a birthday: "14 March 1980", without a year "14 March";
// "" for an invalid one.
func BirthdayText(b api.ContactBirthday, format Strftime, tr Translator) string {
	if !ValidBirthday(b) {
		return ""
	}
	if b.Year == 0 {
		t := time.Date(2000, time.Month(b.Month), b.Day, 12, 0, 0, 0, time.UTC)
		// TRANSLATORS: strftime format of a birthday without a year, e.g.
		// "14 March".
		// xgettext:no-c-format
		return format(t, tr.T("%-d %B"))
	}
	t := time.Date(b.Year, time.Month(b.Month), b.Day, 12, 0, 0, 0, time.UTC)
	// TRANSLATORS: strftime format of a birthday with its year, e.g. "14
	// March 1980".
	// xgettext:no-c-format
	return format(t, tr.T("%-d %B %Y"))
}

// ValidBirthday reports a real date (Year 0 = unknown; 29 February
// without a year is valid).
func ValidBirthday(b api.ContactBirthday) bool {
	if b.Month < 1 || b.Month > 12 || b.Day < 1 || b.Year < 0 || b.Year > 9999 {
		return false
	}
	y := b.Year
	if y == 0 {
		y = 2000
	}
	t := time.Date(y, time.Month(b.Month), b.Day, 0, 0, 0, 0, time.UTC)
	return t.Day() == b.Day && int(t.Month()) == b.Month
}

// AddressLine is a postal address on one line: "Dlouhá 12, 110 00 Praha
// 1, Czechia" (street lines, postcode and city, region, country).
func AddressLine(a api.ContactAddress) string {
	var parts []string
	for _, l := range strings.Split(a.Street, "\n") {
		if l = Clean(l, api.MaxContactFieldBytes); l != "" {
			parts = append(parts, l)
		}
	}
	city := strings.TrimSpace(Clean(a.Postcode, api.MaxContactFieldBytes) + " " + Clean(a.City, api.MaxContactFieldBytes))
	for _, p := range []string{city, Clean(a.Region, api.MaxContactFieldBytes), Clean(a.Country, api.MaxContactFieldBytes)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

// OpenURL is the address a website row opens: an http or https URL with a
// host, a bare host ("example.org/x") as https; "" anything else.
func OpenURL(raw string) string {
	s := strings.TrimSpace(Clean(raw, api.MaxContactFieldBytes))
	if s == "" || strings.ContainsAny(s, " \t") {
		return ""
	}
	if !strings.Contains(s, "://") {
		if strings.Contains(s, ":") || strings.HasPrefix(s, "/") || !strings.Contains(s, ".") {
			return ""
		}
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}

// Title is a contact's name in a list or heading: the display name, else
// the structured name, the company, the first address, NoName.
func Title(c api.ContactCard, tr Translator) string {
	if n := Clean(c.DisplayName, api.MaxContactNameBytes); n != "" {
		return n
	}
	if n := ComposeName(c.Name); n != "" {
		return n
	}
	if o := Clean(c.Org, api.MaxContactFieldBytes); o != "" {
		return o
	}
	for _, e := range c.Emails {
		if a := Clean(e.Address, api.MaxContactFieldBytes); a != "" {
			return a
		}
	}
	return NoName(tr)
}

// SummaryTitle is a list row's name (ContactSummary): the display name,
// the company, the address, NoName.
func SummaryTitle(s api.ContactSummary, tr Translator) string {
	for _, v := range []string{s.DisplayName, s.Org, s.Email} {
		if v = Clean(v, api.MaxContactFieldBytes); v != "" {
			return v
		}
	}
	return NoName(tr)
}

// ComposeName is the structured name in the usual order: prefix, given,
// middle, family, suffix.
func ComposeName(n api.ContactName) string {
	var parts []string
	for _, p := range []string{n.Prefix, n.Given, n.Middle, n.Family, n.Suffix} {
		if p = Clean(p, api.MaxContactNameBytes); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// Initials are the avatar's letters: the first letters of the first and
// the last word of name (one for one word), upper case; else the first
// letter of the address; "" none.
func Initials(name, address string) string {
	var words []string
	for _, w := range strings.Fields(Clean(name, api.MaxContactNameBytes)) {
		if r := firstLetter(w); r != 0 {
			words = append(words, string(unicode.ToUpper(r)))
		}
	}
	switch len(words) {
	case 0:
		if r := firstLetter(address); r != 0 {
			return string(unicode.ToUpper(r))
		}
		return ""
	case 1:
		return words[0]
	}
	return words[0] + words[len(words)-1]
}

func firstLetter(s string) rune {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
	}
	return 0
}

// DetailOf is the detail of card. format writes the birthday.
func DetailOf(card api.ContactCard, format Strftime, tr Translator) Detail {
	d := Detail{Title: Title(card, tr), ReadOnly: card.ReadOnly}
	title, org := Clean(card.Title, api.MaxContactFieldBytes), Clean(card.Org, api.MaxContactFieldBytes)
	switch {
	case title != "" && org != "":
		d.Subtitle = TwoParts(title, org, tr)
	case title != "":
		d.Subtitle = title
	default:
		d.Subtitle = org
	}
	if d.Subtitle == d.Title {
		d.Subtitle = ""
	}
	first := ""
	for _, e := range card.Emails {
		a := Clean(e.Address, api.MaxContactFieldBytes)
		if a == "" {
			continue
		}
		if first == "" {
			first = a
		}
		r := DetailRow{Kind: RowEmail, Label: DetailLabel(FieldEmail, e.Label, tr), Value: a, Address: a}
		d.Rows = append(d.Rows, r)
		d.Actions.Emails = append(d.Actions.Emails, r)
	}
	d.Initials = Initials(d.Title, first)
	if d.Title == NoName(tr) {
		d.Initials = Initials("", first)
	}
	for _, p := range card.Phones {
		if n := Clean(p.Number, api.MaxContactFieldBytes); n != "" {
			d.Rows = append(d.Rows, DetailRow{Kind: RowPhone, Label: DetailLabel(FieldPhone, p.Label, tr), Value: n})
		}
	}
	for _, a := range card.Addresses {
		if l := AddressLine(a); l != "" {
			d.Rows = append(d.Rows, DetailRow{Kind: RowAddress, Label: DetailLabel(FieldAddress, a.Label, tr), Value: l})
		}
	}
	if card.Birthday != nil {
		if t := BirthdayText(*card.Birthday, format, tr); t != "" {
			// TRANSLATORS: a row label of the contact detail.
			d.Rows = append(d.Rows, DetailRow{Kind: RowBirthday, Label: tr.T("birthday"), Value: t})
		}
	}
	for _, u := range card.URLs {
		if v := Clean(u.URL, api.MaxContactFieldBytes); v != "" {
			d.Rows = append(d.Rows, DetailRow{Kind: RowURL, Label: DetailLabel(FieldURL, u.Label, tr), Value: v, URL: OpenURL(v)})
		}
	}
	if n := Clean(card.Nickname, api.MaxContactNameBytes); n != "" {
		// TRANSLATORS: a row label of the contact detail.
		d.Rows = append(d.Rows, DetailRow{Kind: RowNickname, Label: tr.T("nickname"), Value: n})
	}
	if n := CleanBlock(card.Note, api.MaxContactNoteBytes); n != "" {
		// TRANSLATORS: a row label of the contact detail.
		d.Rows = append(d.Rows, DetailRow{Kind: RowNote, Label: tr.T("note"), Value: n})
	}
	d.Actions.WriteEmail = first != ""
	if first != "" {
		d.Actions.ShowMail = ShowMailQuery(first)
	}
	if len(d.Actions.Emails) < 2 {
		d.Actions.Emails = nil
	}
	d.Actions.Edit = !card.ReadOnly
	d.Actions.Delete = !card.ReadOnly
	return d
}

// ShowMailQuery is the search of Show Mail for an address, in the syntax
// of search.query (backend/internal/search): from:address, the value in
// straight quotes when it has a space; the quote characters the parser
// reads („ “ ” ") and control characters are dropped. "" for an empty
// address.
func ShowMailQuery(address string) string {
	v := strings.Map(func(r rune) rune {
		switch {
		case r == '"' || r == '„' || r == '“' || r == '”':
			return -1
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, address)
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return ""
	}
	if strings.Contains(v, " ") {
		return `from:"` + v + `"`
	}
	return "from:" + v
}

// CopyText is Copy: the contact as plain text, one line per row — the
// name, "Job · Company", then "label: value" (a note's lines follow its
// label). Never vCard syntax: it is for pasting into a message.
func CopyText(card api.ContactCard, format Strftime, tr Translator) string {
	d := DetailOf(card, format, tr)
	lines := []string{d.Title}
	if d.Subtitle != "" {
		lines = append(lines, d.Subtitle)
	}
	for _, r := range d.Rows {
		lines = append(lines, fmt.Sprintf("%s: %s", r.Label, r.Value))
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Cleaning
// ---------------------------------------------------------------------------

// Clean is a string of a server as one line of display text: control,
// bidi and invisible format characters dropped, white space runs as one
// space, trimmed, cut to max bytes on a character boundary (max ≤ 0: no
// cut).
func Clean(s string, max int) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == utf8.RuneError, isBidiOrInvisible(r):
			continue
		case unicode.IsSpace(r) || unicode.IsControl(r):
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return cut(b.String(), max)
}

// CleanBlock is Clean that keeps line breaks (a note): every line cleaned,
// at most two empty lines in a row, trimmed.
func CleanBlock(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var out []string
	empty := 0
	for _, l := range strings.Split(s, "\n") {
		l = Clean(l, 0)
		if l == "" {
			empty++
			if empty > 2 {
				continue
			}
		} else {
			empty = 0
		}
		out = append(out, l)
	}
	return cut(strings.TrimSpace(strings.Join(out, "\n")), max)
}

func isBidiOrInvisible(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F, r == 0x061C:
		return true
	case r == 0xFEFF, r >= 0x200B && r <= 0x200D, r == 0x2060, r == 0x00AD:
		return true
	}
	return unicode.Is(unicode.Cf, r)
}

func cut(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	i := max
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return strings.TrimSpace(s[:i])
}
