// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contacts

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestAvatar(t *testing.T) {
	row := Row{Contact: api.ContactSummary{ID: "p_1", BookID: "b_1"}, Initials: "JN"}
	if a := AvatarOfRow(row, PhotoReady); a.Contact != "p_1" || a.Initials != "JN" || !a.MayShowPhoto() {
		t.Fatalf("ready %+v", a)
	}
	if a := AvatarOfRow(row, PhotoRemote); a.Contact != "p_1" {
		t.Fatal("remote")
	}
	if a := AvatarOfRow(row, PhotoNone); a.MayShowPhoto() || a.Initials != "JN" {
		t.Fatal("none")
	}
	if a := AvatarOfRow(row, "bogus"); a.MayShowPhoto() {
		t.Fatal("an unknown state")
	}
	recent := Row{Contact: api.ContactSummary{ID: "r_1", BookID: api.AddressBookRecent}}
	if AvatarOfRow(recent, PhotoReady).MayShowPhoto() {
		t.Fatal("a recent contact with a photo")
	}
	card := api.ContactCard{ID: "p_2", BookID: "b_1"}
	if a := AvatarOfDetail(card, Detail{Initials: "A"}, PhotoReady); a.Contact != "p_2" || a.Initials != "A" {
		t.Fatal("detail")
	}
	if AvatarOfDetail(api.ContactCard{ID: "r_9"}, Detail{}, PhotoReady).MayShowPhoto() {
		t.Fatal("recent detail")
	}

	// Senders: by the exact address, never for the user's own messages.
	a := AvatarOfSender(" Jana@Firma.CZ ", "Jana Nová", false)
	if a.Sender != "jana@firma.cz" || a.Initials != "JN" || a.Contact != "" {
		t.Fatalf("sender %+v", a)
	}
	if a := AvatarOfSender("jana@firma.cz", "Jana", true); a.MayShowPhoto() || a.Initials != "J" {
		t.Fatal("own message")
	}
	if AvatarOfSender("", "Jana", false).MayShowPhoto() || AvatarOfSender("undisclosed", "", false).MayShowPhoto() {
		t.Fatal("no address")
	}

	// monochrome-avatars greys initials, never a photo.
	if !Monochrome(true, false) || Monochrome(true, true) || Monochrome(false, false) {
		t.Fatal("monochrome")
	}
}

func TestNearRows(t *testing.T) {
	for _, c := range []struct{ first, last, count, from, to int }{
		{10, 19, 100, 0, 30},
		{40, 49, 100, 30, 60},
		{90, 99, 100, 80, 100},
		{0, 4, 3, 0, 3},
		{5, 4, 100, 0, 0},
		{0, 0, 0, 0, 0},
		{-3, 2, 10, 0, 6},
	} {
		if from, to := NearRows(c.first, c.last, c.count); from != c.from || to != c.to {
			t.Errorf("NearRows(%d, %d, %d) = %d, %d", c.first, c.last, c.count, from, to)
		}
	}
}
