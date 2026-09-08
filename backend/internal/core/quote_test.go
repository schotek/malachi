// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// seedQuoted adds a fetched message to the inbox from a testdata/mime
// sample, the way the syncer stores one: the envelope from the headers,
// the parsed body, the raw file. It returns the message id.
func (m *mailbox) seedQuoted(t *testing.T, name string) api.MessageID {
	t.Helper()
	ctx := context.Background()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mime", name))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mime.Parse(bytes.NewReader(raw), mime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	row := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: uint32(200 + len(m.msgs)),
		Subject: parsed.Subject, Date: parsed.Date, From: parsed.From, To: parsed.To, CC: parsed.CC, ReplyTo: parsed.ReplyTo,
		RFCMessageID: parsed.MessageID, Size: int64(len(raw))}
	if row.Date.IsZero() {
		row.Date = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	}
	if err := m.b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
		t.Fatal(err)
	}
	if err := m.b.store.SetMessageBody(ctx, row.ID, store.BodyUpdate{
		Text: parsed.Text, HasHTML: parsed.HasHTML, Snippet: parsed.Snippet,
		Attachments: parsed.Attachments, HasAttachments: parsed.HasAttachments,
		Headers: parsed.Headers, State: store.BodyFetched,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.b.store.WriteMessageRaw(ctx, string(m.acc), row.ID, bytes.NewReader(raw), 25<<20); err != nil {
		t.Fatal(err)
	}
	m.msgs = append(m.msgs, api.MessageID(row.ID))
	return api.MessageID(row.ID)
}

// create is draft.create for the mailbox's account.
func (m *mailbox) create(t *testing.T, mode api.ComposeMode, id api.MessageID, attribution string) *api.DraftCreateResult {
	t.Helper()
	res, err := m.b.Drafts().Create(context.Background(), api.DraftCreateParams{
		AccountID: m.acc, Mode: mode, MessageID: id, Attribution: attribution,
	})
	if err != nil {
		t.Fatalf("draft.create %s: %v", mode, err)
	}
	return res
}

// cidRefs lists the Content-IDs the HTML references.
var cidRefs = regexp.MustCompile(`cid:([^"'\s>]+)`)

// checkQuoteClean is what every quoted body must satisfy: nothing active,
// nothing remote, no reference to the original's parts by any spelling,
// cid: only to the draft's own inline copies.
func checkQuoteClean(t *testing.T, d api.Draft) {
	t.Helper()
	for _, bad := range []string{"<script", "<form", "<iframe", "onclick=", "onerror=", "onload=", "javascript:", "vbscript:",
		"malachi-cid:", "../", `src="http`, `src="data:`, "url(", "@import", "<style"} {
		if strings.Contains(strings.ToLower(d.HTMLBody), bad) {
			t.Errorf("html contains %q:\n%s", bad, d.HTMLBody)
		}
	}
	own := map[string]bool{}
	for _, a := range d.Attachments {
		if a.Inline {
			own[a.ContentID] = true
		}
	}
	for _, ref := range cidRefs.FindAllStringSubmatch(d.HTMLBody, -1) {
		if !own[ref[1]] {
			t.Errorf("html references cid:%s, which is not an inline attachment of the draft", ref[1])
		}
	}
}

// saveRoundTrip saves the created draft and checks that the backend stored
// it as it was handed out: the sanitiser's own output is its fixed point.
func saveRoundTrip(t *testing.T, m *mailbox, d api.Draft) *api.DraftSaveResult {
	t.Helper()
	saved, err := m.b.Drafts().Save(context.Background(), api.DraftSaveParams{Draft: d})
	if err != nil {
		t.Fatalf("draft.save of the created draft: %v", err)
	}
	if saved.HTMLBody != d.HTMLBody {
		t.Errorf("draft.save changed the html:\n got %q\nwant %q", saved.HTMLBody, d.HTMLBody)
	}
	if saved.TextBody != d.TextBody {
		t.Errorf("draft.save changed the text:\n got %q\nwant %q", saved.TextBody, d.TextBody)
	}
	if saved.Blocked != (api.BlockedContent{}) {
		t.Errorf("draft.save removed something from the created draft: %+v", saved.Blocked)
	}
	if len(saved.Attachments) != len(d.Attachments) {
		t.Errorf("draft.save kept %d of %d attachments", len(saved.Attachments), len(d.Attachments))
	}
	return saved
}

func TestDraftCreateReply(t *testing.T) {
	m := seedMailbox(t)
	id := m.seedQuoted(t, "html-inline-cid.eml")
	res := m.create(t, api.ComposeReply, id, "On Monday, Carol <carol@example.org> wrote:")
	d := res.Draft

	if d.ID != "" || d.Version != 0 || d.AccountID != m.acc {
		t.Errorf("template identity = %q v%d %q", d.ID, d.Version, d.AccountID)
	}
	// Reply-To wins over From; nobody else.
	if len(d.To) != 1 || d.To[0] != (api.Address{Name: "Carol Work", Address: "carol.work@example.org"}) || len(d.CC) != 0 || len(d.BCC) != 0 {
		t.Errorf("recipients = to %v cc %v bcc %v", d.To, d.CC, d.BCC)
	}
	if d.Subject != "Re: Pictures" || d.InReplyTo != id || d.Forwarding != "" {
		t.Errorf("subject %q inReplyTo %q forwarding %q", d.Subject, d.InReplyTo, d.Forwarding)
	}
	if res.Quoted != api.QuoteHTML {
		t.Errorf("quoted = %q", res.Quoted)
	}
	checkQuoteClean(t, d)

	// The attribution is escaped above the cite block, the formatting kept.
	wantPrefix := `<p><br/></p><div>On Monday, Carol &lt;carol@example.org&gt; wrote:</div><blockquote type="cite"><p>Hello <b>there</b>, a picture: <img src="cid:`
	if !strings.HasPrefix(d.HTMLBody, wantPrefix) {
		t.Errorf("html:\n got %q\nwant prefix %q", d.HTMLBody, wantPrefix)
	}
	if !strings.HasSuffix(d.HTMLBody, "</blockquote>") {
		t.Errorf("html does not end with the cite block: %q", d.HTMLBody)
	}
	// The picture was copied under a fresh id and every spelling of the
	// original reference points at the copy; the calendar part a cid:
	// named is not a picture and was left out.
	if len(d.Attachments) != 1 {
		t.Fatalf("attachments = %+v", d.Attachments)
	}
	pic := d.Attachments[0]
	if !pic.Inline || pic.ContentType != "image/png" || pic.Filename != "pic.png" || !strings.HasSuffix(pic.ContentID, "@malachi.local") || pic.Size == 0 {
		t.Errorf("copied picture = %+v", pic)
	}
	if n := strings.Count(d.HTMLBody, `src="cid:`+pic.ContentID+`"`); n != 3 {
		t.Errorf("the copy is referenced %d times, want 3:\n%s", n, d.HTMLBody)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].ContentID != "cal@example.org" || res.Skipped[0].Filename != "invite.ics" {
		t.Errorf("skipped = %+v", res.Skipped)
	}
	if res.Blocked.RemoteImages != 1 || res.Blocked.Scripts != 1 || res.Blocked.EventHandlers != 1 || res.Blocked.DangerousURLs < 1 {
		t.Errorf("blocked = %+v", res.Blocked)
	}
	// The text alternative quotes with "> ", nested quotes with "> > ".
	if !strings.HasPrefix(d.TextBody, "On Monday, Carol <carol@example.org> wrote:\n> Hello there, a picture: [pic]\n>\n> Other spellings of it: [enc] [br]\n") {
		t.Errorf("text = %q", d.TextBody)
	}
	if !strings.Contains(d.TextBody, "\n> > older link <https://example.org/x>") {
		t.Errorf("text lacks the nested quote: %q", d.TextBody)
	}
	saveRoundTrip(t, m, d)
}

func TestDraftCreateReplyAll(t *testing.T) {
	m := seedMailbox(t)
	id := m.seedQuoted(t, "html-inline-cid.eml")
	d := m.create(t, api.ComposeReplyAll, id, "").Draft
	if len(d.To) != 1 || d.To[0].Address != "carol.work@example.org" {
		t.Errorf("to = %v", d.To)
	}
	// To and CC of the original, without the account's own address.
	want := []api.Address{{Name: "Dave", Address: "dave@example.org"}, {Name: "Erin", Address: "erin@example.org"}}
	if len(d.CC) != len(want) || d.CC[0] != want[0] || d.CC[1] != want[1] {
		t.Errorf("cc = %v, want %v", d.CC, want)
	}
	// Without an attribution the quote starts right under the empty line.
	if !strings.HasPrefix(d.HTMLBody, `<p><br/></p><blockquote type="cite">`) {
		t.Errorf("html = %q", d.HTMLBody)
	}
	if !strings.HasPrefix(d.TextBody, "> Hello there") {
		t.Errorf("text = %q", d.TextBody)
	}
}

// A message of one's own is answered to whom it went; a note to oneself
// to oneself.
func TestDraftCreateReplyToOwnMessage(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	seed := func(uid uint32, from, to, cc []api.Address) api.MessageID {
		row := &store.Message{AccountID: string(m.acc), FolderID: string(m.inbox), UID: uid, Subject: "mine",
			Date: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC), From: from, To: to, CC: cc}
		if err := m.b.store.UpsertMessages(ctx, []*store.Message{row}); err != nil {
			t.Fatal(err)
		}
		return api.MessageID(row.ID)
	}
	me := api.Address{Name: "Me", Address: "me@example.invalid"}
	bob := api.Address{Name: "Bob", Address: "bob@example.org"}
	sent := seed(50, []api.Address{me}, []api.Address{bob, {Address: "ME@example.invalid"}}, []api.Address{{Name: "x\r\nBcc: y", Address: "carol@example.org"}, {Address: "not an address"}})
	note := seed(51, []api.Address{me}, []api.Address{me}, nil)

	d := m.create(t, api.ComposeReplyAll, sent, "").Draft
	if len(d.To) != 1 || d.To[0] != bob {
		t.Errorf("own message: to = %v", d.To)
	}
	// The name that would break a header goes, the address stays; the
	// unparsable address goes altogether.
	if len(d.CC) != 1 || d.CC[0] != (api.Address{Address: "carol@example.org"}) {
		t.Errorf("own message: cc = %v", d.CC)
	}
	if d := m.create(t, api.ComposeReply, note, "").Draft; len(d.To) != 1 || d.To[0] != me {
		t.Errorf("note to self: to = %v", d.To)
	}
	// Nothing to quote: the body was never fetched.
	if res := m.create(t, api.ComposeReply, note, "x wrote:"); res.Quoted != api.QuoteNone || res.Draft.HTMLBody != "" || res.Draft.TextBody != "" {
		t.Errorf("unfetched body quoted as %q: %+v", res.Quoted, res.Draft)
	}
}

