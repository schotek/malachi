// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"context"
	"log/slog"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	imime "github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestUserAddress(t *testing.T) {
	for _, tc := range []struct {
		id, name, want string
	}{
		{"JIRAUSER10100", "Jana Dvořáková", "JIRAUSER10100"},
		{"jana.dvorakova", "Jana Dvořáková", "jana.dvorakova"},
		{"a_b-c.d", "", "a_b-c.d"},
		{"712020:0c9f3c3e-7a9c-4b6e-9d55-3b1f0e8a2c41", "Petr Svoboda", "u-" + hash24("712020:0c9f3c3e-7a9c-4b6e-9d55-3b1f0e8a2c41")},
		{strings.Repeat("a", 64), "", strings.Repeat("a", 64)},
		{strings.Repeat("a", 65), "", "u-" + hash24(strings.Repeat("a", 65))},
		{"a..b", "", "u-" + hash24("a..b")},
		{"a.", "", "u-" + hash24("a.")},
		{".a", "", "u-" + hash24(".a")},
		{"-a", "", "u-" + hash24("-a")},
		{"jana@acme", "", "u-" + hash24("jana@acme")},
		{"", "Eva Horáková", "n-" + hash24("eva horáková")},
		{"", "  Eva \t Horáková ", "n-" + hash24("eva horáková")},
		{"", "", "anonymous"},
	} {
		got := userAddress(tc.id, tc.name)
		if got.Address != tc.want+"@"+userDomain || got.Name != tc.name {
			t.Errorf("userAddress(%q, %q) = %+v, want %s", tc.id, tc.name, got, tc.want)
			continue
		}
		// Every synthesised address is one plain mailbox.
		if a, err := netmail.ParseAddress("<" + got.Address + ">"); err != nil || a.Address != got.Address {
			t.Errorf("%s does not parse: %v", got.Address, err)
		}
	}
}

