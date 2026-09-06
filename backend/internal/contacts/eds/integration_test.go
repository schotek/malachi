// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eds

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestIntegration lists the address books of the real Evolution Data
// Server and runs one search over them. It is opt-in:
// MALACHI_TEST_EDS=1 go test -run Integration -v. Only counts and book
// names are logged, never the contacts.
func TestIntegration(t *testing.T) {
	if os.Getenv("MALACHI_TEST_EDS") != "1" {
		t.Skip("set MALACHI_TEST_EDS=1 with a session bus to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := New(nil)
	defer c.Close()

	books, err := c.Books(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range books {
		t.Logf("%s %q backend=%s collection=%s goa=%s email=%q", b.UID, b.Name, b.Backend, b.Collection, b.GOAAccountID, b.Email)
	}
	if len(books) == 0 {
		t.Skip("no address books")
	}
	query := os.Getenv("MALACHI_TEST_EDS_QUERY")
	if query == "" {
		query = "ja"
	}
	start := time.Now()
	found, err := c.Search(ctx, books, query, 100)
	if err != nil {
		t.Fatal(err)
	}
	perBook := map[string]int{}
	for _, f := range found {
		perBook[f.Book]++
	}
	t.Logf("%d contacts for %q in %v: %v", len(found), query, time.Since(start).Round(time.Millisecond), perBook)
}
