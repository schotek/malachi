// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"

	"github.com/schotek/malachi/backend/internal/bulk"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/oneclick"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// unsubEnv is a backend with a mail account, an inbox and a junk folder, a
// DNS that serves one DKIM key and a one-click client that records its
// requests.
type unsubEnv struct {
	t      *testing.T
	b      *Backend
	out    *fakeOutbox
	acc    api.AccountID
	folder map[string]store.Folder
	priv   ed25519.PrivateKey

	mu       sync.Mutex
	posts    []string
	lookups  []string
	postErr  error
	nextUID  uint32
	txtExtra map[string][]string
}

func newUnsubEnv(t *testing.T) *unsubEnv {
	t.Helper()
	b, _ := newSyncBackend(t)
	e := &unsubEnv{t: t, b: b, out: outboxOf(t, b), nextUID: 1}
	e.acc = api.AccountID(seedAccount(t, b, "me@example.invalid"))
	e.folder = seedFolders(t, b, string(e.acc), []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Junk", Name: "Junk", Path: "Junk", Role: api.RoleJunk, Subscribed: true, Selectable: true},
	})
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	e.priv = priv
	key := "v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(pub)
	b.LookupTXT = func(_ context.Context, domain string) ([]string, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.lookups = append(e.lookups, domain)
		if domain == "s1._domainkey.news.example" {
			return []string{key}, nil
		}
		return nil, errors.New("no such record")
	}
	b.OneClickPost = func(_ context.Context, u string) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.posts = append(e.posts, u)
		return e.postErr
	}
	return e
}

func (e *unsubEnv) postedURLs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.posts...)
}

// raw is a message from news@news.example with the given extra header
// lines.
func raw(extra ...string) string {
	h := []string{
		"From: News <news@news.example>", "To: me@example.invalid", "Subject: Weekly news",
		"Date: Mon, 01 Sep 2026 11:00:00 +0000", "Message-ID: <w1@news.example>",
	}
	h = append(h, extra...)
	h = append(h, "MIME-Version: 1.0", "Content-Type: text/plain; charset=utf-8")
	return strings.Join(h, "\r\n") + "\r\n\r\nHello.\r\n"
}

const (
	hdrOneClickURL  = "List-Unsubscribe: <https://news.example/u/abc>, <mailto:unsub@news.example>"
	hdrOneClickPost = "List-Unsubscribe-Post: List-Unsubscribe=One-Click"
)

