// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contacts

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
)

// en is the translator of the tests: the msgid itself.
type en struct{}

func (en) T(msgid string) string { return msgid }
func (en) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (en) C(_, msgid string) string { return msgid }

var tr = en{}

func sampleAccounts() []api.Account {
	return []api.Account{
		{ID: "acc_m365", Config: api.AccountConfig{Name: "Work", Email: "jana@firma.cz", Kind: api.AccountGraph}},
		{ID: "acc_imap", Config: api.AccountConfig{Name: "Home", Email: "jana@example.org"}},
		{ID: "acc_dav", Config: api.AccountConfig{Name: "Nextcloud", Kind: api.AccountCalDAV}},
	}
}

func sampleBooks() []api.AddressBook {
	t := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	t2 := t.Add(-time.Hour)
	return []api.AddressBook{
		{ID: "b_dav", AccountID: "acc_dav", Name: "Personal", Kind: api.AddressBookKindCardDAV, Visible: true, Count: 52, LastSyncAt: &t2},
		{ID: "b_m1", AccountID: "acc_m365", Name: "Contacts", Kind: api.AddressBookKindGraph, Visible: true, Count: 318, LastSyncAt: &t},
		{ID: "b_m2", AccountID: "acc_m365", Name: "Suppliers", Kind: api.AddressBookKindGraph, Visible: true, Count: 41, ReadOnly: true},
		{ID: "b_m3", AccountID: "acc_m365", Name: "Archive 2019", Kind: api.AddressBookKindGraph, Visible: false, Count: 122},
		{ID: api.AddressBookRecent, Name: "recent", Kind: api.AddressBookKindRecent, ReadOnly: true, Visible: true, Count: 1204},
	}
}

func TestSidebarGroupsByAccountOrder(t *testing.T) {
	items := Sidebar(sampleBooks(), sampleAccounts(), tr)
	var got []string
	for _, i := range items {
		got = append(got, i.Title+"/"+i.Badge)
	}
	want := []string{"All Contacts/", "jana@firma.cz/M365", "Contacts/", "Suppliers/", "Nextcloud/CARDDAV", "Personal/",
		"On This Computer/", "Recently Used/"}
	if !slices.Equal(got, want) {
		t.Fatalf("sidebar %v", got)
	}
	if items[0].Count != 318+41+52 || items[0].Book != api.AddressBookAll {
		t.Fatalf("all %+v", items[0])
	}
	if items[1].Selectable() || items[6].Selectable() || !items[7].Selectable() || !items[7].ReadOnly || !items[3].ReadOnly || items[2].ReadOnly {
		t.Fatal("selectable or read only")
	}
	if !HasBooks(items) || DefaultSelection(items) != api.AddressBookAll {
		t.Fatal("default")
	}
	if it, ok := Find(items, "b_dav"); !ok || it.AccountID != "acc_dav" {
		t.Fatal("find")
	}
	if _, ok := Find(items, "b_m3"); ok {
		t.Fatal("a hidden book is listed")
	}
}

func TestSidebarOnlyRecent(t *testing.T) {
	books := []api.AddressBook{{ID: api.AddressBookRecent, Kind: api.AddressBookKindRecent, Visible: true, Count: 3}}
	items := Sidebar(books, nil, tr)
	if len(items) != 2 || items[1].Kind != ItemRecent || HasBooks(items) || DefaultSelection(items) != api.AddressBookRecent {
		t.Fatalf("items %+v", items)
	}
	if DefaultSelection(nil) != "" {
		t.Fatal("empty")
	}
	// A book of an account account.list no longer has stays, at the end.
	items = Sidebar(append(books, api.AddressBook{ID: "b_x", AccountID: "acc_gone", Kind: api.AddressBookKindCardDAV, Visible: true}), nil, tr)
	if items[1].Kind != ItemAccount || items[1].Badge != BadgeCardDAV || items[2].Title != "Contacts" {
		t.Fatalf("orphan %+v", items)
	}
}

