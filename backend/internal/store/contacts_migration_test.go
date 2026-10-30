// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import "testing"

// 0023 creates the address-book tables, indexes a contact for search and
// drops a book's contacts, addresses and index rows with the book.
func TestContactsMigration(t *testing.T) {
	s := openTestStore(t)
	for _, name := range []string{"ab_books", "ab_contacts", "ab_emails", "ab_contacts_fts", "ab_contacts_fts_docs",
		"ab_contacts_fts_ai", "ab_contacts_fts_au", "ab_contacts_fts_ad"} {
		if countRows(t, s, `SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, name) != 1 {
			t.Errorf("%s missing", name)
		}
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'ab%' AND tbl_name != 'ab_contacts'`); n != 0 {
		t.Fatalf("%d triggers of 0023 outside ab_contacts", n)
	}
	for _, q := range []string{
		`INSERT INTO ab_books (id, account_id, remote_id, name) VALUES ('b_1', 'acc_1', '/ab/', 'Contacts')`,
		`INSERT INTO ab_contacts (id, book_id, remote_id, raw, raw_kind, display_name, org, emails)
		 VALUES ('p_1', 'b_1', '/ab/1.vcf', x'00', 'vcard', 'Jana Nováková', 'Acme', 'jana@example.org')`,
		`INSERT INTO ab_emails (contact_id, position, address) VALUES ('p_1', 0, 'jana@example.org')`,
		`UPDATE ab_contacts SET display_name = 'Jana Svobodová' WHERE id = 'p_1'`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM ab_contacts_fts WHERE ab_contacts_fts MATCH 'svobodova'`); n != 1 {
		t.Fatalf("search after update: %d", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM ab_contacts_fts WHERE ab_contacts_fts MATCH 'novakova'`); n != 0 {
		t.Fatalf("old name still indexed: %d", n)
	}
	if _, err := s.db.Exec(`DELETE FROM ab_books WHERE id = 'b_1'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"ab_contacts", "ab_emails", "ab_contacts_fts_docs"} {
		if n := countRows(t, s, `SELECT COUNT(*) FROM `+table); n != 0 {
			t.Errorf("%s keeps %d rows after the book went", table, n)
		}
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM ab_contacts_fts WHERE ab_contacts_fts MATCH 'acme'`); n != 0 {
		t.Errorf("index keeps the removed contact")
	}
}