// sign adds a DKIM signature of domain covering keys.
func (e *unsubEnv) sign(message, domain string, keys ...string) string {
	e.t.Helper()
	if len(keys) == 0 {
		keys = []string{"From", "To", "Subject", "Date", "Message-ID", "List-Unsubscribe", "List-Unsubscribe-Post"}
	}
	var out bytes.Buffer
	err := dkim.Sign(&out, strings.NewReader(message), &dkim.SignOptions{
		Domain: domain, Selector: "s1", Signer: e.priv, HeaderKeys: keys,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed, BodyCanonicalization: dkim.CanonicalizationRelaxed,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out.String()
}

// store puts a downloaded message in folder (INBOX, Junk) of the account.
func (e *unsubEnv) store(folder, message string, flags ...api.Flag) api.MessageID {
	e.t.Helper()
	ctx := context.Background()
	parsed, err := mime.Parse(strings.NewReader(message), mime.DefaultLimits())
	if err != nil {
		e.t.Fatal(err)
	}
	m := &store.Message{AccountID: string(e.acc), FolderID: e.folder[folder].ID, UID: e.nextUID, Flags: flags,
		Subject: parsed.Subject, From: parsed.From, Date: time.Now(), RFCMessageID: parsed.MessageID, Size: int64(len(message))}
	e.nextUID++
	if err := e.b.store.UpsertMessages(ctx, []*store.Message{m}); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.b.store.WriteMessageRaw(ctx, string(e.acc), m.ID, strings.NewReader(message), 1<<20); err != nil {
		e.t.Fatal(err)
	}
	if err := e.b.store.SetMessageBody(ctx, m.ID, store.BodyUpdate{Text: parsed.Text, Headers: parsed.Headers, State: store.BodyFetched, Size: int64(len(message))}); err != nil {
		e.t.Fatal(err)
	}
	return api.MessageID(m.ID)
}

func (e *unsubEnv) get(id api.MessageID) api.Message {
	e.t.Helper()
	res, err := e.b.Messages().Get(context.Background(), api.MessageGetParams{AccountID: e.acc, MessageID: id})
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Message
}

func (e *unsubEnv) unsubscribe(id api.MessageID) (*api.MessageUnsubscribeResult, error) {
	return e.b.Messages().Unsubscribe(context.Background(), api.MessageUnsubscribeParams{AccountID: e.acc, MessageID: id})
}

func TestUnsubscribeOfferInGet(t *testing.T) {
	e := newUnsubEnv(t)
	cases := []struct {
		name   string
		raw    string
		method api.UnsubscribeMethod
		target string
		url    string
	}{
		{"one-click", raw(hdrOneClickURL, hdrOneClickPost), api.UnsubscribeOneClick, "news.example", ""},
		{"mailto without post header", raw(hdrOneClickURL), api.UnsubscribeMailto, "unsub@news.example", ""},
		{"page only", raw("List-Unsubscribe: <https://news.example/leave?id=1>"), api.UnsubscribeURL, "news.example", "https://news.example/leave?id=1"},
		{"list prefers mailto", raw("List-Id: Go <go.news.example>", "List-Post: <mailto:go@news.example>", hdrOneClickURL, hdrOneClickPost), api.UnsubscribeMailto, "unsub@news.example", ""},
	}
	for _, c := range cases {
		m := e.get(e.store("INBOX", c.raw))
		if m.Unsubscribe == nil || m.Unsubscribe.Method != c.method || m.Unsubscribe.Target != c.target || m.Unsubscribe.URL != c.url || m.Unsubscribe.UnsubscribedAt != nil {
			t.Errorf("%s: offer %+v", c.name, m.Unsubscribe)
		}
		if m.Bulk == nil {
			t.Errorf("%s: no bulk info", c.name)
		}
	}
	if m := e.get(e.store("INBOX", raw())); m.Unsubscribe != nil || m.Bulk != nil {
		t.Errorf("personal mail: %+v %+v", m.Unsubscribe, m.Bulk)
	}
	if m := e.get(e.store("INBOX", raw("Auto-Submitted: auto-generated"))); m.Unsubscribe != nil || m.Bulk == nil || m.Bulk.Kind != api.BulkAutomated || m.Bulk.Domain != "news.example" {
		t.Errorf("automated: %+v %+v", m.Unsubscribe, m.Bulk)
	}
	// No offer in the junk folder or for a message flagged junk.
	if m := e.get(e.store("Junk", raw(hdrOneClickURL, hdrOneClickPost))); m.Unsubscribe != nil || m.Bulk == nil {
		t.Errorf("junk folder: %+v %+v", m.Unsubscribe, m.Bulk)
	}
	if m := e.get(e.store("INBOX", raw(hdrOneClickURL, hdrOneClickPost), api.FlagJunk)); m.Unsubscribe != nil {
		t.Errorf("flagged junk: %+v", m.Unsubscribe)
	}
}

func TestBulkInListings(t *testing.T) {
	e := newUnsubEnv(t)
	e.store("INBOX", raw("List-Id: <go.news.example>", "List-Post: <mailto:go@news.example>"))
	e.store("INBOX", raw())
	res, err := e.b.Messages().List(context.Background(), api.MessageListParams{AccountID: e.acc, FolderID: api.FolderID(e.folder["INBOX"].ID)})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, m := range res.Messages {
		if m.Bulk == nil {
			kinds = append(kinds, "-")
		} else {
			kinds = append(kinds, fmt.Sprintf("%s/%s/%s", m.Bulk.Kind, m.Bulk.ListID, m.Bulk.Domain))
		}
	}
	if fmt.Sprint(kinds) != "[- list/go.news.example/news.example]" && fmt.Sprint(kinds) != "[list/go.news.example/news.example -]" {
		t.Errorf("kinds = %v", kinds)
	}
}

func TestUnsubscribeOneClickVerified(t *testing.T) {
	e := newUnsubEnv(t)
	id := e.store("INBOX", e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example"))
	res, err := e.unsubscribe(id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != api.UnsubscribeDone || res.UnsubscribedAt == nil || res.URL != "" || res.Unverified {
		t.Fatalf("result %+v", res)
	}
	if got := e.postedURLs(); len(got) != 1 || got[0] != "https://news.example/u/abc" {
		t.Fatalf("posts %v", got)
	}
	// Remembered: the offer says so, for every message of the sender.
	if m := e.get(id); m.Unsubscribe == nil || m.Unsubscribe.UnsubscribedAt == nil || !m.Unsubscribe.UnsubscribedAt.Equal(*res.UnsubscribedAt) {
		t.Errorf("offer after: %+v", m.Unsubscribe)
	}
	other := e.get(e.store("INBOX", raw(hdrOneClickURL, hdrOneClickPost)))
	if other.Unsubscribe == nil || other.Unsubscribe.UnsubscribedAt == nil {
		t.Errorf("another message of the sender: %+v", other.Unsubscribe)
	}
	if got := e.outboxCount(); got != 0 {
		t.Errorf("outbox has %d messages", got)
	}
}

func (e *unsubEnv) outboxCount() int {
	e.t.Helper()
	res, err := e.b.Sync().Status(context.Background(), api.SyncStatusParams{AccountID: e.acc})
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Accounts[0].PendingOutbox
}

// Whatever lacks a valid, aligned signature that covers both fields is
// not sent: the page is handed back as unverified.
func TestUnsubscribeOneClickUnverified(t *testing.T) {
	e := newUnsubEnv(t)
	good := e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example")
	prepend := func(s, line string) string { return line + "\r\n" + s }
	cases := map[string]string{
		"not signed":                  raw(hdrOneClickURL, hdrOneClickPost),
		"signature of another domain": e.sign(raw(hdrOneClickURL, hdrOneClickPost), "attacker.example"),
		"does not cover the post":     e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example", "From", "To", "Subject", "Date", "Message-ID", "List-Unsubscribe"),
		"does not cover the url":      e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example", "From", "To", "Subject", "Date", "Message-ID", "List-Unsubscribe-Post"),
		"body changed":                strings.Replace(good, "Hello.", "Hello!", 1),
		"url header changed":          strings.Replace(good, "https://news.example/u/abc", "https://evil.example/u/abc", 1),
		"header added in front":       prepend(good, "List-Unsubscribe: <https://evil.example/x>"),
		"from added in front":         prepend(good, "From: Boss <boss@victim.example>"),
		"post added in front":         prepend(good, "List-Unsubscribe-Post: List-Unsubscribe=One-Click"),
		"garbage signature":           "DKIM-Signature: v=1; a=rsa-sha256; d=news.example; s=s1; h=from; bh=AAAA; b=AAAA\r\n" + raw(hdrOneClickURL, hdrOneClickPost),
	}
	for name, message := range cases {
		id := e.store("INBOX", message)
		res, err := e.unsubscribe(id)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// The page handed back is whatever the message says: it opens only
		// after the user sees it and is never fetched.
		if res.Outcome != api.UnsubscribeOpenURL || !res.Unverified || !strings.HasPrefix(res.URL, "https://") || res.UnsubscribedAt != nil {
			t.Errorf("%s: result %+v", name, res)
		}
	}
	if got := e.postedURLs(); len(got) != 0 {
		t.Fatalf("posted: %v", got)
	}
	if m := e.get(e.store("INBOX", raw(hdrOneClickURL, hdrOneClickPost))); m.Unsubscribe.UnsubscribedAt != nil {
		t.Error("remembered after a fallback")
	}
}

// A subdomain signing for the organisation, and the organisation for a
// subdomain, are aligned.
func TestUnsubscribeDKIMAlignment(t *testing.T) {
	e := newUnsubEnv(t)
	// The key lives under news.example; sign as news.example for a From
	// of a subdomain of it.
	message := strings.Replace(raw(hdrOneClickURL, hdrOneClickPost), "news@news.example", "news@mail.news.example", 1)
	id := e.store("INBOX", e.sign(message, "news.example"))
	res, err := e.unsubscribe(id)
	if err != nil || res.Outcome != api.UnsubscribeDone {
		t.Fatalf("%+v %v", res, err)
	}
	// A From in another organisation is not.
	other := strings.Replace(raw(hdrOneClickURL, hdrOneClickPost), "news@news.example", "news@other.example", 1)
	res, err = e.unsubscribe(e.store("INBOX", e.sign(other, "news.example")))
	if err != nil || res.Outcome != api.UnsubscribeOpenURL || !res.Unverified {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestUnsubscribeDKIMLookupFailures(t *testing.T) {
	e := newUnsubEnv(t)
	signed := e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example")
	for name, lookup := range map[string]func(context.Context, string) ([]string, error){
		"dns error":     func(context.Context, string) ([]string, error) { return nil, errors.New("servfail") },
		"no record":     func(context.Context, string) ([]string, error) { return nil, nil },
		"junk record":   func(context.Context, string) ([]string, error) { return []string{"v=DKIM1; k=rsa; p=%%%"}, nil },
		"revoked key":   func(context.Context, string) ([]string, error) { return []string{"v=DKIM1; k=ed25519; p="}, nil },
		"slow resolver": func(ctx context.Context, _ string) ([]string, error) { <-ctx.Done(); return nil, ctx.Err() },
	} {
		if name == "slow resolver" {
			if testing.Short() {
				continue
			}
		}
		e.b.LookupTXT = lookup
		id := e.store("INBOX", signed)
		ctx := context.Background()
		if name == "slow resolver" {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
		}
		res, err := e.b.Messages().Unsubscribe(ctx, api.MessageUnsubscribeParams{AccountID: e.acc, MessageID: id})
		if err != nil || res.Outcome != api.UnsubscribeOpenURL || !res.Unverified {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	if len(e.postedURLs()) != 0 {
		t.Error("posted")
	}
}

func TestUnsubscribeAtMostFiveSignaturesAreChecked(t *testing.T) {
	e := newUnsubEnv(t)
	// The valid signature is the lowest, so the sixth in header order: it
	// is not looked at.
	message := e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example")
	for i := 0; i < 5; i++ {
		message = e.sign(message, "other"+fmt.Sprint(i)+".example")
	}
	res, err := e.unsubscribe(e.store("INBOX", message))
	if err != nil || !res.Unverified {
		t.Fatalf("%+v %v", res, err)
	}
	e.mu.Lock()
	n := len(e.lookups)
	e.mu.Unlock()
	if n > 5 {
		t.Errorf("%d lookups", n)
	}
}

func TestUnsubscribePostFailures(t *testing.T) {
	e := newUnsubEnv(t)
	id := e.store("INBOX", e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example"))
	for name, c := range map[string]struct {
		err  error
		code api.ErrorCode
	}{
		"refused":     {&oneclick.StatusError{Code: 410}, api.CodeUnsubscribeFailed},
		"redirect":    {&oneclick.StatusError{Code: 302}, api.CodeUnsubscribeFailed},
		"server":      {&oneclick.StatusError{Code: 500}, api.CodeUnsubscribeFailed},
		"blocked":     {oneclick.ErrBlockedAddress, api.CodeUnsubscribeFailed},
		"unreachable": {errors.New("connection refused"), api.CodeNetworkError},
	} {
		e.postErr = c.err
		res, err := e.unsubscribe(id)
		if res != nil || errCode(t, err) != c.code {
			t.Errorf("%s: %+v %v", name, res, err)
		}
		if strings.Contains(err.Error(), "news.example") {
			t.Errorf("%s: the error names the sender: %v", name, err)
		}
	}
	if m := e.get(id); m.Unsubscribe.UnsubscribedAt != nil {
		t.Error("remembered after failures")
	}
}

// With the real client against a TLS test server: 2xx is accepted, a
// redirect and every other status are 1505, and the request is the
// one-click POST.
func TestUnsubscribeOverHTTP(t *testing.T) {
	e := newUnsubEnv(t)
	var mu sync.Mutex
	var reqs []string
	status := http.StatusAccepted
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqs = append(reqs, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type")+" "+string(b))
		code := status
		mu.Unlock()
		if code/100 == 3 {
			w.Header().Set("Location", "/elsewhere")
		}
		w.WriteHeader(code)
	}))
	defer srv.Close()
	// The test server is on loopback with its own certificate: only the
	// transport is replaced; Post still refuses redirects.
	p := &oneclick.Poster{Client: srv.Client(), Refuse: func(string) bool { return false }}
	e.b.OneClickPost = p.Post

	message := strings.Replace(raw(hdrOneClickURL, hdrOneClickPost), "https://news.example/u/abc", srv.URL+"/unsub?u=1", 1)
	id := e.store("INBOX", e.sign(message, "news.example"))
	for _, c := range []struct {
		code int
		want api.ErrorCode
	}{{http.StatusFound, api.CodeUnsubscribeFailed}, {http.StatusNotFound, api.CodeUnsubscribeFailed}, {http.StatusInternalServerError, api.CodeUnsubscribeFailed}, {http.StatusOK, 0}} {
		mu.Lock()
		status = c.code
		mu.Unlock()
		res, err := e.unsubscribe(id)
		if c.want == 0 {
			if err != nil || res.Outcome != api.UnsubscribeDone {
				t.Errorf("%d: %+v %v", c.code, res, err)
			}
		} else if errCode(t, err) != c.want {
			t.Errorf("%d: %v", c.code, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reqs) != 4 {
		t.Fatalf("requests: %v", reqs)
	}
	for _, r := range reqs {
		if r != "POST /unsub application/x-www-form-urlencoded List-Unsubscribe=One-Click" {
			t.Errorf("request %q", r)
		}
	}
}

func TestUnsubscribeRefusals(t *testing.T) {
	e := newUnsubEnv(t)
	good := e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example")
	for name, id := range map[string]api.MessageID{
		"junk folder":    e.store("Junk", good),
		"flagged junk":   e.store("INBOX", good, api.FlagJunk),
		"no offer":       e.store("INBOX", raw()),
		"automated mail": e.store("INBOX", raw("Auto-Submitted: auto-generated")),
		"http page only": e.store("INBOX", raw("List-Unsubscribe: <http://news.example/u>")),
	} {
		res, err := e.unsubscribe(id)
		if res != nil || errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	if _, err := e.unsubscribe("m_nope"); errCode(t, err) != api.CodeMessageNotFound {
		t.Errorf("unknown message: %v", err)
	}
	if _, err := e.b.Messages().Unsubscribe(context.Background(), api.MessageUnsubscribeParams{AccountID: "acc_nope", MessageID: "m"}); errCode(t, err) != api.CodeAccountNotFound {
		t.Errorf("unknown account: %v", err)
	}
	if _, err := e.b.Messages().Unsubscribe(context.Background(), api.MessageUnsubscribeParams{}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("empty params: %v", err)
	}
	if len(e.postedURLs()) != 0 || e.outboxCount() != 0 {
		t.Error("something was sent")
	}
}

func TestUnsubscribeRefusedForIssueTrackerAccounts(t *testing.T) {
	ctx := context.Background()
	e := newUnsubEnv(t)
	acct := store.Account{ID: "jira1", Name: "Acme", Enabled: true,
		Config: api.AccountConfig{Name: "Acme", Email: "jana@example.invalid", Kind: api.AccountJira,
			Jira: &api.JiraConfig{SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud, Login: "jana@example.invalid"}}}
	if err := e.b.store.AddAccount(ctx, &acct); err != nil {
		t.Fatal(err)
	}
	f := seedFolders(t, e.b, "jira1", []store.Folder{{Mailbox: "space:1", Name: "Space", Path: "Space", Subscribed: true, Selectable: true}})["space:1"]
	m := &store.Message{AccountID: "jira1", FolderID: f.ID, RemoteID: "i:1", Subject: "ITSD-1: x", Date: time.Now(), ThreadID: "jira:1"}
	if err := e.b.store.UpsertMessages(ctx, []*store.Message{m}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"List-Unsubscribe": "<https://acme.example/u>", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"}
	if err := e.b.store.SetMessageBody(ctx, m.ID, store.BodyUpdate{Text: "x", Headers: headers}); err != nil {
		t.Fatal(err)
	}
	got, err := e.b.Messages().Get(ctx, api.MessageGetParams{AccountID: "jira1", MessageID: api.MessageID(m.ID)})
	if err != nil || got.Message.Bulk != nil || got.Message.Unsubscribe != nil {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := e.b.Messages().Unsubscribe(ctx, api.MessageUnsubscribeParams{AccountID: "jira1", MessageID: api.MessageID(m.ID)}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("unsubscribe: %v", err)
	}
}

func TestUnsubscribePageIsOnlyHandedBack(t *testing.T) {
	e := newUnsubEnv(t)
	id := e.store("INBOX", raw("List-Unsubscribe: <https://news.example/leave?id=7>"))
	res, err := e.unsubscribe(id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != api.UnsubscribeOpenURL || res.URL != "https://news.example/leave?id=7" || res.Unverified || res.UnsubscribedAt != nil {
		t.Fatalf("%+v", res)
	}
	if len(e.postedURLs()) != 0 || len(e.lookups) != 0 || e.outboxCount() != 0 {
		t.Error("something was sent or looked up")
	}
	if m := e.get(id); m.Unsubscribe.UnsubscribedAt != nil {
		t.Error("remembered")
	}
}

func TestUnsubscribeMailtoQueuesInOutbox(t *testing.T) {
	ctx := context.Background()
	e := newUnsubEnv(t)
	id := e.store("INBOX", raw("List-Unsubscribe: <mailto:leave@news.example?subject=remove%20me&body=Please%20remove%20me&cc=x@y.example>, <https://news.example/u>"))
	e.out.reset()
	res, err := e.unsubscribe(id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != api.UnsubscribeQueued || res.UnsubscribedAt == nil || res.URL != "" {
		t.Fatalf("%+v", res)
	}
	if calls := e.out.recorded(); len(calls) != 1 || calls[0] != "wake:"+string(e.acc) {
		t.Errorf("outbox calls %v", calls)
	}
	if e.outboxCount() != 1 {
		t.Fatalf("pending outbox %d", e.outboxCount())
	}
	// It sits in the outbox folder, from the account's address, to the
	// first address only, and no draft was left behind.
	of, ok := outboxFolder(t, e.b, e.acc)
	if !ok {
		t.Fatal("no outbox folder")
	}
	list, err := e.b.Messages().List(ctx, api.MessageListParams{AccountID: e.acc, FolderID: of.ID})
	if err != nil || len(list.Messages) != 1 {
		t.Fatalf("outbox listing: %+v %v", list, err)
	}
	q := list.Messages[0]
	if q.Subject != "remove me" || len(q.To) != 1 || q.To[0].Address != "leave@news.example" || q.From[0].Address != "me@example.invalid" || q.Outbox == nil {
		t.Errorf("queued: %+v", q)
	}
	rf, err := e.b.store.OpenMessageRaw(ctx, string(e.acc), string(q.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer rf.Close()
	b, _ := io.ReadAll(rf)
	text := string(b)
	for _, want := range []string{"Subject: remove me", "To: <leave@news.example>", "From:", "Please remove me"} {
		if !strings.Contains(text, want) && !strings.Contains(text, strings.Replace(want, "<leave@news.example>", "leave@news.example", 1)) {
			t.Errorf("message lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "x@y.example") {
		t.Error("the cc of the mailto: URI was used")
	}
	drafts, err := e.b.Drafts().List(ctx, api.DraftListParams{AccountID: e.acc})
	if err != nil || len(drafts.Drafts) != 0 {
		t.Errorf("drafts: %+v %v", drafts, err)
	}
	// Remembered.
	if m := e.get(id); m.Unsubscribe == nil || m.Unsubscribe.UnsubscribedAt == nil {
		t.Errorf("offer after: %+v", m.Unsubscribe)
	}
}

func TestUnsubscribeMailtoDefaultSubject(t *testing.T) {
	ctx := context.Background()
	e := newUnsubEnv(t)
	id := e.store("INBOX", raw("List-Unsubscribe: <mailto:leave@news.example>"))
	if res, err := e.unsubscribe(id); err != nil || res.Outcome != api.UnsubscribeQueued {
		t.Fatalf("%+v %v", res, err)
	}
	of, _ := outboxFolder(t, e.b, e.acc)
	list, err := e.b.Messages().List(ctx, api.MessageListParams{AccountID: e.acc, FolderID: of.ID})
	if err != nil || len(list.Messages) != 1 || list.Messages[0].Subject != "unsubscribe" {
		t.Fatalf("%+v %v", list, err)
	}
}

// A list is remembered by its List-Id, a sender by its address.
func TestUnsubscribeRememberKeys(t *testing.T) {
	e := newUnsubEnv(t)
	a := e.store("INBOX", raw("List-Id: <go.news.example>", "List-Post: <mailto:go@news.example>", "List-Unsubscribe: <mailto:leave@news.example>"))
	if _, err := e.unsubscribe(a); err != nil {
		t.Fatal(err)
	}
	sameList := strings.Replace(raw("List-Id: <go.news.example>", "List-Post: <mailto:go@news.example>", "List-Unsubscribe: <mailto:leave@news.example>"), "news@news.example", "other@news.example", 1)
	if m := e.get(e.store("INBOX", sameList)); m.Unsubscribe.UnsubscribedAt == nil {
		t.Error("another sender of the same list is not remembered")
	}
	otherList := raw("List-Id: <rust.news.example>", "List-Post: <mailto:rust@news.example>", "List-Unsubscribe: <mailto:leave@news.example>")
	if m := e.get(e.store("INBOX", otherList)); m.Unsubscribe.UnsubscribedAt != nil {
		t.Error("another list is remembered")
	}
}

// A message whose attachments stayed on the server is not whole on disk
// and not held: it is downloaded, and when that cannot be done the call
// fails or the check fails closed, never a request.
func TestUnsubscribeReducedMessageIsNeverSent(t *testing.T) {
	ctx := context.Background()
	e := newUnsubEnv(t)
	id := e.store("INBOX", e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example"))
	if _, err := e.b.store.DB().ExecContext(ctx, `UPDATE messages SET raw_state = 'partial', remote_parts = '["2"]', remote_bytes = 10 WHERE id = ?`, string(id)); err != nil {
		t.Fatal(err)
	}
	res, err := e.unsubscribe(id)
	if err == nil && (res.Outcome != api.UnsubscribeOpenURL || !res.Unverified) {
		t.Errorf("%+v", res)
	}
	if len(e.postedURLs()) != 0 {
		t.Error("posted")
	}
}

func TestBulkBackfill(t *testing.T) {
	ctx := context.Background()
	e := newUnsubEnv(t)
	b := e.b
	// Rows as they were before the migration: stored, not classified.
	id := e.store("INBOX", raw(hdrOneClickURL, hdrOneClickPost))
	plain := e.store("INBOX", raw())
	noFile := &store.Message{AccountID: string(e.acc), FolderID: e.folder["INBOX"].ID, UID: 99, Subject: "headers only", Date: time.Now(),
		Headers: map[string]string{"Auto-Submitted": "auto-replied"}}
	if err := b.store.UpsertMessages(ctx, []*store.Message{noFile}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.store.DB().ExecContext(ctx, `UPDATE messages SET bulk = '', list_id = ''`); err != nil {
		t.Fatal(err)
	}
	// The stored headers are stale (an older parser); the raw file wins.
	if _, err := b.store.DB().ExecContext(ctx, `UPDATE messages SET headers_json = '{}' WHERE id = ?`, string(id)); err != nil {
		t.Fatal(err)
	}

	if err := b.backfillBulk(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{string(id): "newsletter", string(plain): "none", noFile.ID: "automated"}
	for mid, kind := range want {
		m, err := b.store.GetMessage(ctx, string(e.acc), mid)
		if err != nil || m.Bulk != kind {
			t.Errorf("%s: %q %v, want %q", mid, m.Bulk, err, kind)
		}
	}
	if v, _, _ := b.store.GetMeta(ctx, metaBulkClassified); v != bulk.RuleVersion+":done" {
		t.Errorf("cursor %q", v)
	}
	// Finished: a row that is unclassified later is left alone by a rerun.
	if _, err := b.store.DB().ExecContext(ctx, `UPDATE messages SET bulk = '' WHERE id = ?`, string(plain)); err != nil {
		t.Fatal(err)
	}
	if err := b.backfillBulk(ctx); err != nil {
		t.Fatal(err)
	}
	if m, _ := b.store.GetMessage(ctx, string(e.acc), string(plain)); m.Bulk != "" {
		t.Errorf("rerun classified %q", m.Bulk)
	}
	// Another rule version starts over.
	if err := b.store.SetMeta(ctx, metaBulkClassified, "0:done"); err != nil {
		t.Fatal(err)
	}
	if err := b.backfillBulk(ctx); err != nil {
		t.Fatal(err)
	}
	for mid, kind := range want {
		if m, _ := b.store.GetMessage(ctx, string(e.acc), mid); m.Bulk != kind {
			t.Errorf("after the rule change %s: %q, want %q", mid, m.Bulk, kind)
		}
	}
	// And an interrupted pass resumes.
	if _, err := b.store.DB().ExecContext(ctx, `UPDATE messages SET bulk = ''`); err != nil {
		t.Fatal(err)
	}
	if err := b.store.SetMeta(ctx, metaBulkClassified, bulk.RuleVersion+":m_x"); err != nil {
		t.Fatal(err)
	}
	if err := b.backfillBulk(ctx); err != nil {
		t.Fatal(err)
	}
	if m, _ := b.store.GetMessage(ctx, string(e.acc), string(id)); m.Bulk != "newsletter" {
		t.Errorf("resumed: %q", m.Bulk)
	}
}

// A repeat within a minute is answered with what the first one did, with
// no second request or mail; after that a new request is made.
func TestUnsubscribeRepeatIsAnswered(t *testing.T) {
	ctx := context.Background()
	e := newUnsubEnv(t)
	id := e.store("INBOX", e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example"))
	first, err := e.unsubscribe(id)
	if err != nil || first.Outcome != api.UnsubscribeDone {
		t.Fatalf("%+v %v", first, err)
	}
	again, err := e.unsubscribe(id)
	if err != nil || again.Outcome != api.UnsubscribeDone || again.UnsubscribedAt == nil || !again.UnsubscribedAt.Equal(*first.UnsubscribedAt) {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	if n := len(e.postedURLs()); n != 1 {
		t.Fatalf("%d posts", n)
	}
	// Older than a minute: a new request.
	old := time.Now().Add(-2 * time.Minute)
	if err := e.b.store.RememberUnsubscription(ctx, string(e.acc), "from:news@news.example", "oneClick", old); err != nil {
		t.Fatal(err)
	}
	if res, err := e.unsubscribe(id); err != nil || res.Outcome != api.UnsubscribeDone {
		t.Fatalf("%+v %v", res, err)
	}
	if n := len(e.postedURLs()); n != 2 {
		t.Fatalf("%d posts", n)
	}

	// mailto: queued once.
	mid := e.store("INBOX", strings.Replace(raw("List-Unsubscribe: <mailto:leave@other.example>"), "news@news.example", "x@other.example", 1))
	for i := 0; i < 3; i++ {
		res, err := e.unsubscribe(mid)
		if err != nil || res.Outcome != api.UnsubscribeQueued || res.UnsubscribedAt == nil {
			t.Fatalf("mailto %d: %+v %v", i, res, err)
		}
	}
	if e.outboxCount() != 1 {
		t.Errorf("%d queued", e.outboxCount())
	}
}

// A second call for the same sender while one runs is a conflict.
func TestUnsubscribeInFlightConflict(t *testing.T) {
	e := newUnsubEnv(t)
	id := e.store("INBOX", e.sign(raw(hdrOneClickURL, hdrOneClickPost), "news.example"))
	started, release := make(chan struct{}), make(chan struct{})
	e.b.OneClickPost = func(context.Context, string) error {
		close(started)
		<-release
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := e.unsubscribe(id); done <- err }()
	<-started
	if _, err := e.unsubscribe(id); errCode(t, err) != api.CodeConflict {
		t.Errorf("concurrent call: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Released: the repeat is answered from the record.
	if res, err := e.unsubscribe(id); err != nil || res.Outcome != api.UnsubscribeDone {
		t.Errorf("after: %+v %v", res, err)
	}
}