func TestListSubtitlesAndStatus(t *testing.T) {
	items := Sidebar(sampleBooks(), sampleAccounts(), tr)
	if s := ListSubtitle(items[0], 412, tr); s != "All Contacts · 412" {
		t.Fatal(s)
	}
	if s := ListSubtitle(items[7], 1, tr); s != "Recently Used · 1 address" {
		t.Fatal(s)
	}
	if s := ListSubtitle(items[5], 82, tr); s != "Personal · 82 contacts" {
		t.Fatal(s)
	}
	at, ok := SyncedAt(sampleBooks())
	if !ok || at.Hour() != 9 {
		t.Fatalf("synced %v", at)
	}
	now := time.Date(2026, 10, 30, 10, 3, 0, 0, time.UTC)
	if s := StatusLine(sampleBooks(), now, tr); !strings.HasPrefix(s, "Contacts synchronized ") {
		t.Fatal(s)
	}
	if StatusLine(nil, now, tr) != "" {
		t.Fatal("status without books")
	}
}

func TestRowsSections(t *testing.T) {
	list := []api.ContactSummary{
		{ID: "p1", DisplayName: "Tomáš Adámek", Section: "A", Org: "Stavby"},
		{ID: "p2", DisplayName: "Lucie Andělová", Section: "A", Email: "lucie@example.com"},
		{ID: "p3", DisplayName: "", Section: "", Email: "x@example.com"},
		{ID: "p4", DisplayName: "\u202eEvil", Section: "č"},
	}
	rows := Rows(list, true, tr)
	var got []string
	for _, r := range rows {
		if r.IsHeading() {
			got = append(got, "["+r.Heading+"]")
		} else {
			got = append(got, r.Title+"|"+r.Detail+"|"+r.Initials)
		}
	}
	want := []string{"[A]", "Tomáš Adámek|Stavby|TA", "Lucie Andělová|lucie@example.com|LA", "[#]", "x@example.com||X", "[Č]", "Evil||E"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows %q", got)
	}
	if n := len(Rows(list, false, tr)); n != 4 {
		t.Fatalf("flat %d", n)
	}
}

func card() api.ContactCard {
	return api.ContactCard{
		ID: "p_1", BookID: "b_m1", Revision: "r1",
		Name:        api.ContactName{Given: "Jana", Family: "Nováková"},
		DisplayName: "Jana Nováková", Org: "Stavby Novák s.r.o.", Title: "Vedoucí nákupu",
		Emails: []api.ContactEmail{{Label: "work", Address: "jana.novakova@example.cz"}, {Label: "home", Address: "jana@example.org"}},
		Phones: []api.ContactPhone{{Label: "mobile", Number: "+420 601 234 567"}, {Label: "Kancelář", Number: "1"}},
		Addresses: []api.ContactAddress{{Label: "work", Street: "Dlouhá 12", Postcode: "110 00", City: "Praha 1"},
			{Street: "  "}},
		URLs:     []api.ContactURL{{URL: "stavbynovak.example.cz"}, {Label: "work", URL: "javascript:alert(1)"}},
		Birthday: &api.ContactBirthday{Month: 3, Day: 14},
		Note:     "Volat po 10. hodině.\r\n\r\n\r\n\r\nDruhý řádek",
	}
}

func TestDetail(t *testing.T) {
	d := DetailOf(card(), calendar.EnglishStrftime, tr)
	if d.Title != "Jana Nováková" || d.Subtitle != "Vedoucí nákupu · Stavby Novák s.r.o." || d.Initials != "JN" || d.ReadOnly {
		t.Fatalf("heading %+v", d)
	}
	var got []string
	for _, r := range d.Rows {
		got = append(got, r.Label+"="+r.Value+"="+r.URL)
	}
	want := []string{
		"work email=jana.novakova@example.cz=", "home email=jana@example.org=",
		"mobile=+420 601 234 567=", "Kancelář=1=",
		"work=Dlouhá 12, 110 00 Praha 1=",
		"birthday=14 March=",
		"website=stavbynovak.example.cz=https://stavbynovak.example.cz", "work website=javascript:alert(1)=",
		"note=Volat po 10. hodině.\n\n\nDruhý řádek=",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("rows\n%q\n%q", got, want)
	}
	a := d.Actions
	if !a.WriteEmail || len(a.Emails) != 2 || a.ShowMail != "from:jana.novakova@example.cz" || !a.Edit || !a.Delete {
		t.Fatalf("actions %+v", a)
	}
	c := card()
	c.Emails, c.ReadOnly = c.Emails[:1], true
	d = DetailOf(c, calendar.EnglishStrftime, tr)
	if d.Actions.Emails != nil || d.Actions.Edit || d.Actions.Delete || !d.ReadOnly {
		t.Fatalf("read only %+v", d.Actions)
	}
	empty := DetailOf(api.ContactCard{}, calendar.EnglishStrftime, tr)
	if empty.Title != "No Name" || empty.Initials != "" || empty.Actions.WriteEmail || empty.Actions.ShowMail != "" {
		t.Fatalf("empty %+v", empty)
	}
}

