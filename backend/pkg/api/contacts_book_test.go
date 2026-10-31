// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// contactsBookMethods end AllMethods, after the assistant profile's;
// notify.contactsChanged ends AllNotifications.
var contactsBookMethods = []string{
	"addressBook.list", "addressBook.set",
	"contact.list", "contact.get", "contact.lookup",
	"contact.create", "contact.update", "contact.delete",
	"contact.photo",
}

func TestContactsBookMethodsOrder(t *testing.T) {
	n := len(AllMethods)
	if !reflect.DeepEqual(AllMethods[n-len(contactsBookMethods):], contactsBookMethods) {
		t.Fatalf("AllMethods does not end with the address-book methods: %v", AllMethods[n-len(contactsBookMethods):])
	}
	if AllMethods[n-len(contactsBookMethods)-1] != MethodAssistantProfileResolve {
		t.Fatal("the address-book methods do not follow the assistant profile's")
	}
	if AllNotifications[len(AllNotifications)-1] != NotifyContactsChanged {
		t.Fatalf("AllNotifications: %v", AllNotifications)
	}
}

// The codes are the free ones of their groups (docs/plans/address-book.md §4.3).
func TestContactsBookErrorCodes(t *testing.T) {
	for code, name := range map[ErrorCode]string{
		1110: "contactNotFound", 1111: "addressBookNotFound", 1206: "contactsConsentRequired",
		1308: "contactConflict", 1509: "addressBookReadOnly",
	} {
		if code.String() != name {
			t.Errorf("%d = %s, want %s", code, code.String(), name)
		}
	}
	raw, _ := json.Marshal(ContactsConsentRequiredData{AccountID: "acc_1", SignIn: true})
	if string(raw) != `{"accountId":"acc_1","signIn":true}` {
		t.Errorf("consent data %s", raw)
	}
}

func TestContactsBookLimits(t *testing.T) {
	if MaxContactListLimit != 500 || MaxVCardBytes != 1<<20 || MaxContactLookupAddresses != 200 ||
		MaxContactPhotoBytes != 64<<10 || MaxContactPhotoPixels != 256 || ContactPhotoTimeout != 15*time.Second {
		t.Fatal("limits differ from docs/api.md §4.19")
	}
}

// Minimal values send the required fields only; a full card survives a
// round trip.
func TestContactsBookJSON(t *testing.T) {
	if got, want := roundTrip(t, "book", AddressBook{ID: AddressBookRecent, Name: "Recent", Kind: AddressBookKindRecent, ReadOnly: true, Visible: true, Count: 3}),
		`{"id":"recent","name":"Recent","kind":"recent","readOnly":true,"visible":true,"count":3}`; got != want {
		t.Errorf("book %s, want %s", got, want)
	}
	if got, want := roundTrip(t, "summary", ContactSummary{ID: "p_1", BookID: "b_1", DisplayName: "Jana", SortKey: "jana", Section: "J"}),
		`{"id":"p_1","bookId":"b_1","displayName":"Jana","sortKey":"jana","section":"J"}`; got != want {
		t.Errorf("summary %s, want %s", got, want)
	}
	card := ContactCard{
		ID: "p_1", BookID: "b_1", Revision: `"e1"`,
		Name:        ContactName{Given: "Jana", Family: "Nováková"},
		DisplayName: "Jana Nováková", Org: "Acme", Title: "CTO",
		Emails:    []ContactEmail{{Label: ContactLabelWork, Address: "jana@example.org"}},
		Phones:    []ContactPhone{{Label: ContactLabelMobile, Number: "+420 600 000 000"}},
		Addresses: []ContactAddress{{Label: ContactLabelHome, City: "Praha", Country: "CZ"}},
		URLs:      []ContactURL{{URL: "https://example.org"}},
		Birthday:  &ContactBirthday{Month: 2, Day: 29},
		Note:      "plain text",
	}
	roundTrip(t, "card", card)
	roundTrip(t, "conflict", ContactConflictData{Server: card})
	if got, want := roundTrip(t, "empty card", ContactCard{}),
		`{"id":"","bookId":"","readOnly":false,"revision":"","name":{},"displayName":"","emails":null,"phones":null,"addresses":null,"urls":null}`; got != want {
		t.Errorf("empty card %s, want %s", got, want)
	}
	roundTrip(t, "lookup", ContactLookupResult{Known: map[string]ContactID{"jana@example.org": "p_1"}})
	if got, want := roundTrip(t, "lookup photos", ContactLookupResult{Known: map[string]ContactID{},
		Photos: map[string]ContactPhotoRef{"jana@example.org": {ContactID: "p_1", State: ContactPhotoRemote}}}),
		`{"known":{},"photos":{"jana@example.org":{"contactId":"p_1","state":"remote"}}}`; got != want {
		t.Errorf("lookup photos %s, want %s", got, want)
	}
	if got, want := roundTrip(t, "lookup params", ContactLookupParams{Addresses: []string{"a@b.c"}, WithPhotos: true}),
		`{"addresses":["a@b.c"],"withPhotos":true}`; got != want {
		t.Errorf("lookup params %s, want %s", got, want)
	}
	if got, want := roundTrip(t, "photo", ContactPhotoResult{State: ContactPhotoStateReady, MIME: "image/jpeg", Data: []byte{0xff, 0xd8}, Revision: `"e1"`}),
		`{"state":"ready","mime":"image/jpeg","data":"/9g=","revision":"\"e1\""}`; got != want {
		t.Errorf("photo %s, want %s", got, want)
	}
	if got, want := roundTrip(t, "no photo", ContactPhotoResult{State: ContactPhotoStateNone, Revision: "ck"}),
		`{"state":"none","revision":"ck"}`; got != want {
		t.Errorf("no photo %s, want %s", got, want)
	}
	if got, want := roundTrip(t, "summary with photo", ContactSummary{ID: "p_1", BookID: "b_1", Photo: ContactPhotoReady}),
		`{"id":"p_1","bookId":"b_1","displayName":"","sortKey":"","section":"","photo":"ready"}`; got != want {
		t.Errorf("summary with photo %s, want %s", got, want)
	}
	if got, want := roundTrip(t, "changed", ContactsChangedNotification{AccountID: "acc_1", BookIDs: []AddressBookID{"b_1"}}),
		`{"accountId":"acc_1","bookIds":["b_1"]}`; got != want {
		t.Errorf("notification %s, want %s", got, want)
	}
	if got, want := roundTrip(t, "graph", GraphConfig{Source: GraphSourceDaemon, Contacts: &ContactsConfig{Sync: true}}),
		`{"source":"daemon","contacts":{"sync":true}}`; got != want {
		t.Errorf("graph config %s, want %s", got, want)
	}
	if got, want := roundTrip(t, "search", ContactSearchParams{AccountID: "acc_1", Query: "ja", Scope: ContactScopeAll}),
		`{"accountId":"acc_1","query":"ja","scope":"all"}`; got != want {
		t.Errorf("search %s, want %s", got, want)
	}
}
