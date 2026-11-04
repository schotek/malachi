// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contactsctl

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestSamplePhotos(t *testing.T) {
	s := NewSampleCaller(time.Now())
	var notified int
	s.Notify = func(string, json.RawMessage) { notified++ }
	ctx := context.Background()

	var list api.ContactListResult
	if err := s.Call(ctx, api.MethodContactList, api.ContactListParams{BookID: api.AddressBookAll}, &list); err != nil {
		t.Fatal(err)
	}
	ready, remote := 0, 0
	var marie api.ContactID
	for _, c := range list.Contacts {
		switch c.Photo {
		case api.ContactPhotoReady:
			ready++
		case api.ContactPhotoRemote:
			remote++
			if c.DisplayName == "Marie Svobodová" {
				marie = c.ID
			}
		}
	}
	if ready != 5 || remote != 2 || marie == "" {
		t.Fatalf("ready %d remote %d", ready, remote)
	}

	var lk api.ContactLookupResult
	if err := s.Call(ctx, api.MethodContactLookup, api.ContactLookupParams{
		Addresses: []string{"Jana.Novakova@example.cz", "pavel.novak@example.cz"}, WithPhotos: true}, &lk); err != nil {
		t.Fatal(err)
	}
	ref, ok := lk.Photos["jana.novakova@example.cz"]
	if !ok || ref.State != api.ContactPhotoReady || len(lk.Photos) != 1 || len(lk.Known) != 2 {
		t.Fatalf("lookup %+v", lk)
	}
	// devmail's senders: two ready, one remote, the rest without a contact.
	var dm api.ContactLookupResult
	if err := s.Call(ctx, api.MethodContactLookup, api.ContactLookupParams{WithPhotos: true, Addresses: []string{
		"martin.benes@example.test", "petr.svoboda@acme.test", "tomas.holub@example.test", "lucie.markova@example.test"}}, &dm); err != nil {
		t.Fatal(err)
	}
	if dm.Photos["martin.benes@example.test"].State != api.ContactPhotoReady || dm.Photos["petr.svoboda@acme.test"].State != api.ContactPhotoReady ||
		dm.Photos["tomas.holub@example.test"].State != api.ContactPhotoRemote || len(dm.Photos) != 3 {
		t.Fatalf("devmail lookup %+v", dm.Photos)
	}
	var ph api.ContactPhotoResult
	if err := s.Call(ctx, api.MethodContactPhoto, api.ContactPhotoParams{ContactID: ref.ContactID}, &ph); err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(ph.Data))
	if err != nil || format != "jpeg" || cfg.Width != 96 || ph.State != api.ContactPhotoStateReady || ph.MIME != "image/jpeg" {
		t.Fatalf("photo %v %s %+v", err, format, cfg)
	}

	// The remote one is "fetched", without a change notification (the
	// daemon sends none either).
	if err := s.Call(ctx, api.MethodContactPhoto, api.ContactPhotoParams{ContactID: marie}, &ph); err != nil || ph.State != api.ContactPhotoStateReady || notified != 0 {
		t.Fatalf("remote %v %+v %d", err, ph.State, notified)
	}
	// Without a photo: none.
	for _, c := range list.Contacts {
		if c.Photo == "" {
			if err := s.Call(ctx, api.MethodContactPhoto, api.ContactPhotoParams{ContactID: c.ID}, &ph); err != nil || ph.State != api.ContactPhotoStateNone {
				t.Fatalf("none %v %+v", err, ph)
			}
			break
		}
	}
	// contact.update keeps a photo as it is (F1).
	var get api.ContactGetResult
	if err := s.Call(ctx, api.MethodContactGet, api.ContactGetParams{ContactID: ref.ContactID}, &get); err != nil {
		t.Fatal(err)
	}
	card := get.Contact
	card.Photo, card.Note = "", "x"
	if err := s.Call(ctx, api.MethodContactUpdate, api.ContactUpdateParams{ContactID: card.ID, Revision: card.Revision, Card: card}, nil); err != nil {
		t.Fatal(err)
	}
	_ = s.Call(ctx, api.MethodContactGet, api.ContactGetParams{ContactID: ref.ContactID}, &get)
	if get.Contact.Photo != api.ContactPhotoReady {
		t.Fatalf("update dropped the photo: %+v", get.Contact.Photo)
	}
}