func TestCopyText(t *testing.T) {
	c := card()
	c.Note = ""
	got := CopyText(c, calendar.EnglishStrftime, tr)
	if !strings.HasPrefix(got, "Jana Nováková\nVedoucí nákupu · Stavby Novák s.r.o.\nwork email: jana.novakova@example.cz\n") ||
		strings.Contains(got, "BEGIN:VCARD") {
		t.Fatalf("copy %q", got)
	}
}

func TestBirthday(t *testing.T) {
	f := calendar.EnglishStrftime
	if s := BirthdayText(api.ContactBirthday{Year: 1980, Month: 3, Day: 14}, f, tr); s != "14 March 1980" {
		t.Fatal(s)
	}
	if s := BirthdayText(api.ContactBirthday{Month: 2, Day: 29}, f, tr); s != "29 February" {
		t.Fatal(s)
	}
	for _, b := range []api.ContactBirthday{{Year: 2023, Month: 2, Day: 29}, {Month: 13, Day: 1}, {Month: 4, Day: 31}, {Month: 1}, {Year: -1, Month: 1, Day: 1}} {
		if ValidBirthday(b) || BirthdayText(b, f, tr) != "" {
			t.Errorf("valid %+v", b)
		}
	}
}

func TestShowMailQuery(t *testing.T) {
	for in, want := range map[string]string{
		"jana@example.org":      "from:jana@example.org",
		`"a b"@example.org`:     `from:"a b@example.org"`,
		"„x“@e.cz":              "from:x@e.cz",
		"a\nb@e.cz":             `from:"a b@e.cz"`,
		"  ":                    "",
		"\"\"":                  "",
		"jana@example.org\x00x": `from:"jana@example.org x"`,
	} {
		if got := ShowMailQuery(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestOpenURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.org/a":          "https://example.org/a",
		"http://example.org":             "http://example.org",
		"example.org/x":                  "https://example.org/x",
		"javascript:alert(1)":            "",
		"file:///etc/passwd":             "",
		"https://user:pw@example.org":    "",
		"ftp://example.org":              "",
		"localhost":                      "",
		"/relative":                      "",
		"exa mple.org":                   "",
		"https://exa\u202emple.org/path": "https://example.org/path",
	} {
		if got := OpenURL(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestLabels(t *testing.T) {
	if LabelText(FieldEmail, "work", tr) != "work" || LabelText(FieldEmail, "", tr) != "No Label" ||
		LabelText(FieldPhone, "fax", tr) != "fax" || LabelText(FieldURL, "home", tr) != "home" ||
		LabelText(FieldPhone, "x-\u202ecustom", tr) != "x-custom" || LabelText(FieldAddress, "other", tr) != "other" {
		t.Fatal("labels")
	}
	if DetailLabel(FieldEmail, "", tr) != "email" || DetailLabel(FieldPhone, "", tr) != "phone" ||
		DetailLabel(FieldAddress, "", tr) != "address" || DetailLabel(FieldURL, "other", tr) != "website" ||
		DetailLabel(FieldURL, "work", tr) != "work website" || DetailLabel(FieldEmail, "other", tr) != "other email" {
		t.Fatal("detail labels")
	}
	if got := LabelChoices(FieldPhone, "custom"); len(got) != 9 || got[7] != "" || got[8] != "custom" {
		t.Fatalf("choices %v", got)
	}
	if got := LabelChoices(FieldEmail, "work"); len(got) != 4 {
		t.Fatalf("choices %v", got)
	}
}

func TestAddressLineAndClean(t *testing.T) {
	a := api.ContactAddress{Street: "Dlouhá 12\nvchod B", City: "Praha", Postcode: "110 00", Region: " ", Country: "Česko\u200b"}
	if s := AddressLine(a); s != "Dlouhá 12, vchod B, 110 00 Praha, Česko" {
		t.Fatal(s)
	}
	if s := Clean("a\t\u2066b\u2069  c\x07", 0); s != "a b c" {
		t.Fatalf("%q", s)
	}
	if s := Clean("ěěě", 5); s != "ěě" {
		t.Fatalf("%q", s)
	}
	if s := CleanBlock("a\r\nb\r\rc", 0); s != "a\nb\n\nc" {
		t.Fatalf("%q", s)
	}
}

func TestInitials(t *testing.T) {
	for _, c := range [][3]string{{"Jana Nováková", "", "JN"}, {"eva", "", "E"}, {"", "x@y.cz", "X"}, {"", "", ""},
		{"(Ing.) Petr  van Dyk", "", "ID"}, {"Řehoř Šťastný", "", "ŘŠ"}} {
		if got := Initials(c[0], c[1]); got != c[2] {
			t.Errorf("%q → %q, want %q", c[0], got, c[2])
		}
	}
}

func TestAccountLabels(t *testing.T) {
	accs := sampleAccounts()
	if s := AccountLabel(&accs[0], tr); s != "Microsoft 365 (jana@firma.cz)" {
		t.Fatal(s)
	}
	if s := AccountLabel(&accs[2], tr); s != "Nextcloud" {
		t.Fatal(s)
	}
	if AccountLabel(nil, tr) != "" {
		t.Fatal("nil")
	}
	if s := SourceLine("Contacts", AccountLabel(&accs[0], tr), tr); s != "Address book Contacts · Microsoft 365 (jana@firma.cz)" {
		t.Fatal(s)
	}
	if SuggestionTooltip("", "", tr) != "Address book" || SuggestionTooltip("Kontakty", "", tr) != "Kontakty" ||
		SuggestionTooltip("Kontakty", "Microsoft 365", tr) != "Kontakty · Microsoft 365" || SuggestionTooltip("", "X", tr) != "X" {
		t.Fatal("tooltip")
	}
	if Badge("eway") != BadgeEway || Badge("weird") != "" || AccountKindBadge(accs[1]) != "" {
		t.Fatal("badges")
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []api.ErrorCode{api.CodeContactNotFound, api.CodeAddressBookNotFound, api.CodeAddressBookReadOnly,
		api.CodeContactConflict, api.CodeContactsConsentRequired, api.CodeOffline, api.CodeInvalidArgument} {
		if s, ok := ErrorText(c, "", tr); !ok || s == "" {
			t.Errorf("code %d", c)
		}
	}
	if s, _ := ErrorText(api.CodeContactsConsentRequired, "petr@firma2.cz", tr); s != "Microsoft needs your permission to access the contacts of petr@firma2.cz." {
		t.Fatal(s)
	}
	if _, ok := ErrorText(api.CodeNetworkError, "", tr); ok {
		t.Fatal("network error has its own sentence")
	}
	q := DeleteQuestion("Jana Nováková", "Microsoft 365 (jana@firma.cz)", tr)
	if q.Heading != "Delete contact Jana Nováková?" || !q.Destructive || !strings.Contains(q.Body, "Microsoft 365 (jana@firma.cz)") {
		t.Fatalf("%+v", q)
	}
}

func TestFold(t *testing.T) {
	for _, c := range []struct {
		w    float64
		want Fold
	}{{900, Fold{}}, {719, Fold{SidebarCollapsed: true}}, {519, Fold{true, true}}, {720, Fold{}}} {
		if got := FoldFor(c.w, MacBreakpoints); got != c.want {
			t.Errorf("%v → %+v", c.w, got)
		}
	}
	if !FoldFor(800, GTKBreakpoints).SidebarCollapsed {
		t.Fatal("gtk")
	}
}
