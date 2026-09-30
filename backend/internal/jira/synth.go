// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"mime"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/jira/botclean"
	imime "github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/safename"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Synthesis. Every item of an issue — its description, each comment, each
// changelog entry with status or assignee changes — becomes one RFC 5322
// message, and it is built the same way whenever it is built: by the
// syncer, and again by FetchMessage for message.download. The output is a
// function of what the site says and of the account's rendering settings
// only (no clock, no random boundary), so a rebuild is byte for byte the
// message stored as long as the site says the same, and the part numbers
// of its pictures and files never move.
//
// Addresses and ids live under reserved names (RFC 2606 .invalid):
//
//	From        <user id>@users.jira.invalid, or u-<hash>@… for an id that is
//	            no plain local part (cloud ids hold ':'), n-<hash of the
//	            name>@… for a comment a bot relayed on someone's behalf
//	Message-ID  issue.<id>@<site host>.malachi.invalid, comment.<cid>.issue.<id>@…,
//	            history.<hid>.issue.<id>@…; comments and events answer the
//	            description (In-Reply-To, References)
//	Subject     "KEY: Summary" on every message of the issue
//
// The site's HTML stays the site's HTML (hostile until internal/sanitize
// has seen it at display); synthesis only makes its relative links
// absolute and embeds the site's own pictures (images.go). Event messages
// are text/plain with one language-neutral line per change,
// "<from> → <to>" with "—" for a side that is empty: clients build the
// sentence from MessageIssue.Changes.

// synthVersion identifies the rules of this file and images.go; it is part
// of every message's revision and of the account's render key, so raising
// it rebuilds every stored item. 2: histories fetched in bulk carry their
// time as epoch milliseconds, which "1" read as no time at all.
const synthVersion = "2"

const (
	// userDomain is the domain of every synthesised sender.
	userDomain = "users.jira.invalid"
	// msgIDDomainSuffix follows the site's host in every Message-ID.
	msgIDDomainSuffix = ".malachi.invalid"
	// revisionHeader carries a hash of what a message was built from (and
	// synthVersion): the syncer compares it to decide whether a stored
	// description must be built again.
	revisionHeader = "X-Malachi-Revision"
	// maxTextBytes caps the text/plain rendering of one item.
	maxTextBytes = 4 << 20
	// maxPartBytes caps one picture or file; maxPartsBytes all of them in
	// one message (raw, before base64), which keeps a message under
	// ingest.MaxMessageBytes; maxPictures caps the pictures embedded.
	maxPartBytes  = 16 << 20
	maxPartsBytes = 16 << 20
	maxPictures   = 32
	// mimeReserve is what the headers and the structure of a message may
	// take, set aside before the parts get their budget.
	mimeReserve = 1 << 20
	// editTolerance is how much later than its creation a comment may be
	// updated without counting as edited (the service desk touches new
	// comments).
	editTolerance = time.Minute
)

// synth builds the messages of one account's issues.
type synth struct {
	site   *url.URL // the account's SiteURL
	base   *url.URL // the site with a trailing slash: relative links resolve against it
	host   string   // the site's host, as a Message-ID domain label
	rules  *botclean.Compiled
	remote Remote
	client *Client // routePath: which pictures lie within the site
	me     User
	log    *slog.Logger
	// partsCap and picturesCap lower maxPartsBytes and maxPictures
	// (tests; 0 = those).
	partsCap    int64
	picturesCap int
}

// newSynth prepares the synthesis for an account. rules may be nil (no
// bot rules).
func newSynth(cfg api.JiraConfig, rules *botclean.Compiled, remote Remote, client *Client, me User, log *slog.Logger) (*synth, error) {
	site, err := NormaliseSiteURL(cfg.SiteURL)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(site)
	if err != nil {
		return nil, api.NewError(api.CodeInvalidArgument, "jira: site URL does not parse")
	}
	b := *u
	b.Path = strings.TrimSuffix(b.Path, "/") + "/"
	b.RawPath = ""
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &synth{site: u, base: &b, host: msgIDHost(u.Hostname()), rules: rules, remote: remote, client: client, me: me, log: log}, nil
}

