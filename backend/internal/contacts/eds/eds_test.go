// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eds

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/schotek/malachi/backend/internal/contacts"
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeBus answers method calls from a handler table keyed by "path method".
type fakeBus struct {
	mu       sync.Mutex
	handlers map[string]func(args []any) ([]any, error)
	calls    []string
	closed   bool
}

func newFakeBus() *fakeBus {
	return &fakeBus{handlers: map[string]func([]any) ([]any, error){}}
}

func (b *fakeBus) on(path dbus.ObjectPath, method string, fn func(args []any) ([]any, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[string(path)+" "+method] = fn
}

func (b *fakeBus) reply(path dbus.ObjectPath, method string, out ...any) {
	b.on(path, method, func([]any) ([]any, error) { return out, nil })
}

func (b *fakeBus) count(path dbus.ObjectPath, method string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, c := range b.calls {
		if c == string(path)+" "+method {
			n++
		}
	}
	return n
}

func (b *fakeBus) Object(_ string, path dbus.ObjectPath) dbus.BusObject {
	return &fakeObject{b: b, path: path}
}
func (b *fakeBus) AddMatchSignalContext(context.Context, ...dbus.MatchOption) error    { return nil }
func (b *fakeBus) RemoveMatchSignalContext(context.Context, ...dbus.MatchOption) error { return nil }
func (b *fakeBus) Signal(chan<- *dbus.Signal)                                          {}
func (b *fakeBus) RemoveSignal(chan<- *dbus.Signal)                                    {}
func (b *fakeBus) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return nil
}

type fakeObject struct {
	b    *fakeBus
	path dbus.ObjectPath
}

func (o *fakeObject) CallWithContext(_ context.Context, method string, _ dbus.Flags, args ...any) *dbus.Call {
	o.b.mu.Lock()
	o.b.calls = append(o.b.calls, string(o.path)+" "+method)
	fn := o.b.handlers[string(o.path)+" "+method]
	o.b.mu.Unlock()
	call := &dbus.Call{Destination: booksService, Path: o.path, Method: method, Args: args}
	if fn == nil {
		call.Err = dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownObject", Body: []any{"no handler for " + method}}
		return call
	}
	call.Body, call.Err = fn(args)
	return call
}
func (o *fakeObject) Call(method string, flags dbus.Flags, args ...any) *dbus.Call {
	return o.CallWithContext(context.Background(), method, flags, args...)
}
func (o *fakeObject) GetProperty(string) (dbus.Variant, error) { panic("unused") }
func (o *fakeObject) Go(string, dbus.Flags, chan *dbus.Call, ...any) *dbus.Call {
	panic("unused")
}
func (o *fakeObject) GoWithContext(context.Context, string, dbus.Flags, chan *dbus.Call, ...any) *dbus.Call {
	panic("unused")
}
func (o *fakeObject) AddMatchSignal(string, string, ...dbus.MatchOption) *dbus.Call { panic("unused") }
func (o *fakeObject) RemoveMatchSignal(string, string, ...dbus.MatchOption) *dbus.Call {
	panic("unused")
}
func (o *fakeObject) StoreProperty(string, any) error { panic("unused") }
func (o *fakeObject) SetProperty(string, any) error   { panic("unused") }
func (o *fakeObject) Destination() string             { return booksService }
func (o *fakeObject) Path() dbus.ObjectPath           { return o.path }

// --- fixtures ----------------------------------------------------------------

const (
	m365Collection  = "4816070173bc9cf1aaf1885eb4ef5746bb9f1d85"
	cloudCollection = "954bb117a5cd071296cbe383374b412ba1c9a0c5"
	offCollection   = "0000000000000000000000000000000000000000"
	contactsBook    = "948c8b07b12c6f089d05f5124289b8d4945b019a"
	peopleBook      = "e8037464b820458124eb52b5996006ee5e9f00a5"
	galBook         = "8ae380745b9827bd306e5c6681af621f0baa0c3a"
	cloudBook       = "036076da4446c972e341461c262fd30d4fa03a0b"
	localBook       = "system-address-book"
	disabledBook    = "1111111111111111111111111111111111111111"
	hiddenBook      = "2222222222222222222222222222222222222222"
)