func TestMsgIDHost(t *testing.T) {
	for in, want := range map[string]string{
		"acme.atlassian.net": "acme.atlassian.net",
		"Jira.Example.ORG.":  "jira.example.org",
		"[::1]":              "1",
		"127.0.0.1":          "127.0.0.1",
		"":                   "site",
		"a..b":               "a.b",
		"xn--jra-6na.test":   "xn--jra-6na.test",
		"under_score.test":   "under-score.test",
	} {
		if got := msgIDHost(in); got != want {
			t.Errorf("msgIDHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEventText(t *testing.T) {
	got := eventText([]api.IssueChange{
		{Field: api.IssueFieldStatus, From: "To Do", To: "In Progress"},
		{Field: api.IssueFieldAssignee, To: "Jana Dvořáková"},
		{Field: api.IssueFieldAssignee, From: "Petr Svoboda"},
	})
	want := "To Do → In Progress\n— → Jana Dvořáková\nPetr Svoboda → —"
	if got != want {
		t.Fatalf("eventText = %q", got)
	}
}

// synthFor builds the synthesis of the fake site's account.
func synthFor(t *testing.T, f *jiratest.Server, cfg api.JiraConfig) (*synth, Remote) {
	t.Helper()
	c, err := NewClient(optionsOf(f))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRemote(c)
	y, err := newSynth(cfg, compileRules(cfg, slog.New(slog.DiscardHandler)), r, c, User{ID: f.Me}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return y, r
}

// siteItems reads an issue from the fake site as the syncer does and
// describes its messages.
func siteItems(t *testing.T, y *synth, r Remote, id string) (Issue, []*item) {
	t.Helper()
	ctx := context.Background()
	got, err := r.BulkIssues(ctx, []string{id}, IssueOptions{Fields: FieldsAll, Rendered: true})
	if err != nil || len(got) != 1 {
		t.Fatalf("issue %s: %v", id, err)
	}
	cl, err := r.Comments(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	hs, err := r.Changelogs(ctx, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	return got[0], y.items(got[0], cl.Comments, hs[id], false, false)
}

func ctxPath(f *jiratest.Server) string { return f.Site.Path }

func TestSynthRoundTrip(t *testing.T) {
	modesRun(t, func(t *testing.T, mode jiratest.Mode) {
		f := jiratest.New(t, mode)
		stepClock(f, syncT0)
		ctx := context.Background()
		cp := ctxPath(f)
		f.AddSiteFile("/images/logo.png", "image/png", jiratest.PNG)
		is := f.AddIssue("ITSD", "Tiskárna nefunguje", func(is *jiratest.Issue) { is.Reporter = f.Petr })
		spec := f.AddAttachment(is.ID, "zadani.txt", "text/plain", []byte("Zadání: vyměnit toner."))
		logID := f.AddAttachment(is.ID, "log.txt", "text/plain", []byte("E42 paper jam"))
		f.Update(is.ID, func(is *jiratest.Issue) {
			is.Description = `<p>Tiskárna <b>netiskne</b>. <img src="` + cp + `/images/logo.png" alt="logo"></p>` +
				`<p><a href="` + cp + `/browse/ITSD-2">ITSD-2</a></p>`
		})
		comment := f.AddComment(is.ID, f.Me, `<p>Log: <a href="`+cp+`/secure/attachment/`+logID+`/log.txt">log.txt</a></p>`)
		f.SetStatus(is.ID, "3", f.Petr)

		cfg := f.Config()
		y, r := synthFor(t, f, cfg)
		issue, items := siteItems(t, y, r, is.ID)
		if len(items) != 3 {
			t.Fatalf("items = %d", len(items))
		}
		desc, com, ev := items[0], items[1], items[2]
		if desc.remoteID != "i:"+is.ID || com.remoteID != "c:"+comment || ev.kind != api.IssueItemEvent {
			t.Fatalf("items = %s %s %s", desc.remoteID, com.remoteID, ev.remoteID)
		}
		if len(desc.files) != 1 || desc.files[0].ID != spec || len(com.files) != 1 || com.files[0].ID != logID {
			t.Fatalf("files: description %+v, comment %+v", desc.files, com.files)
		}
		if !com.mine || desc.mine {
			t.Fatal("mine is wrong")
		}

		for _, it := range items {
			raw1, err := y.build(ctx, issue, it)
			if err != nil {
				t.Fatal(err)
			}
			raw2, err := y.build(ctx, issue, it)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw1, raw2) {
				t.Fatalf("%s: two builds differ", it.remoteID)
			}
			if !bytes.Contains(raw1, []byte(revisionHeader+": "+it.revision+"\r\n")) {
				t.Fatalf("%s: no revision header", it.remoteID)
			}
			p, err := imime.Parse(bytes.NewReader(raw1), imime.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if p.MessageID != it.msgID || p.Subject != "ITSD-1: Tiskárna nefunguje" || len(p.From) != 1 ||
				p.From[0].Address != it.from.Address || p.From[0].Name != it.from.Name || !p.Date.Equal(it.date) {
				t.Fatalf("%s: parsed %+v", it.remoteID, p)
			}
			if it != desc && (p.InReplyTo != desc.msgID || len(p.References) != 1 || p.References[0] != desc.msgID) {
				t.Fatalf("%s: in reply to %q %v", it.remoteID, p.InReplyTo, p.References)
			}
			switch it {
			case desc:
				if !p.HasHTML || !strings.Contains(p.RawHTML, `src="cid:img1.`+desc.msgID+`"`) ||
					!strings.Contains(p.RawHTML, `href="`+f.Site.String()+`/browse/ITSD-2"`) || !strings.Contains(p.Text, "netiskne") {
					t.Fatalf("description html %q text %q", p.RawHTML, p.Text)
				}
				if len(p.Attachments) != 2 {
					t.Fatalf("description parts = %+v", p.Attachments)
				}
				pic, file := p.Attachments[0], p.Attachments[1]
				if pic.PartID != "1.2.2" || pic.ContentType != "image/png" || pic.Filename != "logo.png" || !pic.Inline {
					t.Fatalf("picture = %+v", pic)
				}
				if file.PartID != "2" || file.Filename != "zadani.txt" || file.Size != int64(len("Zadání: vyměnit toner.")) {
					t.Fatalf("file = %+v", file)
				}
			case com:
				if len(p.Attachments) != 1 || p.Attachments[0].Filename != "log.txt" || p.Attachments[0].PartID != "2" {
					t.Fatalf("comment parts = %+v", p.Attachments)
				}
			case ev:
				if p.HasHTML || p.Text != "To Do → In Progress" || len(p.Attachments) != 0 {
					t.Fatalf("event = %q html %v", p.Text, p.HasHTML)
				}
			}
		}

		// The revision follows the content, not the subject.
		before := desc.revision
		f.Update(is.ID, func(is *jiratest.Issue) { is.Summary = "Jiný název" })
		_, again := siteItems(t, y, r, is.ID)
		if again[0].revision != before {
			t.Fatal("a rename changed the description's revision")
		}
		f.Update(is.ID, func(is *jiratest.Issue) { is.Description += "<p>Doplněno.</p>" })
		_, again = siteItems(t, y, r, is.ID)
		if again[0].revision == before {
			t.Fatal("an edited description kept its revision")
		}
	})
}

func TestSynthBudgets(t *testing.T) {
	f := jiratest.New(t, jiratest.DC)
	stepClock(f, syncT0)
	ctx := context.Background()
	cp := ctxPath(f)
	var imgs string
	for _, name := range []string{"a", "b", "c"} {
		f.AddSiteFile("/images/"+name+".png", "image/png", jiratest.PNG)
		imgs += `<img src="` + cp + `/images/` + name + `.png">`
	}
	is := f.AddIssue("WEB", "Rozpočet", func(is *jiratest.Issue) { is.Description = "<p>" + imgs + "</p>" })
	big := f.AddAttachment(is.ID, "velky.bin", "application/octet-stream", bytes.Repeat([]byte{1}, 400))
	f.AddAttachment(is.ID, "maly.txt", "text/plain", []byte("malý"))
	ref := f.AddAttachment(is.ID, "odkaz.bin", "application/octet-stream", bytes.Repeat([]byte{2}, 400))
	f.AddComment(is.ID, f.Petr, `<p><a href="`+cp+`/secure/attachment/`+ref+`/odkaz.bin">odkaz.bin</a></p>`)

	y, r := synthFor(t, f, f.Config())
	y.picturesCap, y.partsCap = 2, 300
	issue, items := siteItems(t, y, r, is.ID)
	raw, err := y.build(ctx, issue, items[0])
	if err != nil {
		t.Fatal(err)
	}
	p, err := imime.Parse(bytes.NewReader(raw), imime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var inline, files []string
	for _, a := range p.Attachments {
		if a.Inline {
			inline = append(inline, a.Filename)
		} else {
			files = append(files, a.Filename)
		}
	}
	if strings.Join(inline, ",") != "a.png,b.png" || strings.Join(files, ",") != "maly.txt" {
		t.Fatalf("inline %v, files %v", inline, files)
	}
	// The third picture keeps its link; the file over budget gets one.
	if !strings.Contains(p.RawHTML, `src="`+f.Site.String()+`/images/c.png"`) {
		t.Fatalf("html = %q", p.RawHTML)
	}
	link := `<a href="` + f.Site.String() + `/secure/attachment/` + big + `/velky.bin">velky.bin</a>`
	if !strings.Contains(p.RawHTML, link) {
		t.Fatalf("no link to the file over budget: %q", p.RawHTML)
	}
	// A file over budget is not even downloaded.
	if n := len(f.RequestsTo("", "/secure/attachment/"+big)); n != 0 {
		t.Fatalf("the file over budget was requested %d times", n)
	}
	// The comment's file over budget: its own link stays, none is added.
	raw, err = y.build(ctx, issue, items[1])
	if err != nil {
		t.Fatal(err)
	}
	p, _ = imime.Parse(bytes.NewReader(raw), imime.DefaultLimits())
	if len(p.Attachments) != 0 || strings.Count(p.RawHTML, "odkaz.bin</a>") != 1 {
		t.Fatalf("comment = %q, parts %+v", p.RawHTML, p.Attachments)
	}

	// The budget leaves room for the text parts.
	if b := partsBudget(""); b != maxPartsBytes {
		t.Fatalf("budget = %d", b)
	}
	if b := partsBudget(strings.Repeat("x", 3<<20)); b >= maxPartsBytes || b <= 0 {
		t.Fatalf("budget with 3 MiB of HTML = %d", b)
	}
	if b := partsBudget(strings.Repeat("x", 5<<20)); b != 0 {
		t.Fatalf("budget with 5 MiB of HTML = %d", b)
	}
	// Whatever the budget, a message fits ingest's cap.
	html := strings.Repeat("á", 2<<20) // two bytes each, six when quoted-printable
	if room := partsBudget(html); float64(room)*4/3*78/76+6*float64(len(html))+mimeReserve > 25<<20 {
		t.Fatalf("budget %d overflows", room)
	}
}

func TestItemsAssignFiles(t *testing.T) {
	c, err := NewClient(Options{SiteURL: jiratest.DCSite, Deployment: api.JiraDataCenter})
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.JiraConfig{SiteURL: jiratest.DCSite, Deployment: api.JiraDataCenter}
	y, err := newSynth(cfg, compileRules(cfg, slog.New(slog.DiscardHandler)), nil, c, User{ID: "me"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := syncT0
	atts := []Attachment{
		{ID: "30", Filename: "c.txt", Size: 1}, {ID: "4", Filename: "a.txt", Size: 1}, {ID: "200", Filename: "b.txt", Size: 1},
		{ID: "5", Filename: "cizi.txt", Size: 1},
	}
	is := Issue{ID: "7", Key: "WEB-1", Summary: "Soubory", Created: now, Updated: now, Attachments: atts,
		DescriptionHTML: `<img src="/jira/secure/thumbnail/30/_thumb_30.png">`,
		Reporter:        User{ID: "me", Name: "Jana"}}
	comments := []Comment{
		{ID: "11", Author: User{ID: "p", Name: "Petr"}, Created: now, Updated: now,
			BodyHTML: `<a href="https://jira.acme.test/jira/secure/attachment/200/b.txt">b</a> <a href="https://elsewhere.test/jira/secure/attachment/5/x">x</a>`},
		{ID: "12", Author: User{ID: "p", Name: "Petr"}, Created: now, Updated: now.Add(2 * time.Minute),
			BodyHTML: `<a href="/jira/rest/api/2/attachment/content/30">c</a>`},
	}
	items := y.items(is, comments, nil, true, false)
	got := map[string]string{}
	for _, it := range items {
		var ids []string
		for _, a := range it.files {
			ids = append(ids, a.ID)
		}
		got[it.remoteID] = strings.Join(ids, ",")
	}
	// The description: its own picture's file and those nobody names,
	// sorted by id (numerically); another host's link names nothing.
	if got["i:7"] != "4,5,30" || got["c:11"] != "200" || got["c:12"] != "30" {
		t.Fatalf("files = %v", got)
	}
	if items[1].edited || !items[2].edited || items[1].visibility != api.CommentPublic || !items[0].mine {
		t.Fatalf("items = %+v %+v", items[1], items[2])
	}
}