// compileRules compiles the account's comment rules; unusable entries are
// logged and skipped (validation refuses them at configuration time).
func compileRules(cfg api.JiraConfig, log *slog.Logger) *botclean.Compiled {
	c, errs := botclean.Compile(botclean.Rules{BotNames: cfg.BotNames, MetadataFilters: cfg.MetadataFilters, AuthorPrefixes: cfg.AuthorPrefixes})
	for _, err := range errs {
		log.Warn("jira comment rule skipped", "err", err)
	}
	return c
}

// renderKey identifies the settings the stored messages of an issue were
// built with: the comment rules, whether events are shown, the synthesis
// rules. Another key rebuilds the issue.
func renderKey(rules *botclean.Compiled, hideEvents bool) string {
	h := sha256.New()
	writeField(h, "jira-render")
	writeField(h, synthVersion)
	writeField(h, rules.Key())
	writeField(h, fmt.Sprint(hideEvents))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// msgIDHost turns a host name into a Message-ID domain label: lower case,
// only letters, digits, '.' and '-' ("site" when nothing is left; an IPv6
// literal loses its colons).
func msgIDHost(h string) string {
	h = strings.ToLower(strings.Trim(h, "[]."))
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), ".-")
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	if s == "" {
		return "site"
	}
	return s
}

// item is one message of an issue as the site describes it now, ready to
// be compared with what is stored and to be built.
type item struct {
	remoteID string // "i:<issue>", "c:<comment>", "h:<history>"
	kind     api.IssueItemKind
	issueID  string
	// authorID is the site's author (the bot's, for a relayed comment);
	// mine says the item is the user's own.
	authorID string
	mine     bool
	from     api.Address
	// date is the message's date (a relayed comment's header date);
	// created the time the site created the item, which decides whether
	// it is news; updated the site's last change (a comment's edit).
	date    time.Time
	created time.Time
	updated time.Time
	html    string // the site's HTML, bot-cleaned; "" for an event
	text    string // an event's text
	// files are the issue's attachments this message carries, sorted by
	// id; links are those of them that only get a link when the budget
	// runs out (the description's unreferenced ones).
	files      []Attachment
	visibility api.CommentVisibility
	via        string
	edited     bool
	changes    []api.IssueChange
	msgID      string
	inReplyTo  string
	revision   string
}

// storeItem is what issue_items keeps of it.
func (it *item) storeItem() store.IssueItem {
	return store.IssueItem{
		RemoteID: it.remoteID, IssueID: it.issueID, Kind: it.kind, Visibility: it.visibility,
		AuthorID: it.authorID, Via: it.via, Changes: it.changes, Edited: it.edited, Updated: it.updated,
	}
}

func (y *synth) issueMsgID(issueID string) string {
	return "issue." + issueID + "@" + y.host + msgIDDomainSuffix
}

func (y *synth) commentMsgID(issueID, commentID string) string {
	return "comment." + commentID + ".issue." + issueID + "@" + y.host + msgIDDomainSuffix
}

func (y *synth) historyMsgID(issueID, historyID string) string {
	return "history." + historyID + ".issue." + issueID + "@" + y.host + msgIDDomainSuffix
}

// subject is the subject of every message of the issue.
func subject(is Issue) string {
	if is.Summary == "" {
		return is.Key
	}
	return is.Key + ": " + is.Summary
}