func sourceObject(uid, data string) map[string]map[string]dbus.Variant {
	return map[string]map[string]dbus.Variant{
		ifaceSource: {"UID": dbus.MakeVariant(uid), "Data": dbus.MakeVariant(data)},
	}
}

func bookData(name, parent, backend string, extra ...string) string {
	return "[Data Source]\nDisplayName=" + name + "\nEnabled=true\nParent=" + parent + "\n\n" +
		"[Address Book]\nBackendName=" + backend + "\n" + strings.Join(extra, "\n") + "\n"
}

// managedObjects mirrors a registry with two Online Accounts collections,
// their books, a local book and the sources that must not count.
func managedObjects() map[dbus.ObjectPath]map[string]map[string]dbus.Variant {
	const p = "/org/gnome/evolution/dataserver/SourceManager/Source_"
	return map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		sourcesPath: {},
		p + "1": sourceObject(m365Collection, "[Data Source]\nDisplayName=me@contoso.com\nEnabled=true\nParent=\n\n"+
			"[Collection]\nBackendName=microsoft365\nIdentity=me@contoso.com\n\n"+
			"[GNOME Online Accounts]\nAccountId=account_1788593046_1\nAddress=Me@Contoso.com\n"),
		p + "2": sourceObject(contactsBook, bookData("Contacts", m365Collection, "microsoft365")),
		p + "3": sourceObject(peopleBook, bookData("People", m365Collection, "microsoft365", "[Autocomplete]", "IncludeMe=false")),
		p + "4": sourceObject(galBook, bookData("Organisation users", m365Collection, "microsoft365")),
		p + "5": sourceObject(cloudCollection, "[Data Source]\nDisplayName=vlada@example.org\nEnabled=true\nParent=\n\n"+
			"[Collection]\nBackendName=webdav\nIdentity=vlada@example.org\n\n"+
			"[GNOME Online Accounts]\nAccountId=account_1788289873_0\nAddress=\n"),
		p + "6":  sourceObject(cloudBook, bookData("Kontakty", cloudCollection, "carddav")),
		p + "7":  sourceObject(localBook, "[Data Source]\nDisplayName=Personal\nEnabled=true\nParent=local-stub\n\n[Address Book]\nBackendName=local\n"),
		p + "8":  sourceObject(disabledBook, "[Data Source]\nDisplayName=Old\nEnabled=false\nParent=\n\n[Address Book]\nBackendName=local\n"),
		p + "9":  sourceObject(offCollection, "[Data Source]\nDisplayName=off\nEnabled=false\nParent=\n\n[Collection]\nBackendName=webdav\nIdentity=off@example.org\n"),
		p + "10": sourceObject(hiddenBook, bookData("Hidden by parent", offCollection, "carddav")),
		p + "11": sourceObject("system-calendar", "[Data Source]\nDisplayName=Personal\nEnabled=true\n\n[Calendar]\nBackendName=local\n"),
		p + "12": sourceObject("../etc", bookData("Traversal", "", "local")),
		p + "13": {"org.gnome.evolution.dataserver.Source.Writable": {}},
	}
}

func newClient(t *testing.T) (*Client, *fakeBus) {
	t.Helper()
	b := newFakeBus()
	c := New(nil)
	c.dial = func() (bus, error) { return b, nil }
	return c, b
}

func card(fn, email string) string {
	return "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:" + fn + "\r\nEMAIL:" + email + "\r\nEND:VCARD\r\n"
}

// wireBooks makes the factory open any known book at a path derived from
// its uid, with a working Open.
func wireBooks(b *fakeBus, uids ...string) map[string]dbus.ObjectPath {
	paths := map[string]dbus.ObjectPath{}
	for i, uid := range uids {
		paths[uid] = dbus.ObjectPath("/org/gnome/evolution/dataserver/Subprocess/1/" + string(rune('1'+i)))
		b.reply(paths[uid], ifaceBook+".Open", []string{})
	}
	b.on(factoryPath, ifaceFactory+".OpenAddressBook", func(args []any) ([]any, error) {
		uid, _ := args[0].(string)
		p, ok := paths[uid]
		if !ok {
			return nil, dbus.Error{Name: "org.gnome.evolution.dataserver.AddressBook.Error.NoSuchBook"}
		}
		return []any{string(p), booksService}, nil
	})
	return paths
}

