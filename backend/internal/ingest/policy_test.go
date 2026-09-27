// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ingest

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

var policyNow = time.Date(2026, 9, 27, 15, 30, 0, 0, time.UTC)

// parsedWith is a parse result with the given attachments and HTML body.
func parsedWith(html string, atts ...api.Attachment) *mime.Parsed {
	return &mime.Parsed{HasHTML: html != "", RawHTML: html, HTMLPartID: "1.1", TextPartID: "", Attachments: atts}
}

func att(id string, size int64, contentID string) api.Attachment {
	return api.Attachment{PartID: id, Filename: "f" + id, ContentType: "application/octet-stream", Size: size, ContentID: contentID}
}

// oldTarget is an inbox message received long before policyNow.
func oldTarget() Target {
	return Target{AccountID: "a", MessageID: "m", Role: api.RoleInbox, HasServerCopy: true,
		InternalDate: policyNow.AddDate(0, 0, -400)}
}

func omitted(p Plan) []string {
	return slices.Sorted(maps.Keys(p.Omit))
}

func TestDecidePolicy(t *testing.T) {
	big := int64(api.LargeAttachmentMinBytes)
	p := parsedWith("", att("2", big, ""), att("3", big-1, ""), att("4", 3*big, ""))
	day := 24 * time.Hour
	cases := []struct {
		name  string
		days  int
		age   time.Duration // before policyNow; 0 = no date at all
		want  []string
		bytes int64
	}{
		{"keep all", 0, 400 * day, nil, 0},
		{"small only", api.AttachmentOfflineNone, time.Hour, []string{"2", "4"}, 4 * big},
		{"small only, undated", api.AttachmentOfflineNone, 0, []string{"2", "4"}, 4 * big},
		{"30 days, old", 30, 40 * day, []string{"2", "4"}, 4 * big},
		{"30 days, recent", 30, 5 * day, nil, 0},
		{"30 days, undated counts as new", 30, 0, nil, 0},
		// The cutoff is midnight UTC 30 days before the day of now.
		{"30 days, just before the cutoff", 30, 30*day + 15*time.Hour + 31*time.Minute, []string{"2", "4"}, 4 * big},
		{"30 days, at the cutoff", 30, 30*day + 15*time.Hour + 30*time.Minute, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tg := oldTarget()
			tg.InternalDate = time.Time{}
			if c.age > 0 {
				tg.InternalDate = policyNow.Add(-c.age)
			}
			plan := Decide(p, tg, Policy{AttachmentOfflineDays: c.days}, policyNow, false)
			if got := omitted(plan); !slices.Equal(got, c.want) || plan.OmitBytes != c.bytes {
				t.Errorf("omit %v (%d bytes), want %v (%d)", got, plan.OmitBytes, c.want, c.bytes)
			}
			if plan.CandidateBytes != 4*big {
				t.Errorf("candidate bytes %d, want %d", plan.CandidateBytes, 4*big)
			}
		})
	}
}

// The age is the server's internal date; the Date header, which the sender
// chooses, counts only without one, and its zero value means none.
func TestDecideAgeSource(t *testing.T) {
	p := parsedWith("", att("2", api.LargeAttachmentMinBytes, ""))
	pol := Policy{AttachmentOfflineDays: 30}
	tg := oldTarget()
	tg.InternalDate = policyNow.AddDate(0, 0, -2)
	tg.Date = policyNow.AddDate(-5, 0, 0) // a forged old Date header
	if plan := Decide(p, tg, pol, policyNow, false); len(plan.Omit) != 0 {
		t.Errorf("recent internal date overridden by the Date header: %v", omitted(plan))
	}
	tg.InternalDate = time.Time{}
	if plan := Decide(p, tg, pol, policyNow, false); len(plan.Omit) != 1 {
		t.Errorf("Date header not used without an internal date: %v", omitted(plan))
	}
	tg.Date = time.Time{}
	if plan := Decide(p, tg, pol, policyNow, false); len(plan.Omit) != 0 {
		t.Errorf("a message without dates is old: %v", omitted(plan))
	}
}