// items describes every message of the issue: the description, the
// comments (oldest first, through the bot rules) and, unless hideEvents,
// the status and assignee changes. serviceDesk marks the comments of a
// service-desk space public unless the site says internal.
func (y *synth) items(is Issue, comments []Comment, hist []History, serviceDesk, hideEvents bool) []*item {
	refs := map[string]map[string]bool{} // remote id → attachment ids its HTML references
	var out []*item

	desc := &item{
		remoteID: "i:" + is.ID, kind: api.IssueItemDescription, issueID: is.ID,
		authorID: is.Reporter.ID, mine: y.isMe(is.Reporter.ID),
		from: userAddress(is.Reporter.ID, is.Reporter.Name),
		date: is.Created, created: is.Created, updated: is.Updated,
		html: is.DescriptionHTML, msgID: y.issueMsgID(is.ID),
	}
	refs[desc.remoteID] = y.attachmentRefs(desc.html)
	out = append(out, desc)

	for _, c := range comments {
		it := &item{
			remoteID: "c:" + c.ID, kind: api.IssueItemComment, issueID: is.ID,
			authorID: c.Author.ID, mine: y.isMe(c.Author.ID),
			from: userAddress(c.Author.ID, c.Author.Name),
			date: c.Created, created: c.Created, updated: c.Updated,
			visibility: c.Visibility,
			edited:     !c.Created.IsZero() && c.Updated.Sub(c.Created) > editTolerance,
			msgID:      y.commentMsgID(is.ID, c.ID), inReplyTo: desc.msgID,
		}
		if it.updated.IsZero() {
			it.updated = c.Created
		}
		if serviceDesk && it.visibility == "" {
			it.visibility = api.CommentPublic
		}
		res := y.rules.Clean(c.Author.Name, c.BodyHTML, c.Created)
		it.html = res.HTML
		if res.AuthorName != "" {
			// Relayed by a bot on someone's behalf: that person is the
			// sender, the bot is "via".
			it.from = userAddress("", res.AuthorName)
			it.mine, it.via = false, res.Via
			if !res.Created.IsZero() {
				it.date = res.Created.UTC()
			}
		}
		refs[it.remoteID] = y.attachmentRefs(it.html)
		out = append(out, it)
	}
	if !hideEvents {
		out = append(out, y.eventItems(is, hist, desc.msgID)...)
	}

	// Attachments: a file a message's HTML references is that message's;
	// one no message references goes with the description.
	referenced := map[string]bool{}
	for _, r := range refs {
		for id := range r {
			referenced[id] = true
		}
	}
	atts := append([]Attachment(nil), is.Attachments...)
	sort.SliceStable(atts, func(i, j int) bool { return idLess(atts[i].ID, atts[j].ID) })
	for _, it := range out {
		for _, a := range atts {
			if refs[it.remoteID][a.ID] || (it == desc && !referenced[a.ID]) {
				it.files = append(it.files, a)
			}
		}
		it.revision = y.revision(it)
	}
	return out
}

func (y *synth) isMe(id string) bool { return id != "" && y.me.ID != "" && id == y.me.ID }

// localPattern is a user id that can stand as the local part of an
// address as it is.
var localPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// userAddress is the synthesised address of a site user: the id as the
// local part when it can be one, else a hash of it; without an id, a hash
// of the name (a relayed comment's author); without either, "anonymous".
// The display name is the site's.
func userAddress(id, name string) api.Address {
	var local string
	switch {
	case id != "" && localPattern.MatchString(id) && !strings.Contains(id, "..") && !strings.HasSuffix(id, "."):
		local = id
	case id != "":
		local = "u-" + hash24(id)
	case strings.TrimSpace(name) != "":
		local = "n-" + hash24(strings.ToLower(strings.Join(strings.Fields(name), " ")))
	default:
		local = "anonymous"
	}
	return api.Address{Name: name, Address: local + "@" + userDomain}
}

func hash24(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:24]
}

// writeField writes s length-prefixed, so that no two field lists hash
// alike.
func writeField(h hash.Hash, s string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	_, _ = h.Write(n[:]) // a hash never fails
	_, _ = io.WriteString(h, s)
}

// revision is the hash of everything the item's message is built from,
// except its subject (a renamed issue retitles its rows without a
// rebuild) and the bytes of its pictures and files.
func (y *synth) revision(it *item) string {
	h := sha256.New()
	for _, s := range []string{synthVersion, string(it.kind), it.remoteID, it.from.Name, it.from.Address,
		it.date.UTC().Format(time.RFC3339Nano), it.html, it.text, it.msgID, it.inReplyTo, y.site.String()} {
		writeField(h, s)
	}
	for _, a := range it.files {
		writeField(h, a.ID)
		writeField(h, a.Filename)
		writeField(h, fmt.Sprint(a.Size))
		writeField(h, a.ContentURL)
	}
	return synthVersion + "." + hex.EncodeToString(h.Sum(nil))[:32]
}

// part is a picture or a file of a message.
type part struct {
	filename    string
	contentType string
	cid         string // pictures only
	data        []byte
}