// --- tests -------------------------------------------------------------------

func TestBooks(t *testing.T) {
	c, b := newClient(t)
	b.reply(sourcesPath, ifaceObjectManager+".GetManagedObjects", managedObjects())
	ctx := context.Background()

	books, err := c.Books(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, bk := range books {
		got = append(got, bk.UID+"|"+bk.Name+"|"+bk.Backend+"|"+bk.Collection+"|"+bk.GOAAccountID+"|"+bk.Email)
	}
	want := []string{
		contactsBook + "|Contacts|microsoft365|" + m365Collection + "|account_1788593046_1|me@contoso.com",
		cloudBook + "|Kontakty|carddav|" + cloudCollection + "|account_1788289873_0|vlada@example.org",
		galBook + "|Organisation users|microsoft365|" + m365Collection + "|account_1788593046_1|me@contoso.com",
		localBook + "|Personal|local|local-stub||",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("books:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The list is cached for a minute, then fetched again.
	if _, err := c.Books(ctx); err != nil || b.count(sourcesPath, ifaceObjectManager+".GetManagedObjects") != 1 {
		t.Errorf("second call hit the bus (%d), err %v", b.count(sourcesPath, ifaceObjectManager+".GetManagedObjects"), err)
	}
	c.now = func() time.Time { return time.Now().Add(2 * booksTTL) }
	if _, err := c.Books(ctx); err != nil || b.count(sourcesPath, ifaceObjectManager+".GetManagedObjects") != 2 {
		t.Errorf("stale cache not refreshed, err %v", err)
	}
}

func TestSearch(t *testing.T) {
	c, b := newClient(t)
	paths := wireBooks(b, contactsBook, galBook)
	var gotQuery string
	b.on(paths[contactsBook], ifaceBook+".GetContactList", func(args []any) ([]any, error) {
		gotQuery, _ = args[0].(string)
		return []any{[]string{
			card("Alice Example", "alice@example.org"),
			"BEGIN:VCARD\r\nFN:Two Mails\r\nEMAIL:a@example.org\r\nEMAIL;TYPE=PREF:b@example.org\r\nEND:VCARD\r\n",
			"not a card at all",
		}}, nil
	})
	// The directory book answers with a stale handle first: the client
	// must reopen it once and go on.
	galCalls := 0
	b.on(paths[galBook], ifaceBook+".GetContactList", func([]any) ([]any, error) {
		galCalls++
		if galCalls == 1 {
			return nil, dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownObject"}
		}
		return []any{[]string{card("Colleague", "colleague@contoso.com")}}, nil
	})
	books := []contacts.Book{
		{UID: contactsBook, Name: "Contacts"},
		{UID: galBook, Name: "GAL"},
		{UID: "../../etc/passwd", Name: "Traversal"},
	}

	found, err := c.Search(context.Background(), books, `al"i\ce`, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range found {
		got = append(got, f.Name+"|"+f.Address+"|"+f.Book)
	}
	want := []string{
		"Alice Example|alice@example.org|Contacts",
		"Two Mails|b@example.org|Contacts",
		"Two Mails|a@example.org|Contacts",
		"Colleague|colleague@contoso.com|GAL",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("found:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if gotQuery != `(contains "x-evolution-any-field" "al\"i\\ce")` {
		t.Errorf("query = %s", gotQuery)
	}
	if n := b.count(factoryPath, ifaceFactory+".OpenAddressBook"); n != 3 {
		t.Errorf("OpenAddressBook called %d times, want 3 (two books, one reopened)", n)
	}

	// Handles are kept: another search opens nothing new; a limit cuts.
	found, err = c.Search(context.Background(), books[:2], "a", 1)
	if err != nil || len(found) != 1 || b.count(factoryPath, ifaceFactory+".OpenAddressBook") != 3 {
		t.Errorf("second search: %+v, %v, opens %d", found, err, b.count(factoryPath, ifaceFactory+".OpenAddressBook"))
	}
	if found, err := c.Search(context.Background(), books, "   ", 5); err != nil || found != nil {
		t.Errorf("blank query: %+v, %v", found, err)
	}
}

func TestSearchFailingBooks(t *testing.T) {
	c, b := newClient(t)
	paths := wireBooks(b, contactsBook, galBook)
	b.reply(paths[contactsBook], ifaceBook+".GetContactList", []string{card("Alice", "alice@example.org")})
	b.on(paths[galBook], ifaceBook+".GetContactList", func([]any) ([]any, error) {
		return nil, dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{"backend broke"}}
	})
	books := []contacts.Book{{UID: contactsBook, Name: "Contacts"}, {UID: galBook, Name: "GAL"}}

	// One broken book costs its own results only.
	found, err := c.Search(context.Background(), books, "al", 10)
	if err != nil || len(found) != 1 || found[0].Address != "alice@example.org" {
		t.Fatalf("with a failing book: %+v, %v", found, err)
	}

	// No book answered and the service is gone: unavailable, for the core
	// to swallow.
	b.on(factoryPath, ifaceFactory+".OpenAddressBook", func([]any) ([]any, error) {
		return nil, dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"}
	})
	c.Close()
	_, err = c.Search(context.Background(), books, "al", 10)
	if code(t, err) != api.CodeUnavailable {
		t.Errorf("service gone: %v", err)
	}

	// A factory that names a bogus owner or path is refused, not trusted.
	b.reply(factoryPath, ifaceFactory+".OpenAddressBook", "/org/gnome/evolution/dataserver/Subprocess/1/1", "org.freedesktop.DBus")
	c.Close()
	if found, err := c.Search(context.Background(), books[:1], "al", 10); err != nil || len(found) != 0 {
		t.Errorf("bogus owner: %+v, %v", found, err)
	}
}

func TestUnavailable(t *testing.T) {
	c, _ := newClient(t)
	c.dial = func() (bus, error) { return nil, errors.New("no session bus") }
	if _, err := c.Books(context.Background()); code(t, err) != api.CodeUnavailable {
		t.Errorf("no bus: %v", err)
	}

	c, b := newClient(t)
	b.on(sourcesPath, ifaceObjectManager+".GetManagedObjects", func([]any) ([]any, error) {
		return nil, dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"}
	})
	if _, err := c.Books(context.Background()); code(t, err) != api.CodeUnavailable {
		t.Errorf("no service: %v", err)
	}
}

func TestReconnectsOnce(t *testing.T) {
	c, b := newClient(t)
	dials := 0
	c.dial = func() (bus, error) { dials++; return b, nil }
	failed := false
	b.on(sourcesPath, ifaceObjectManager+".GetManagedObjects", func([]any) ([]any, error) {
		if !failed {
			failed = true
			return nil, dbus.ErrClosed
		}
		return []any{managedObjects()}, nil
	})
	books, err := c.Books(context.Background())
	if err != nil || len(books) != 4 || dials != 2 || !b.closed {
		t.Fatalf("reconnect: %d books, err %v, dials %d, closed %v", len(books), err, dials, b.closed)
	}
}

func TestContainsQuery(t *testing.T) {
	cases := map[string]string{
		"jan":   `(contains "x-evolution-any-field" "jan")`,
		`a"b`:   `(contains "x-evolution-any-field" "a\"b")`,
		`a\b`:   `(contains "x-evolution-any-field" "a\\b")`,
		"Novák": `(contains "x-evolution-any-field" "Novák")`,
		"(or)":  `(contains "x-evolution-any-field" "(or)")`,
	}
	for in, want := range cases {
		if got := containsQuery(in); got != want {
			t.Errorf("containsQuery(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestValidUID(t *testing.T) {
	for _, ok := range []string{"system-address-book", "948c8b07b12c6f089d05f5124289b8d4945b019a", "a.b_c@d"} {
		if !ValidUID(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "../etc", "a b", "a/b", strings.Repeat("x", 129), "ünïcode"} {
		if ValidUID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func code(t *testing.T, err error) api.ErrorCode {
	t.Helper()
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("not an api error: %v", err)
	}
	return apiErr.Code
}
