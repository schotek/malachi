// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contacts

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// msgKey is a msgid of the template with its context and plural.
type msgKey struct{ ctx, msgid, plural string }

// recorder is a translator that notes every msgid asked for.
type recorder map[msgKey]bool

func (r recorder) T(msgid string) string { r[msgKey{msgid: msgid}] = true; return msgid }

func (r recorder) N(msgid, plural string, n int) string {
	r[msgKey{msgid: msgid, plural: plural}] = true
	if n == 1 {
		return msgid
	}
	return plural
}

func (r recorder) C(ctx, msgid string) string { r[msgKey{ctx: ctx, msgid: msgid}] = true; return msgid }

// exercise calls every function of the package that translates, down
// every branch that picks a msgid.
func exercise(tr Translator) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	WindowTitle(tr)
	AllContacts(tr)
	RecentlyUsed(tr)
	OnThisComputer(tr)
	SearchContacts(tr)
	NewContact(tr)
	ContactCount(2, tr)
	AddressCount(2, tr)
	SyncedLine(now.Add(-time.Hour), now, tr)
	WriteEmail(tr)
	ShowMail(tr)
	Copy(tr)
	Edit(tr)
	Done(tr)
	Cancel(tr)
	DeleteContact(tr)
	ReadOnly(tr)
	ShowContact(tr)
	AddTitle(tr)
	ContactAdded(tr)
	Add(tr)
	MoreDetails(tr)
	SuggestionTooltip("", "", tr)
	SuggestionTooltip("a", "b", tr)
	DeleteQuestion("a", "b", tr)
	ConflictQuestion(tr)
	DiscardQuestion(tr)
	Copied(tr)
	Allow(tr)
	ConsentOnline(tr)
	for e := EmptyNone; e <= EmptySelection; e++ {
		EmptyText(e, tr)
	}
	OpenPreferences(tr)
	for _, c := range []api.ErrorCode{api.CodeContactNotFound, api.CodeAddressBookNotFound, api.CodeAddressBookReadOnly,
		api.CodeContactConflict, api.CodeContactsConsentRequired, api.CodeOffline, api.CodeInvalidArgument} {
		ErrorText(c, "", tr)
		ErrorText(c, "x", tr)
	}
	SyncContacts(tr)
	CardDAVNote(tr)
	SuggestionsGroup(tr)
	for _, s := range Scopes {
		ScopeText(s, tr)
	}
	NoContactAccounts(tr)
	AddressBooksHeading(tr)
	CalendarsHeading(tr)
	WizardTitle(tr)
	for _, k := range []FieldKind{FieldEmail, FieldPhone, FieldAddress, FieldURL} {
		for _, l := range LabelChoices(k, "custom") {
			LabelText(k, l, tr)
			DetailLabel(k, l, tr)
		}
		AddRowText(k, tr)
	}
	RemoveRowText(tr)
	AddBirthday(tr)
	for k := KeyGiven; k < keyCount; k++ {
		Placeholder(k, tr)
	}
	for p := PartStreet; p <= PartCountry; p++ {
		AddressPlaceholder(p, tr)
	}
	BookLabel(tr)
	NameLabel(tr)
	EmailLabel(tr)
	d := card()
	d.Birthday = &api.ContactBirthday{Year: 1980, Month: 3, Day: 14}
	d.Nickname = "J"
	DetailOf(d, calendar.EnglishStrftime, tr)
	DetailOf(card(), calendar.EnglishStrftime, tr)
	DetailOf(api.ContactCard{}, calendar.EnglishStrftime, tr)
	f := NewForm("b")
	f.Emails = []LabelledValue{{Value: "bad"}, {Value: strings.Repeat("a", 2000) + "@x.cz"}}
	f.URLs = []LabelledValue{{Value: "javascript:x"}}
	f.Birthday = &api.ContactBirthday{}
	f.Validate(tr)
	NewForm("b").Validate(tr)
	g := NewForm("b")
	g.Kind = api.AddressBookKindGraph
	g.Emails = []LabelledValue{{Value: "a@b.cz"}, {Value: "c@b.cz"}, {Value: "d@b.cz"}, {Value: "e@b.cz"}}
	g.Addresses = []api.ContactAddress{{City: "A"}, {City: "B"}}
	g.URLs = []LabelledValue{{Value: "a.cz"}, {Value: "b.cz"}}
	g.Validate(tr)
	ProblemOf(&api.Error{Code: api.CodeInvalidArgument, Message: "card.org: over"}, tr)
	NewAddFromChip(api.Address{Name: "A B", Address: "a@b.cz"}, "", nil, nil, tr)
	accs := []api.Account{{ID: "a", Config: api.AccountConfig{Email: "x@y.cz", Kind: api.AccountGraph}}}
	AccountLabel(&accs[0], tr)
	ChipItemText(ChipShow, tr)
	ChipItemText(ChipAdd, tr)
	ListSubtitle(Item{Kind: ItemAll}, 1, tr)
	ListSubtitle(Item{Kind: ItemRecent}, 1, tr)
	ListSubtitle(Item{Kind: ItemBook}, 1, tr)
	Sidebar([]api.AddressBook{{ID: "b", Kind: api.AddressBookKindGraph, Visible: true},
		{ID: api.AddressBookRecent, Kind: api.AddressBookKindRecent}}, nil, tr)
	BookName(api.AddressBook{ID: "b"}, tr)
	SourceLine("a", "b", tr)
}