// mimeInput is everything writeMIME needs.
type mimeInput struct {
	from      api.Address
	subject   string
	date      time.Time
	msgID     string
	inReplyTo string
	revision  string
	html      string // "" = a text/plain message
	text      string
	pictures  []part
	files     []part
}

// build downloads what the item's message embeds and writes the message.
// A picture or file the site will not hand over (gone, refused, too big,
// not what it claims) is left out, a picture as the link it was, a file
// the description carries as a link to the site; a failure of the site or
// the network as a whole is returned.
func (y *synth) build(ctx context.Context, is Issue, it *item) ([]byte, error) {
	in := mimeInput{
		from: it.from, subject: subject(is), date: it.date, msgID: it.msgID, inReplyTo: it.inReplyTo,
		revision: it.revision, text: it.text,
	}
	if it.kind != api.IssueItemEvent {
		html := y.absolutise(it.html)
		budget := partsBudget(html)
		if y.partsCap > 0 {
			budget = min(budget, y.partsCap)
		}
		pictures, html, spent, err := y.embedPictures(ctx, it.msgID, html, is.Attachments, budget)
		if err != nil {
			return nil, err
		}
		budget -= spent
		var links []Attachment
		for _, a := range it.files {
			p, ok, err := y.fetchFile(ctx, a, budget)
			if err != nil {
				return nil, err
			}
			if !ok {
				links = append(links, a)
				continue
			}
			budget -= int64(len(p.data))
			in.files = append(in.files, p)
		}
		if it.kind == api.IssueItemDescription {
			// A file referenced by a comment has its link in that comment;
			// the description's own files that did not fit get one here.
			refs := y.attachmentRefs(html)
			var own []Attachment
			for _, a := range links {
				if !refs[a.ID] {
					own = append(own, a)
				}
			}
			html += y.fileLinks(own)
		}
		in.pictures, in.html = pictures, html
		in.text = imime.HTMLToText(html, maxTextBytes)
	}
	var buf strings.Builder
	if err := writeMIME(&buf, in); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// partsBudget is what the pictures and files of a message with this HTML
// may take in all: maxPartsBytes, less when the text parts (quoted-
// printable, at worst three bytes a byte, the HTML and its text) and the
// base64 of the parts would not fit in ingest.MaxMessageBytes.
func partsBudget(html string) int64 {
	room := int64(ingest.MaxMessageBytes) - mimeReserve - 6*int64(len(html))
	// base64 takes 4 bytes per 3, plus a line break per 76.
	room = room * 3 / 4 * 76 / 78
	return max(0, min(room, maxPartsBytes))
}

// fileLinks is the HTML of links to files that are not in the message: one
// paragraph, a line per file, the file name as the text (nothing of ours:
// the backend has no words of its own).
func (y *synth) fileLinks(files []Attachment) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<p>")
	for i, a := range files {
		if i > 0 {
			b.WriteString("<br>")
		}
		name := a.Filename
		if name == "" {
			name = a.ID
		}
		fmt.Fprintf(&b, `<a href="%s">%s</a>`, escapeAttr(y.attachmentURL(a)), escapeText(name))
	}
	b.WriteString("</p>")
	return b.String()
}

// attachmentURL is the link a browser opens an attachment with: the
// site's own, whatever route the syncer's requests take.
func (y *synth) attachmentURL(a Attachment) string {
	name := a.Filename
	if name == "" {
		name = a.ID
	}
	return strings.TrimSuffix(y.site.String(), "/") + "/secure/attachment/" + url.PathEscape(a.ID) + "/" + url.PathEscape(name)
}

var (
	attrEscaper = strings.NewReplacer(`&`, "&amp;", `"`, "&quot;", `'`, "&#39;", `<`, "&lt;", `>`, "&gt;")
	textEscaper = strings.NewReplacer(`&`, "&amp;", `<`, "&lt;", `>`, "&gt;")
)

func escapeAttr(s string) string { return attrEscaper.Replace(s) }
func escapeText(s string) string { return textEscaper.Replace(s) }