func TestDecideNeverOmits(t *testing.T) {
	p := parsedWith("", att("2", 5<<20, ""))
	pol := Policy{AttachmentOfflineDays: api.AttachmentOfflineNone}
	cases := map[string]func(*Target, *bool){
		"drafts":          func(t *Target, _ *bool) { t.Role = api.RoleDrafts },
		"outbox":          func(t *Target, _ *bool) { t.Role = api.RoleOutbox },
		"no server copy":  func(t *Target, _ *bool) { t.HasServerCopy = false },
		"on demand":       func(_ *Target, d *bool) { *d = true },
		"hydrated today":  func(t *Target, _ *bool) { t.HydratedAt = policyNow.Add(-time.Hour) },
		"hydrated 6 days": func(t *Target, _ *bool) { t.HydratedAt = policyNow.Add(-HydratedKeep + time.Minute) },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			tg, onDemand := oldTarget(), false
			mut(&tg, &onDemand)
			plan := Decide(p, tg, pol, policyNow, onDemand)
			if len(plan.Omit) != 0 || plan.OmitBytes != 0 {
				t.Errorf("omitted %v", omitted(plan))
			}
			if plan.CandidateBytes != 5<<20 {
				t.Errorf("candidate bytes %d: the candidates do not depend on the message's circumstances", plan.CandidateBytes)
			}
		})
	}
	tg := oldTarget()
	tg.HydratedAt = policyNow.Add(-HydratedKeep - time.Second)
	if plan := Decide(p, tg, pol, policyNow, false); len(plan.Omit) != 1 {
		t.Errorf("grace over, nothing omitted: %+v", plan)
	}
}

func TestDecideNoCandidates(t *testing.T) {
	pol := Policy{AttachmentOfflineDays: api.AttachmentOfflineNone}
	truncated := parsedWith("", att("2", 5<<20, ""))
	truncated.Truncated = true
	crypto := parsedWith("", att("2", 5<<20, ""))
	crypto.Crypto = true
	for name, p := range map[string]*mime.Parsed{"truncated": truncated, "signed or encrypted": crypto, "nil": nil} {
		if plan := Decide(p, oldTarget(), pol, policyNow, false); plan.Omit != nil || plan.CandidateBytes != 0 {
			t.Errorf("%s: %+v", name, plan)
		}
	}
}

// A part the HTML shows through cid: stays; an Outlook-style Content-ID
// nothing references does not keep a part, and the comparison ignores
// brackets and case the way mime.NormalizeCID does.
func TestDecideReferencedParts(t *testing.T) {
	big := int64(200 << 10)
	html := `<p>logo <img src="cid:Logo@Example.ORG"> and <div style="background:url(cid:bg@example.org)"></div></p>`
	p := parsedWith(html,
		att("1.2", big, "<logo@example.org>"),
		att("1.3", big, "bg@example.org"),
		att("2", big, "outlook-generated@example.org"),
		att("3", big, ""),
	)
	plan := Decide(p, oldTarget(), Policy{AttachmentOfflineDays: api.AttachmentOfflineNone}, policyNow, false)
	if got := omitted(plan); !slices.Equal(got, []string{"2", "3"}) {
		t.Errorf("omit %v, want [2 3]", got)
	}

	// An incomplete reference list (a malachi-cid: URL here) keeps every
	// part with a Content-ID.
	p.RawHTML += `<img src="malachi-cid:acc/msg/2">`
	plan = Decide(p, oldTarget(), Policy{AttachmentOfflineDays: api.AttachmentOfflineNone}, policyNow, false)
	if got := omitted(plan); !slices.Equal(got, []string{"3"}) || plan.CandidateBytes != big {
		t.Errorf("incomplete references: omit %v, candidates %d", got, plan.CandidateBytes)
	}

	// Too many references to list completely: the same.
	var many strings.Builder
	for i := range 1100 {
		fmt.Fprintf(&many, `<img src="cid:x%d@e">`, i)
	}
	p.RawHTML = many.String()
	plan = Decide(p, oldTarget(), Policy{AttachmentOfflineDays: api.AttachmentOfflineNone}, policyNow, false)
	if got := omitted(plan); !slices.Equal(got, []string{"3"}) {
		t.Errorf("capped references: omit %v", got)
	}
}

// The chosen text and HTML bodies are never attachments of the parse, but
// a parse result that lists one anyway must not lose it.
func TestDecideKeepsBodies(t *testing.T) {
	p := parsedWith("<p>x</p>", att("1.1", 1<<20, ""), att("", 1<<20, ""))
	plan := Decide(p, oldTarget(), Policy{AttachmentOfflineDays: api.AttachmentOfflineNone}, policyNow, false)
	if len(plan.Omit) != 0 || plan.CandidateBytes != 0 {
		t.Errorf("a body or an unnamed part is a candidate: %+v", plan)
	}
}

func TestPolicyCutoff(t *testing.T) {
	if c := (Policy{}).Cutoff(policyNow); !c.IsZero() {
		t.Errorf("keep all has a cutoff: %v", c)
	}
	if c := (Policy{AttachmentOfflineDays: -1}).Cutoff(policyNow); !c.IsZero() {
		t.Errorf("small only has a cutoff: %v", c)
	}
	want := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	if c := (Policy{AttachmentOfflineDays: 30}).Cutoff(policyNow); !c.Equal(want) {
		t.Errorf("cutoff %v, want %v", c, want)
	}
}