func TestDraftCreateForward(t *testing.T) {
	m := seedMailbox(t)
	id := m.seedQuoted(t, "html-inline-cid.eml")
	res := m.create(t, api.ComposeForward, id, "---------- Forwarded message ----------\nFrom: Carol <carol@example.org>\nSubject: Pictures")
	d := res.Draft
	if len(d.To) != 0 || len(d.CC) != 0 || d.Subject != "Fwd: Pictures" || d.Forwarding != id || d.InReplyTo != "" {
		t.Errorf("header = to %v cc %v subject %q forwarding %q inReplyTo %q", d.To, d.CC, d.Subject, d.Forwarding, d.InReplyTo)
	}
	if res.Quoted != api.QuoteHTML {
		t.Errorf("quoted = %q", res.Quoted)
	}
	checkQuoteClean(t, d)
	// A forward relays: the header block, then the original as it was, in
	// no cite block of its own (the original's own quote stays nested).
	wantPrefix := `<p><br/></p><div>---------- Forwarded message ----------<br/>From: Carol &lt;carol@example.org&gt;<br/>Subject: Pictures</div><p>Hello <b>there</b>`
	if !strings.HasPrefix(d.HTMLBody, wantPrefix) {
		t.Errorf("html:\n got %q\nwant prefix %q", d.HTMLBody, wantPrefix)
	}
	if !strings.HasPrefix(d.TextBody, "---------- Forwarded message ----------\nFrom: Carol <carol@example.org>\nSubject: Pictures\n\nHello there, a picture: [pic]\n") {
		t.Errorf("text = %q", d.TextBody)
	}
	// Every part travels: the referenced picture inline under a new id,
	// the rest (the duplicate, the calendar, the PDF) as attachments, in
	// the order of the original.
	if len(d.Attachments) != 4 || len(res.Skipped) != 0 {
		t.Fatalf("attachments = %+v skipped = %+v", d.Attachments, res.Skipped)
	}
	pic, dup, ics, pdf := d.Attachments[0], d.Attachments[1], d.Attachments[2], d.Attachments[3]
	if !pic.Inline || pic.Filename != "pic.png" || pic.ContentType != "image/png" || !strings.HasSuffix(pic.ContentID, "@malachi.local") {
		t.Errorf("picture = %+v", pic)
	}
	if dup.Inline || dup.Filename != "dup.gif" || dup.ContentType != "image/gif" || dup.ContentID != "" {
		t.Errorf("duplicate = %+v", dup)
	}
	if ics.Inline || ics.Filename != "invite.ics" || ics.ContentType != "text/calendar" {
		t.Errorf("calendar = %+v", ics)
	}
	if pdf.Inline || pdf.Filename != "report.pdf" || pdf.ContentType != "application/pdf" {
		t.Errorf("pdf = %+v", pdf)
	}
	if n := strings.Count(d.HTMLBody, `src="cid:`+pic.ContentID+`"`); n != 3 {
		t.Errorf("the copy is referenced %d times, want 3", n)
	}
	saveRoundTrip(t, m, d)
}

