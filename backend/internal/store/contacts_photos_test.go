// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"testing"
)

// TestContactPhotos: the rows of 0024, the state the listings compute and
// the lookup of photos by address.
func TestContactPhotos(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	books, err := s.UpsertAddressBooks(ctx, "acc_1", []AddressBook{{RemoteID: "/dav/"}, {RemoteID: "graph-folder"}})
	if err != nil {
		t.Fatal(err)
	}
	dav, gr := books[0].ID, books[1].ID
	add := func(book, remote, etag, kind, key, addr string) string {
		t.Helper()
		id, err := s.UpsertContact(ctx, StoredContact{BookID: book, RemoteID: remote, ETag: etag, Raw: []byte("x"), RawKind: kind,
			DisplayName: key, SortKey: key, Emails: []ContactEmailRow{{Address: addr}}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	withPhoto := add(dav, "/1", `"1"`, ContactRawVCard, "b", "same@example.org")
	without := add(dav, "/2", `"1"`, ContactRawVCard, "a", "same@example.org")
	graphNew := add(gr, "g1", "ck1", ContactRawGraph, "c", "graph@example.org")
	graphNone := add(gr, "g2", "ck1", ContactRawGraph, "d", "none@example.org")

	jpeg := []byte("\xff\xd8\xff…")
	if err := s.SetContactPhoto(ctx, ContactPhoto{ContactID: withPhoto, Revision: `"1"`, State: PhotoOK, MIME: "image/jpeg", Data: jpeg}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContactPhoto(ctx, ContactPhoto{ContactID: without, Revision: `"1"`, State: PhotoNone, MIME: "x", Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContactPhoto(ctx, ContactPhoto{ContactID: graphNone, Revision: "ck1", State: PhotoNone}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ContactPhoto{
		{ContactID: withPhoto, State: "maybe"},
		{ContactID: withPhoto, State: PhotoOK, MIME: "image/jpeg"},
		{ContactID: withPhoto, State: PhotoOK, MIME: "image/jpeg", Data: make([]byte, MaxPhotoBytes+1)},
	} {
		if err := s.SetContactPhoto(ctx, bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if err := s.SetContactPhoto(ctx, ContactPhoto{ContactID: "p_gone", State: PhotoNone}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown contact: %v", err)
	}

	p, err := s.GetContactPhoto(ctx, withPhoto)
	if err != nil || p.State != PhotoOK || p.MIME != "image/jpeg" || !bytes.Equal(p.Data, jpeg) || p.Revision != `"1"` {
		t.Fatalf("get: %+v %v", p, err)
	}
	if p, _ := s.GetContactPhoto(ctx, without); p.MIME != "" || len(p.Data) != 0 {
		t.Errorf("none keeps %q", p.Data)
	}
	if _, err := s.GetContactPhoto(ctx, graphNew); !errors.Is(err, ErrNotFound) {
		t.Errorf("no row: %v", err)
	}

	states := func() map[string]string {
		rows, _, err := s.ListContacts(ctx, ContactQuery{BookIDs: []string{dav, gr}})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, r := range rows {
			out[r.ID] = r.PhotoState
		}
		return out
	}
	want := map[string]string{withPhoto: ContactPhotoReady, without: "", graphNew: ContactPhotoRemote, graphNone: ""}
	if got := states(); !maps.Equal(got, want) {
		t.Errorf("states %v, want %v", got, want)
	}
	if c, _ := s.GetContact(ctx, withPhoto); c.PhotoState != ContactPhotoReady {
		t.Errorf("get contact: %q", c.PhotoState)
	}

	// A new revision: the vCard's row is stale (to be read again), the
	// Graph contact's photo is remote again.
	add(dav, "/1", `"2"`, ContactRawVCard, "b", "same@example.org")
	add(gr, "g2", "ck2", ContactRawGraph, "d", "none@example.org")
	want[withPhoto], want[graphNone] = "", ContactPhotoRemote
	if got := states(); !maps.Equal(got, want) {
		t.Errorf("after new revisions %v, want %v", got, want)
	}
	stale, err := s.VCardContactsWithoutPhoto(ctx, dav, 10)
	if err != nil || len(stale) != 1 || stale[0].ID != withPhoto || string(stale[0].Raw) != "x" {
		t.Fatalf("stale vcards: %+v %v", stale, err)
	}
	if rev, ok, _ := s.ContactPhotoRevision(ctx, withPhoto); !ok || rev != `"1"` {
		t.Errorf("revision %q %v", rev, ok)
	}
	if err := s.SetContactPhoto(ctx, ContactPhoto{ContactID: withPhoto, Revision: `"2"`, State: PhotoOK, MIME: "image/png", Data: []byte("png")}); err != nil {
		t.Fatal(err)
	}
	if stale, _ := s.VCardContactsWithoutPhoto(ctx, dav, 10); len(stale) != 0 {
		t.Errorf("still stale: %d", len(stale))
	}

	// Lookup: same@ has two contacts, only the one with a photo counts
	// even though the other sorts first; none@ is remote again.
	got, err := s.LookupContactPhotos(ctx, []string{"same@example.org", "graph@example.org", "none@example.org", "x@example.org"}, []string{dav, gr})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got["same@example.org"] != (ContactPhotoMatch{withPhoto, ContactPhotoReady}) ||
		got["graph@example.org"] != (ContactPhotoMatch{graphNew, ContactPhotoRemote}) || got["none@example.org"].ContactID != graphNone {
		t.Errorf("lookup %v", got)
	}
	if got, _ := s.LookupContactPhotos(ctx, []string{"same@example.org"}, []string{gr}); len(got) != 0 {
		t.Errorf("other books: %v", got)
	}

	// The rows go with the contact and the book.
	if err := s.DeleteContact(ctx, withPhoto); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAddressBook(ctx, gr); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM ab_photos`); n != 1 {
		t.Errorf("%d photo rows left, want the one of %s", n, without)
	}
}
