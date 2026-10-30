// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contacts

import (
	"fmt"
	"net/mail"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The contact form (W4): edited on the detail's card, saved to the server
// with Done. The form keeps the card it was opened from (Original) and
// knows which fields the user changed (Changed), so that Done sends a
// card the daemon turns into a patch, a contactConflict reloads the
// server's version under the user's changes (Rebase), and closing an
// unchanged form asks nothing (Dirty).

// FieldKey names a part of the form for dirty tracking, rebase and
// validation problems.
type FieldKey int

// The parts.
const (
	KeyGiven FieldKey = iota
	KeyFamily
	KeyMiddle
	KeyPrefix
	KeySuffix
	KeyNickname
	KeyOrg
	KeyTitle
	KeyEmails
	KeyPhones
	KeyAddresses
	KeyURLs
	KeyBirthday
	KeyNote
	keyCount
)

// LabelledValue is an e-mail address, phone or website row of the form.
type LabelledValue struct {
	Label api.ContactLabel
	Value string
}

// Form is the state of the contact form.
type Form struct {
	// Book is where a new contact is created (ignored for an update).
	Book api.AddressBookID

	Given, Family, Middle, Prefix, Suffix string
	Nickname, Org, Title                  string
	Emails                                []LabelledValue
	Phones                                []LabelledValue
	Addresses                             []api.ContactAddress
	URLs                                  []LabelledValue
	// Birthday nil = none.
	Birthday *api.ContactBirthday
	Note     string

	// Original is the card the form was opened from (zero for a new
	// contact); its DisplayName stays when no name part changed.
	Original api.ContactCard
	// IsNew: Done creates (contact.create in Book).
	IsNew bool
	// Kind is the kind of Book (the controller sets it before Validate):
	// a Microsoft book has Graph's limits (GraphMaxEmails).
	Kind api.AddressBookKind
	base Form0
}

// Form0 is the comparable snapshot of a form's fields (what Changed
// compares).
type Form0 struct {
	Strings   [8]string
	Emails    []LabelledValue
	Phones    []LabelledValue
	Addresses []api.ContactAddress
	URLs      []LabelledValue
	Birthday  *api.ContactBirthday
	Note      string
}

// NewForm is the form of a new contact in book.
func NewForm(book api.AddressBookID) *Form {
	f := &Form{Book: book, IsNew: true}
	f.base = f.snapshot()
	return f
}

// EditForm is the form of an existing card.
func EditForm(card api.ContactCard) *Form {
	f := &Form{Book: card.BookID, Original: card}
	f.load(card)
	f.base = f.snapshot()
	return f
}

func (f *Form) load(c api.ContactCard) {
	f.Given, f.Family, f.Middle = c.Name.Given, c.Name.Family, c.Name.Middle
	f.Prefix, f.Suffix = c.Name.Prefix, c.Name.Suffix
	f.Nickname, f.Org, f.Title, f.Note = c.Nickname, c.Org, c.Title, c.Note
	f.Emails = nil
	for _, e := range c.Emails {
		f.Emails = append(f.Emails, LabelledValue{e.Label, e.Address})
	}
	f.Phones = nil
	for _, p := range c.Phones {
		f.Phones = append(f.Phones, LabelledValue{p.Label, p.Number})
	}
	f.Addresses = slices.Clone(c.Addresses)
	f.URLs = nil
	for _, u := range c.URLs {
		f.URLs = append(f.URLs, LabelledValue{u.Label, u.URL})
	}
	f.Birthday = nil
	if c.Birthday != nil {
		b := *c.Birthday
		f.Birthday = &b
	}
	if f.Given == "" && f.Family == "" && f.Middle == "" && f.Prefix == "" && f.Suffix == "" && c.DisplayName != "" {
		// A card with only a display name (a server's FN): the form shows
		// it split, so that it can be edited.
		f.Given, f.Family = SplitName(c.DisplayName)
	}
}

func (f *Form) snapshot() Form0 {
	s := Form0{
		Strings:   [8]string{f.Given, f.Family, f.Middle, f.Prefix, f.Suffix, f.Nickname, f.Org, f.Title},
		Emails:    slices.Clone(f.Emails),
		Phones:    slices.Clone(f.Phones),
		Addresses: slices.Clone(f.Addresses),
		URLs:      slices.Clone(f.URLs),
		Note:      f.Note,
	}
	if f.Birthday != nil {
		b := *f.Birthday
		s.Birthday = &b
	}
	return s
}

// Changed are the parts the user changed since the form opened (or was
// rebased), compared after trimming, empty rows left out.
func (f *Form) Changed() []FieldKey {
	cur, base := normal(f.snapshot()), normal(f.base)
	var out []FieldKey
	for i := range 8 {
		if cur.Strings[i] != base.Strings[i] {
			out = append(out, FieldKey(i))
		}
	}
	if !reflect.DeepEqual(cur.Emails, base.Emails) {
		out = append(out, KeyEmails)
	}
	if !reflect.DeepEqual(cur.Phones, base.Phones) {
		out = append(out, KeyPhones)
	}
	if !reflect.DeepEqual(cur.Addresses, base.Addresses) {
		out = append(out, KeyAddresses)
	}
	if !reflect.DeepEqual(cur.URLs, base.URLs) {
		out = append(out, KeyURLs)
	}
	if !reflect.DeepEqual(cur.Birthday, base.Birthday) {
		out = append(out, KeyBirthday)
	}
	if cur.Note != base.Note {
		out = append(out, KeyNote)
	}
	return out
}

// Dirty reports a change (closing the form asks; Done of an unchanged
// existing contact sends nothing).
func (f *Form) Dirty() bool { return len(f.Changed()) > 0 }

// normal is a snapshot as it would be saved: strings trimmed, empty rows
// dropped, empty lists nil.
func normal(s Form0) Form0 {
	for i := range s.Strings {
		s.Strings[i] = strings.TrimSpace(s.Strings[i])
	}
	s.Emails, s.Phones, s.URLs = normalRows(s.Emails), normalRows(s.Phones), normalRows(s.URLs)
	var as []api.ContactAddress
	for _, a := range s.Addresses {
		a = trimAddress(a)
		if !emptyAddress(a) {
			as = append(as, a)
		}
	}
	s.Addresses = as
	s.Note = strings.TrimSpace(s.Note)
	return s
}

func normalRows(rs []LabelledValue) []LabelledValue {
	var out []LabelledValue
	for _, r := range rs {
		if v := strings.TrimSpace(r.Value); v != "" {
			out = append(out, LabelledValue{r.Label, v})
		}
	}
	return out
}

func trimAddress(a api.ContactAddress) api.ContactAddress {
	a.Street = strings.TrimSpace(a.Street)
	a.City, a.Region = strings.TrimSpace(a.City), strings.TrimSpace(a.Region)
	a.Postcode, a.Country = strings.TrimSpace(a.Postcode), strings.TrimSpace(a.Country)
	return a
}

func emptyAddress(a api.ContactAddress) bool {
	return a.Street == "" && a.City == "" && a.Region == "" && a.Postcode == "" && a.Country == ""
}

// Card is the card Done sends (contact.create's or contact.update's):
// trimmed, empty rows left out; the display name composed from the name
// parts when one of them changed or the original had none, else the
// original's.
func (f *Form) Card() api.ContactCard {
	s := normal(f.snapshot())
	c := api.ContactCard{
		ID: f.Original.ID, BookID: f.Original.BookID, Revision: f.Original.Revision,
		Name: api.ContactName{Given: s.Strings[KeyGiven], Family: s.Strings[KeyFamily], Middle: s.Strings[KeyMiddle],
			Prefix: s.Strings[KeyPrefix], Suffix: s.Strings[KeySuffix]},
		Nickname: s.Strings[KeyNickname], Org: s.Strings[KeyOrg], Title: s.Strings[KeyTitle],
		Emails: []api.ContactEmail{}, Phones: []api.ContactPhone{}, Addresses: []api.ContactAddress{}, URLs: []api.ContactURL{},
		Birthday: s.Birthday, Note: s.Note,
	}
	if f.IsNew {
		c.BookID = f.Book
	}
	for _, e := range s.Emails {
		c.Emails = append(c.Emails, api.ContactEmail{Label: e.Label, Address: e.Value})
	}
	for _, p := range s.Phones {
		c.Phones = append(c.Phones, api.ContactPhone{Label: p.Label, Number: p.Value})
	}
	c.Addresses = append(c.Addresses, s.Addresses...)
	for _, u := range s.URLs {
		c.URLs = append(c.URLs, api.ContactURL{Label: u.Label, URL: u.Value})
	}
	nameChanged := slices.ContainsFunc(f.Changed(), func(k FieldKey) bool { return k <= KeySuffix })
	if !f.IsNew && !nameChanged && strings.TrimSpace(f.Original.DisplayName) != "" {
		// Unchanged: the original's name as it was, so the patch leaves it
		// alone (a server's card with only a display name stays so).
		c.Name, c.DisplayName = f.Original.Name, f.Original.DisplayName
	} else {
		c.DisplayName = ComposeName(c.Name)
		if c.DisplayName == "" {
			c.DisplayName = c.Org
		}
		if c.DisplayName == "" && len(c.Emails) > 0 {
			c.DisplayName = c.Emails[0].Address
		}
	}
	return c
}

// Rebase is a contactConflict's Reload: the form shows server, the
// server's version, and the parts the user changed keep the user's values
// (a list as a whole); afterwards server is the original, so Done updates
// it with its revision.
func (f *Form) Rebase(server api.ContactCard) {
	changed := f.Changed()
	mine := *f
	mine.Emails, mine.Phones = slices.Clone(f.Emails), slices.Clone(f.Phones)
	mine.Addresses, mine.URLs = slices.Clone(f.Addresses), slices.Clone(f.URLs)
	f.Original = server
	f.load(server)
	f.base = f.snapshot()
	for _, k := range changed {
		switch k {
		case KeyGiven:
			f.Given = mine.Given
		case KeyFamily:
			f.Family = mine.Family
		case KeyMiddle:
			f.Middle = mine.Middle
		case KeyPrefix:
			f.Prefix = mine.Prefix
		case KeySuffix:
			f.Suffix = mine.Suffix
		case KeyNickname:
			f.Nickname = mine.Nickname
		case KeyOrg:
			f.Org = mine.Org
		case KeyTitle:
			f.Title = mine.Title
		case KeyEmails:
			f.Emails = mine.Emails
		case KeyPhones:
			f.Phones = mine.Phones
		case KeyAddresses:
			f.Addresses = mine.Addresses
		case KeyURLs:
			f.URLs = mine.URLs
		case KeyBirthday:
			f.Birthday = mine.Birthday
		case KeyNote:
			f.Note = mine.Note
		}
	}
}

// Saved takes the answer of Done: the card as sent with its new id and
// revision becomes the original, the form unchanged.
func (f *Form) Saved(id api.ContactID, revision string) {
	c := f.Card()
	if id != "" {
		c.ID = id
	}
	c.Revision = revision
	f.Original = c
	f.IsNew = false
	f.base = f.snapshot()
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

// MaxRows is the limit of rows of a kind (the API's).
func MaxRows(k FieldKind) int {
	switch k {
	case FieldEmail:
		return api.MaxContactEmails
	case FieldPhone:
		return api.MaxContactPhones
	case FieldAddress:
		return api.MaxContactAddresses
	}
	return api.MaxContactURLs
}

// Count is the number of rows of a kind.
func (f *Form) Count(k FieldKind) int {
	switch k {
	case FieldEmail:
		return len(f.Emails)
	case FieldPhone:
		return len(f.Phones)
	case FieldAddress:
		return len(f.Addresses)
	}
	return len(f.URLs)
}

// CanAdd reports that another row of the kind fits.
func (f *Form) CanAdd(k FieldKind) bool { return f.Count(k) < MaxRows(k) }

// NextLabel is the label of a new row: the first of Labels(k) no row of
// the kind has, else other.
func (f *Form) NextLabel(k FieldKind) api.ContactLabel {
	used := map[api.ContactLabel]bool{}
	switch k {
	case FieldEmail:
		for _, r := range f.Emails {
			used[r.Label] = true
		}
	case FieldPhone:
		for _, r := range f.Phones {
			used[r.Label] = true
		}
	case FieldAddress:
		for _, r := range f.Addresses {
			used[r.Label] = true
		}
	default:
		for _, r := range f.URLs {
			used[r.Label] = true
		}
	}
	for _, l := range Labels(k) {
		if !used[l] {
			return l
		}
	}
	return api.ContactLabelOther
}

// AddRow appends an empty row of the kind (NextLabel) and returns its
// index; -1 when the limit is reached.
func (f *Form) AddRow(k FieldKind) int {
	if !f.CanAdd(k) {
		return -1
	}
	l := f.NextLabel(k)
	switch k {
	case FieldEmail:
		f.Emails = append(f.Emails, LabelledValue{Label: l})
	case FieldPhone:
		f.Phones = append(f.Phones, LabelledValue{Label: l})
	case FieldAddress:
		f.Addresses = append(f.Addresses, api.ContactAddress{Label: l})
	default:
		f.URLs = append(f.URLs, LabelledValue{Label: l})
	}
	return f.Count(k) - 1
}

// RemoveRow removes a row of the kind; an index out of range does
// nothing.
func (f *Form) RemoveRow(k FieldKind, i int) {
	if i < 0 || i >= f.Count(k) {
		return
	}
	switch k {
	case FieldEmail:
		f.Emails = slices.Delete(f.Emails, i, i+1)
	case FieldPhone:
		f.Phones = slices.Delete(f.Phones, i, i+1)
	case FieldAddress:
		f.Addresses = slices.Delete(f.Addresses, i, i+1)
	default:
		f.URLs = slices.Delete(f.URLs, i, i+1)
	}
}

// LabelChoices are the picker's choices for a row with label current:
// Labels(k), no label, and current when it is the server's own.
func LabelChoices(k FieldKind, current api.ContactLabel) []api.ContactLabel {
	out := append(Labels(k), "")
	if current != "" && !IsKnownLabel(k, current) {
		out = append(out, current)
	}
	return out
}

// AddRowText is the button under the rows of a kind ("Add Email").
func AddRowText(k FieldKind, tr Translator) string {
	switch k {
	case FieldEmail:
		// TRANSLATORS: the contact form's button that adds an e-mail
		// address row.
		return tr.T("Add Email")
	case FieldPhone:
		return tr.T("Add Phone")
	case FieldAddress:
		return tr.T("Add Address")
	}
	return tr.T("Add Website")
}

// RemoveRowText is the tooltip (and accessible name) of a row's remove
// button.
func RemoveRowText(tr Translator) string { return tr.T("Remove") }

// AddBirthday is the button that shows the birthday row.
func AddBirthday(tr Translator) string { return tr.T("Add Birthday") }

// Placeholder is a form field's placeholder.
func Placeholder(k FieldKey, tr Translator) string {
	switch k {
	case KeyGiven:
		// TRANSLATORS: placeholders of the contact form's fields.
		return tr.T("First Name")
	case KeyFamily:
		return tr.T("Last Name")
	case KeyMiddle:
		return tr.T("Middle Name")
	case KeyPrefix:
		// TRANSLATORS: a title before a name, such as "Ing." or "Dr.".
		return tr.T("Prefix")
	case KeySuffix:
		// TRANSLATORS: a title after a name, such as "Ph.D." or "Jr.".
		return tr.T("Suffix")
	case KeyNickname:
		return tr.T("Nickname")
	case KeyOrg:
		return tr.T("Company")
	case KeyTitle:
		return tr.T("Job Title")
	case KeyEmails:
		return tr.T("Email")
	case KeyPhones:
		return tr.T("Phone")
	case KeyURLs:
		return tr.T("Website")
	case KeyBirthday:
		return tr.T("Birthday")
	case KeyNote:
		return tr.T("Note")
	}
	return ""
}

// AddressPart is a part of a postal address in the form.
type AddressPart int

// The parts, in the form's order.
const (
	PartStreet AddressPart = iota
	PartPostcode
	PartCity
	PartRegion
	PartCountry
)

// AddressPlaceholder is an address part's placeholder.
func AddressPlaceholder(p AddressPart, tr Translator) string {
	switch p {
	case PartStreet:
		return tr.T("Street")
	case PartPostcode:
		return tr.T("Postal Code")
	case PartCity:
		return tr.T("City")
	case PartRegion:
		// TRANSLATORS: a part of a postal address: state, province or
		// region.
		return tr.T("Region")
	}
	return tr.T("Country")
}

// BookLabel labels the form's and Add to Contacts' choice of book.
func BookLabel(tr Translator) string {
	// TRANSLATORS: the label of the choice where a new contact is saved.
	return tr.T("Address Book")
}

// NameLabel and EmailLabel label Add to Contacts' fields.
func NameLabel(tr Translator) string {
	// TRANSLATORS: the label of a new contact's name in Add to Contacts.
	return tr.T("Name")
}

// EmailLabel labels Add to Contacts' address.
func EmailLabel(tr Translator) string { return Placeholder(KeyEmails, tr) }

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// Problem is a reason Done cannot save: the part, the row (-1 for a part
// without rows) and the sentence.
type Problem struct {
	Key   FieldKey
	Index int
	Text  string
}

// Validate is the form's problems, none when Done may save: a name, a
// company or an address at all; valid e-mail addresses; websites with no
// scheme or http/https; a real birthday; the API's limits.
func (f *Form) Validate(tr Translator) []Problem {
	c := f.Card()
	var out []Problem
	if ComposeName(c.Name) == "" && c.Org == "" && len(c.Emails) == 0 && c.Nickname == "" {
		out = append(out, Problem{Key: KeyGiven, Index: -1, Text: tr.T("Enter a name, a company or an email address.")})
	}
	long := tr.T("This text is too long.")
	for i, s := range []string{c.Name.Given, c.Name.Family, c.Name.Middle, c.Name.Prefix, c.Name.Suffix, c.Nickname} {
		if len(s) > api.MaxContactNameBytes {
			out = append(out, Problem{Key: FieldKey(i), Index: -1, Text: long})
		}
	}
	if len(c.Org) > api.MaxContactFieldBytes {
		out = append(out, Problem{Key: KeyOrg, Index: -1, Text: long})
	}
	if len(c.Title) > api.MaxContactFieldBytes {
		out = append(out, Problem{Key: KeyTitle, Index: -1, Text: long})
	}
	for i, r := range f.Emails {
		v := strings.TrimSpace(r.Value)
		if v == "" {
			continue
		}
		if !ValidEmail(v) {
			// TRANSLATORS: %s is what the user typed as an e-mail address.
			out = append(out, Problem{Key: KeyEmails, Index: i, Text: fmt.Sprintf(tr.T("%s is not a valid email address."), Clean(v, 80))})
		} else if len(v) > api.MaxContactFieldBytes || len(r.Label) > api.MaxContactLabelBytes {
			out = append(out, Problem{Key: KeyEmails, Index: i, Text: long})
		}
	}
	for i, r := range f.Phones {
		if len(strings.TrimSpace(r.Value)) > api.MaxContactFieldBytes || len(r.Label) > api.MaxContactLabelBytes {
			out = append(out, Problem{Key: KeyPhones, Index: i, Text: long})
		}
	}
	for i, a := range f.Addresses {
		a = trimAddress(a)
		for _, s := range []string{a.Street, a.City, a.Region, a.Postcode, a.Country} {
			if len(s) > api.MaxContactFieldBytes {
				out = append(out, Problem{Key: KeyAddresses, Index: i, Text: long})
				break
			}
		}
	}
	for i, r := range f.URLs {
		v := strings.TrimSpace(r.Value)
		if v == "" {
			continue
		}
		if !ValidURL(v) {
			out = append(out, Problem{Key: KeyURLs, Index: i, Text: tr.T("A website must be an address starting with http:// or https://.")})
		} else if len(v) > api.MaxContactFieldBytes {
			out = append(out, Problem{Key: KeyURLs, Index: i, Text: long})
		}
	}
	if f.Birthday != nil && !ValidBirthday(*f.Birthday) {
		out = append(out, Problem{Key: KeyBirthday, Index: -1, Text: tr.T("Enter a valid date.")})
	}
	if len(c.Note) > api.MaxContactNoteBytes {
		out = append(out, Problem{Key: KeyNote, Index: -1, Text: long})
	}
	if f.Kind == api.AddressBookKindGraph {
		out = append(out, f.graphProblems(tr)...)
	}
	return out
}

// Graph's limits of a Microsoft contact (docs/api.md contact.update): at
// most GraphMaxEmails e-mail addresses, one postal address each of work,
// home and other (any other label is other), one website.
const GraphMaxEmails = 3

// graphProblems are the rows a Microsoft contact cannot hold; Index is
// the row of the form (empty rows are not counted, as Card leaves them
// out).
func (f *Form) graphProblems(tr Translator) []Problem {
	var out []Problem
	n := 0
	for i, r := range f.Emails {
		if strings.TrimSpace(r.Value) == "" {
			continue
		}
		if n++; n == GraphMaxEmails+1 {
			out = append(out, Problem{Key: KeyEmails, Index: i, Text: fmt.Sprintf(tr.N(
				// TRANSLATORS: Microsoft 365 is a brand.
				"A Microsoft 365 contact has at most %d email address.",
				"A Microsoft 365 contact has at most %d email addresses.", GraphMaxEmails), GraphMaxEmails)})
		}
	}
	slots := map[api.ContactLabel]bool{}
	for i, a := range f.Addresses {
		if emptyAddress(trimAddress(a)) {
			continue
		}
		slot := api.ContactLabelOther
		if a.Label == api.ContactLabelWork || a.Label == api.ContactLabelHome {
			slot = a.Label
		}
		if slots[slot] {
			out = append(out, Problem{Key: KeyAddresses, Index: i,
				Text: tr.T("A Microsoft 365 contact has one work, one home and one other address.")})
		}
		slots[slot] = true
	}
	n = 0
	for i, r := range f.URLs {
		if strings.TrimSpace(r.Value) == "" {
			continue
		}
		if n++; n == 2 {
			out = append(out, Problem{Key: KeyURLs, Index: i, Text: tr.T("A Microsoft 365 contact has only one website.")})
		}
	}
	return out
}

// ProblemOf reads the field of a refused card from the daemon's
// invalidArgument: its message starts with the field's path
// ("card.emails[2].address: …", docs/api.md contact.create). The problem
// marks that field of the form (Index -1 for a whole list or a field
// without rows) with RefusedText; false when the error is another or
// names no field the form has (the caller then says ErrorText).
func ProblemOf(e *api.Error, tr Translator) (Problem, bool) {
	if e == nil || e.Code != api.CodeInvalidArgument {
		return Problem{}, false
	}
	path, _, ok := strings.Cut(e.Message, ":")
	path, ok2 := strings.CutPrefix(strings.TrimSpace(path), "card.")
	if !ok || !ok2 {
		return Problem{}, false
	}
	head, _, _ := strings.Cut(path, ".")
	index := -1
	if name, rest, ok := strings.Cut(head, "["); ok {
		n, err := strconv.Atoi(strings.TrimSuffix(rest, "]"))
		if err != nil || n < 0 || !strings.HasSuffix(rest, "]") {
			return Problem{}, false
		}
		head, index = name, n
	}
	keys := map[string]FieldKey{
		"emails": KeyEmails, "phones": KeyPhones, "addresses": KeyAddresses, "urls": KeyURLs,
		"nickname": KeyNickname, "org": KeyOrg, "title": KeyTitle, "note": KeyNote,
		"birthday": KeyBirthday, "displayName": KeyGiven,
	}
	key, ok := keys[head]
	if head == "name" {
		parts := map[string]FieldKey{"given": KeyGiven, "family": KeyFamily, "middle": KeyMiddle,
			"prefix": KeyPrefix, "suffix": KeySuffix}
		_, part, _ := strings.Cut(path, ".")
		key, ok = parts[part]
	}
	if !ok {
		return Problem{}, false
	}
	return Problem{Key: key, Index: index, Text: RefusedText(tr)}, true
}

// RefusedText is the sentence over the field the server refused.
func RefusedText(tr Translator) string {
	return tr.T("The server did not accept the marked detail. Correct it and try again.")
}

// ValidEmail reports a bare e-mail address (no name, no angle brackets),
// as the recipients' fields read one (net/mail).
func ValidEmail(s string) bool {
	if s == "" || strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) ||
		strings.ContainsAny(s, "<>,;\"") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Name == "" && a.Address == s && strings.Contains(s[strings.LastIndex(s, "@")+1:], ".")
}

// ValidURL reports a website the form may save: one with http or https
// and a host, or one without a scheme that OpenURL reads as https.
func ValidURL(s string) bool {
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	}
	return OpenURL(s) != ""
}

// SplitName splits a one-line name into given and family names: the last
// word is the family name ("Petr Svoboda" → "Petr", "Svoboda"); one word
// is a given name.
func SplitName(name string) (given, family string) {
	words := strings.Fields(Clean(name, api.MaxContactNameBytes))
	switch len(words) {
	case 0:
		return "", ""
	case 1:
		return words[0], ""
	}
	return strings.Join(words[:len(words)-1], " "), words[len(words)-1]
}