// The created draft is what draft.save stores and what goes out: the
// inline copy stays bound, the message is built with it in a related
// part under its new Content-ID, and attachment.get serves its bytes.
func TestDraftCreateInlineImageRemap(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	id := m.seedQuoted(t, "html-inline-cid.eml")
	d := m.create(t, api.ComposeReply, id, "Carol wrote:").Draft
	pic := d.Attachments[0]

	saved := saveRoundTrip(t, m, d)
	if len(saved.Attachments) != 1 || saved.Attachments[0].ID != pic.ID || saved.Attachments[0].ContentID != pic.ContentID {
		t.Fatalf("bound attachments = %+v", saved.Attachments)
	}
	list, err := m.b.Drafts().List(ctx, api.DraftListParams{AccountID: m.acc})
	if err != nil || list.Page.Total != 1 || len(list.Drafts[0].Attachments) != 1 || list.Drafts[0].HTMLBody != d.HTMLBody {
		t.Fatalf("listed = %+v, %v", list, err)
	}

	got, err := m.b.Attachments().Get(ctx, api.AttachmentGetParams{AccountID: m.acc, AttachmentID: pic.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.AttachmentID != pic.ID || got.ContentType != "image/png" || got.Filename != "pic.png" || !bytes.HasPrefix(got.Data, []byte("\x89PNG")) || got.Size != int64(len(got.Data)) {
		t.Errorf("attachment.get = %+v", got)
	}

	var raw bytes.Buffer
	in := smtp.BuildInput{
		From: api.Address{Address: "me@example.invalid"}, To: d.To, Subject: d.Subject,
		Text: d.TextBody, HTML: d.HTMLBody, Date: time.Now(), MessageID: smtp.NewMessageID("me@example.invalid"),
	}
	path := m.b.store.AttachmentPath(pic.ID)
	in.Attachments = append(in.Attachments, smtp.Attachment{
		Filename: pic.Filename, ContentType: pic.ContentType, Size: pic.Size, Inline: true, ContentID: pic.ContentID,
		Open: func() (io.ReadCloser, error) { return os.Open(path) },
	})
	if err := smtp.BuildMessage(&raw, in); err != nil {
		t.Fatal(err)
	}
	msg := strings.ToLower(raw.String())
	if !strings.Contains(msg, "multipart/alternative") || !strings.Contains(msg, "multipart/related") || !strings.Contains(msg, "content-id: <"+pic.ContentID+">") {
		t.Errorf("built message:\n%s", raw.String())
	}
}

// Whatever the original is, the quote is clean and survives its first
// save unchanged; the same picture referenced three ways is copied once.
func TestDraftCreateHostileOriginal(t *testing.T) {
	for _, name := range []string{"html-scripts-events.eml", "html-cid-foreign.eml", "html-remote-images.eml", "html-inline-cid.eml", "html-obfuscated-urls.eml", "html-forms.eml"} {
		t.Run(name, func(t *testing.T) {
			m := seedMailbox(t)
			id := m.seedQuoted(t, name)
			for _, mode := range []api.ComposeMode{api.ComposeReply, api.ComposeForward} {
				res := m.create(t, mode, id, "X <x@example.org> wrote:")
				if res.Quoted != api.QuoteHTML {
					t.Errorf("%s: quoted = %q", mode, res.Quoted)
				}
				checkQuoteClean(t, res.Draft)
				saveRoundTrip(t, m, res.Draft)
			}
		})
	}
	m := seedMailbox(t)
	d := m.create(t, api.ComposeReply, m.seedQuoted(t, "html-cid-foreign.eml"), "").Draft
	if len(d.Attachments) != 1 || strings.Count(d.HTMLBody, `src="cid:`+d.Attachments[0].ContentID+`"`) != 3 {
		t.Errorf("foreign cids: attachments %+v html %q", d.Attachments, d.HTMLBody)
	}
}

// Without the raw file the text is quoted; with a sanitiser that refuses
// everything the text is quoted plain; a sanitiser that refuses the
// assembled body after the pictures were copied leaves no copy behind.
func TestDraftCreateBodyMissing(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	first := m.msgs[0] // "hello body", HTML, raw with an inline picture
	if err := os.Remove(m.b.store.MessageRawPath(string(m.acc), string(first))); err != nil {
		t.Fatal(err)
	}
	res := m.create(t, api.ComposeReply, first, "Alice wrote:")
	if res.Quoted != api.QuoteText || len(res.Draft.Attachments) != 0 {
		t.Errorf("without raw: quoted %q attachments %+v", res.Quoted, res.Draft.Attachments)
	}
	if res.Draft.HTMLBody != `<p><br/></p><div>Alice wrote:</div><blockquote type="cite">hello body</blockquote>` {
		t.Errorf("without raw: html = %q", res.Draft.HTMLBody)
	}
	if res.Draft.TextBody != "Alice wrote:\n> hello body" {
		t.Errorf("without raw: text = %q", res.Draft.TextBody)
	}
	saveRoundTrip(t, m, res.Draft)

	// The sanitiser refuses everything: plain text, and the forward still
	// carries the attachments of the original.
	id := m.seedQuoted(t, "html-inline-cid.eml")
	m.b.Sanitize = func(sanitize.Input) (sanitize.Output, error) {
		return sanitize.Output{Version: sanitize.Version}, api.NewError(api.CodeSanitizeFailed, "refused")
	}
	res = m.create(t, api.ComposeReply, id, "Carol wrote:")
	if res.Quoted != api.QuoteText || res.Draft.HTMLBody != "" || !strings.HasPrefix(res.Draft.TextBody, "\n\nCarol wrote:\n> Hello there") || len(res.Draft.Attachments) != 0 {
		t.Errorf("refusing sanitiser: %q %+v", res.Quoted, res.Draft)
	}
	if n := countAttachmentFiles(t, m.b); n != 0 {
		t.Errorf("refusing sanitiser left %d copies behind", n)
	}
	fwd := m.create(t, api.ComposeForward, id, "Forwarded:")
	if fwd.Quoted != api.QuoteText || fwd.Draft.HTMLBody != "" || !strings.HasPrefix(fwd.Draft.TextBody, "\n\nForwarded:\nHello there") {
		t.Errorf("refusing sanitiser, forward: %q %+v", fwd.Quoted, fwd.Draft)
	}
	if len(fwd.Draft.Attachments) != 4 || fwd.Draft.Attachments[0].Inline {
		t.Errorf("refusing sanitiser, forward attachments = %+v", fwd.Draft.Attachments)
	}
	for _, a := range fwd.Draft.Attachments {
		if _, err := m.b.Attachments().Remove(ctx, api.AttachmentRemoveParams{AccountID: m.acc, AttachmentID: a.ID}); err != nil {
			t.Fatal(err)
		}
	}

	// The first pass succeeds, the decisive one does not: the copies of
	// the pictures go, the text is quoted in a cite block.
	calls := 0
	m.b.Sanitize = func(in sanitize.Input) (sanitize.Output, error) {
		calls++
		if len(in.RewriteCIDs) > 0 {
			return sanitize.Output{Version: sanitize.Version}, api.NewError(api.CodeSanitizeFailed, "refused")
		}
		return sanitize.Sanitize(in)
	}
	res = m.create(t, api.ComposeReply, id, "Carol wrote:")
	if res.Quoted != api.QuoteText || len(res.Draft.Attachments) != 0 || !strings.Contains(res.Draft.HTMLBody, `<blockquote type="cite">Hello there`) {
		t.Errorf("late refusal: %q %+v", res.Quoted, res.Draft)
	}
	if n := countAttachmentFiles(t, m.b); n != 0 {
		t.Errorf("late refusal left %d copies behind", n)
	}
	if calls < 3 {
		t.Errorf("sanitiser called %d times", calls)
	}
}

func countAttachmentFiles(t *testing.T, b *Backend) int {
	t.Helper()
	entries, err := os.ReadDir(b.store.AttachmentDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return len(entries)
}

// A part over the cap is skipped and reported; the reference to it goes.
func TestDraftCreateOversizedPart(t *testing.T) {
	m := seedMailbox(t)
	id := m.seedQuoted(t, "html-inline-cid.eml")
	prev := quotedPartLimit
	quotedPartLimit = 16
	t.Cleanup(func() { quotedPartLimit = prev })

	res := m.create(t, api.ComposeReply, id, "")
	if res.Quoted != api.QuoteHTML || len(res.Draft.Attachments) != 0 || strings.Contains(res.Draft.HTMLBody, "cid:") {
		t.Errorf("quoted %q attachments %+v html %q", res.Quoted, res.Draft.Attachments, res.Draft.HTMLBody)
	}
	names := []string{}
	for _, s := range res.Skipped {
		names = append(names, s.Filename)
	}
	if strings.Join(names, ",") != "pic.png,invite.ics" {
		t.Errorf("skipped = %v", names)
	}
	if n := countAttachmentFiles(t, m.b); n != 0 {
		t.Errorf("%d files left in the store", n)
	}
	saveRoundTrip(t, m, res.Draft)
}

func TestDraftCreateNew(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	res, err := m.b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeNew})
	if err != nil {
		t.Fatal(err)
	}
	if d := res.Draft; res.Quoted != api.QuoteNone || d.AccountID != m.acc || d.ID != "" || len(d.To) != 0 || len(d.CC) != 0 ||
		len(d.BCC) != 0 || d.Subject != "" || d.TextBody != "" || d.HTMLBody != "" || len(d.Attachments) != 0 {
		t.Errorf("empty template = %+v", res)
	}

	res, err = m.b.Drafts().Create(ctx, api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeNew,
		Mailto: "mailto:Bob%20%3Cbob@example.org%3E?cc=carol@example.org&bcc=dan@example.org&subject=Hi%20there%0Ax&body=line1%0Aline2%20%3Cx%3E%00"})
	if err != nil {
		t.Fatal(err)
	}
	d := res.Draft
	if len(d.To) != 1 || d.To[0] != (api.Address{Name: "Bob", Address: "bob@example.org"}) ||
		len(d.CC) != 1 || d.CC[0].Address != "carol@example.org" || len(d.BCC) != 1 || d.BCC[0].Address != "dan@example.org" {
		t.Errorf("mailto recipients = %v %v %v", d.To, d.CC, d.BCC)
	}
	if d.Subject != "Hi there x" || d.TextBody != "line1\nline2 <x>" || d.HTMLBody != "line1<br/>line2 &lt;x&gt;" {
		t.Errorf("mailto subject %q text %q html %q", d.Subject, d.TextBody, d.HTMLBody)
	}
	saveRoundTrip(t, m, d)

	for name, p := range map[string]api.DraftCreateParams{
		"bad scheme":        {AccountID: m.acc, Mode: api.ComposeNew, Mailto: "https://example.org/"},
		"new with message":  {AccountID: m.acc, Mode: api.ComposeNew, MessageID: m.msgs[0]},
		"reply with mailto": {AccountID: m.acc, Mode: api.ComposeReply, MessageID: m.msgs[0], Mailto: "mailto:x@example.org"},
	} {
		if _, err := m.b.Drafts().Create(ctx, p); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDraftCreateErrors(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	id := m.msgs[0]
	cases := map[string]struct {
		p    api.DraftCreateParams
		code api.ErrorCode
	}{
		"no account":      {api.DraftCreateParams{Mode: api.ComposeReply, MessageID: id}, api.CodeInvalidArgument},
		"unknown account": {api.DraftCreateParams{AccountID: "acc_nope", Mode: api.ComposeReply, MessageID: id}, api.CodeAccountNotFound},
		"unknown message": {api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeReply, MessageID: "m_nope"}, api.CodeMessageNotFound},
		"no message":      {api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeForward}, api.CodeInvalidArgument},
		"bad mode":        {api.DraftCreateParams{AccountID: m.acc, Mode: "quote", MessageID: id}, api.CodeInvalidArgument},
		"attribution NUL": {api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeReply, MessageID: id, Attribution: "a\x00b"}, api.CodeInvalidArgument},
		"attribution CR":  {api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeReply, MessageID: id, Attribution: "a\rb"}, api.CodeInvalidArgument},
		"attribution ESC": {api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeReply, MessageID: id, Attribution: "a\x1bb"}, api.CodeInvalidArgument},
		"attribution utf": {api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeReply, MessageID: id, Attribution: "\xff"}, api.CodeInvalidArgument},
		"attribution big": {api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeReply, MessageID: id, Attribution: strings.Repeat("a", api.MaxDraftAttributionBytes+1)}, api.CodeInvalidArgument},
		"attribution tall": {api.DraftCreateParams{AccountID: m.acc, Mode: api.ComposeReply, MessageID: id,
			Attribution: strings.Repeat("a\n", api.MaxDraftAttributionLines) + "a"}, api.CodeInvalidArgument},
	}
	for name, c := range cases {
		if _, err := m.b.Drafts().Create(ctx, c.p); errCode(t, err) != c.code {
			t.Errorf("%s: %v", name, err)
		}
	}
	// CRLF is accepted and becomes a line break; a trailing break is
	// dropped; the lines cap counts lines, not breaks.
	res := m.create(t, api.ComposeReply, id, "On Monday,\r\nAlice wrote:\n")
	if !strings.Contains(res.Draft.HTMLBody, "<div>On Monday,<br/>Alice wrote:</div>") {
		t.Errorf("html = %q", res.Draft.HTMLBody)
	}
	if !strings.HasPrefix(res.Draft.TextBody, "On Monday,\nAlice wrote:\n> hello body link <https://example.invalid/x>\n>\n> [logo]") {
		t.Errorf("text = %q", res.Draft.TextBody)
	}
	m.create(t, api.ComposeReply, id, strings.TrimSuffix(strings.Repeat("a\n", api.MaxDraftAttributionLines), "\n"))
}