// writeMIME writes the message: text/plain alone, or
// multipart/alternative with the HTML (a multipart/related around the HTML
// and its pictures), in a multipart/mixed with the files; the shape of
// internal/smtp's messages, so their part numbers are what smtp.PartIDs
// says. The boundaries derive from the Message-ID ("=_" never occurs in
// quoted-printable or base64), so the same input gives the same bytes.
func writeMIME(w io.Writer, in mimeInput) error {
	var h mail.Header
	h.SetAddressList("From", []*mail.Address{{Name: in.from.Name, Address: in.from.Address}})
	h.SetSubject(in.subject)
	h.SetDate(in.date.UTC())
	h.SetMessageID(in.msgID)
	if in.inReplyTo != "" {
		h.SetMsgIDList("In-Reply-To", []string{in.inReplyTo})
		h.SetMsgIDList("References", []string{in.inReplyTo})
	}
	h.Set(revisionHeader, in.revision)

	boundary := func(level string) string {
		sum := sha256.Sum256([]byte(in.msgID + "\x00" + level))
		return "=_malachi_" + level + "_" + hex.EncodeToString(sum[:12])
	}
	root := func(ph message.Header) (*message.Writer, error) {
		h.Set("Content-Type", ph.Get("Content-Type"))
		if cte := ph.Get("Content-Transfer-Encoding"); cte != "" {
			h.Set("Content-Transfer-Encoding", cte)
		}
		return message.CreateWriter(w, h.Header)
	}
	text, html := normaliseNewlines(in.text), normaliseNewlines(in.html)
	if len(in.files) == 0 {
		return writeContent(root, boundary, text, html, in.pictures)
	}
	var mh message.Header
	mh.SetContentType("multipart/mixed", map[string]string{"boundary": boundary("mixed")})
	mw, err := root(mh)
	if err != nil {
		return err
	}
	if err := writeContent(mw.CreatePart, boundary, text, html, in.pictures); err != nil {
		return err
	}
	for _, p := range in.files {
		if err := writePart(mw, p, "attachment"); err != nil {
			return err
		}
	}
	return mw.Close()
}

type createFunc func(message.Header) (*message.Writer, error)

func writeContent(create createFunc, boundary func(string) string, text, html string, pictures []part) error {
	if html == "" {
		return writeText(create, "text/plain", text)
	}
	var ah message.Header
	ah.SetContentType("multipart/alternative", map[string]string{"boundary": boundary("alt")})
	aw, err := create(ah)
	if err != nil {
		return err
	}
	if err := writeText(aw.CreatePart, "text/plain", text); err != nil {
		return err
	}
	if len(pictures) == 0 {
		if err := writeText(aw.CreatePart, "text/html", html); err != nil {
			return err
		}
		return aw.Close()
	}
	var rh message.Header
	rh.SetContentType("multipart/related", map[string]string{"type": "text/html", "boundary": boundary("rel")})
	rw, err := aw.CreatePart(rh)
	if err != nil {
		return err
	}
	if err := writeText(rw.CreatePart, "text/html", html); err != nil {
		return err
	}
	for _, p := range pictures {
		if err := writePart(rw, p, "inline"); err != nil {
			return err
		}
	}
	if err := rw.Close(); err != nil {
		return err
	}
	return aw.Close()
}

func writeText(create createFunc, ctype, body string) error {
	var ph message.Header
	ph.SetContentType(ctype, map[string]string{"charset": "utf-8"})
	ph.Set("Content-Transfer-Encoding", "quoted-printable")
	pw, err := create(ph)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(pw, body); err != nil {
		return err
	}
	return pw.Close()
}

func writePart(mw *message.Writer, p part, disposition string) error {
	var ah message.Header
	ah.Set("Content-Type", p.contentType)
	ah.Set("Content-Transfer-Encoding", "base64")
	disp := mime.FormatMediaType(disposition, map[string]string{"filename": safename.Filename(p.filename)})
	if disp == "" {
		disp = mime.FormatMediaType(disposition, map[string]string{"filename": safename.Fallback})
	}
	ah.Set("Content-Disposition", disp)
	if p.cid != "" {
		ah.Set("Content-ID", "<"+p.cid+">")
	}
	pw, err := mw.CreatePart(ah)
	if err != nil {
		return err
	}
	if _, err := pw.Write(p.data); err != nil {
		return err
	}
	return pw.Close()
}

// normaliseNewlines turns CRLF and bare CR into LF (the quoted-printable
// writer then writes CRLF).
func normaliseNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}
