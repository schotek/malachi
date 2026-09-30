// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package botclean

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/jira/botclean/*.want.html")

// Comments as Jira renders them: each <name>.html cleaned must give
// <name>.want.html.
func TestFixtures(t *testing.T) {
	c := mustCompile(t, Rules{
		BotNames:        []string{issueSync},
		MetadataFilters: []string{`^Remote comment create date:.*$`},
		AuthorPrefixes:  []string{"ACME"},
	})
	cases := map[string]struct {
		author, wantAuthor string
		created            time.Time
		via                string
	}{
		// Cloud: header and metadata paragraphs, a mention, an emoticon, a list.
		"cloud-relayed.html": {issueSync, "Jana Dvořáková", utc(2026, 6, 10, 12, 39), "Issue Sync"},
		// Data Center wiki rendering: header ends with <br/>; the header and
		// metadata lines quoted in a code block and a quote stay.
		"dc-relayed.html": {"Issue Sync - Synchronization for Jira", "Jana Dvořáková", utc(2026, 6, 15, 19, 5), "Issue Sync"},
		// A person's comment: only the metadata line of the paragraph goes.
		"human-metadata.html": {"Eva Horáková", "", time.Time{}, ""},
	}
	names := fixtureNames(t)
	if len(names) != len(cases) {
		t.Fatalf("fixtures %v, cases for %d", names, len(cases))
	}
	for _, name := range names {
		tc, ok := cases[name]
		if !ok {
			t.Fatalf("no case for %s", name)
		}
		t.Run(name, func(t *testing.T) {
			got := c.Clean(tc.author, readFixture(t, name), fallback)
			if got.AuthorName != tc.wantAuthor || !got.Created.Equal(tc.created) || got.Via != tc.via {
				t.Errorf("AuthorName %q Created %v Via %q, want %q %v %q", got.AuthorName, got.Created, got.Via, tc.wantAuthor, tc.created, tc.via)
			}
			wantName := strings.TrimSuffix(name, ".html") + ".want.html"
			if *update {
				if err := os.WriteFile(filepath.Join(fixtureDir, wantName), []byte(got.HTML), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			if want := readFixture(t, wantName); got.HTML != want {
				t.Errorf("HTML:\n%s\nwant:\n%s", got.HTML, want)
			}
		})
	}
}