// template reads po/malachi.pot: every entry, and whether it names a file
// of this package among its references.
func template(t *testing.T) map[msgKey]bool {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "po", "malachi.pot"))
	if err != nil {
		t.Fatalf("open the template: %v", err)
	}
	defer f.Close()
	field := regexp.MustCompile(`^(msgctxt|msgid_plural|msgid|msgstr(?:\[\d\])?) (".*")$`)
	out := map[msgKey]bool{}
	var cur msgKey
	var ours bool
	var last *string
	flush := func() {
		if cur.msgid != "" {
			out[cur] = ours
		}
		cur, ours, last = msgKey{}, false, nil
	}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case strings.HasPrefix(line, "#:"):
			ours = ours || strings.Contains(line, "ui/internal/contacts/")
		case strings.HasPrefix(line, "\""):
			if last != nil {
				u, err := strconv.Unquote(line)
				if err != nil {
					t.Fatalf("template string %s: %v", line, err)
				}
				*last += u
			}
		default:
			m := field.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			last = nil
			switch m[1] {
			case "msgctxt":
				last = &cur.ctx
			case "msgid":
				last = &cur.msgid
			case "msgid_plural":
				last = &cur.plural
			}
			if last != nil {
				u, err := strconv.Unquote(m[2])
				if err != nil {
					t.Fatalf("template string %s: %v", m[2], err)
				}
				*last = u
			}
		}
	}
	flush()
	if err := s.Err(); err != nil {
		t.Fatalf("read the template: %v", err)
	}
	return out
}

// TestMsgidsInTemplate keeps po/malachi.pot in step with the package on a
// machine without make lint, like calendar's. It is skipped until make po
// has run once with the package in po/POTFILES (the template then names
// it).
func TestMsgidsInTemplate(t *testing.T) {
	used := recorder{}
	exercise(used)
	pot := template(t)
	own := 0
	for _, ours := range pot {
		if ours {
			own++
		}
	}
	if own == 0 {
		t.Skip("po/malachi.pot names no file of ui/internal/contacts yet (make po)")
	}
	for k := range used {
		if _, ok := pot[k]; !ok {
			t.Errorf("po/malachi.pot lacks msgid %q (context %q, plural %q)", k.msgid, k.ctx, k.plural)
		}
	}
	for k, ours := range pot {
		if ours && !used[k] {
			t.Errorf("po/malachi.pot names ui/internal/contacts for msgid %q (context %q), which the package does not translate", k.msgid, k.ctx)
		}
	}
}
