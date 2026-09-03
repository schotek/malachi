package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/pkg/api"
)

func writeTestFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDraftValidation(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	ok := api.Draft{AccountID: "acc", To: []api.Address{{Address: "a@example.invalid"}}, Subject: "s", TextBody: "t"}

	many := make([]api.Address, api.MaxDraftRecipients+1)
	for i := range many {
		many[i] = api.Address{Address: "a@example.invalid"}
	}
	manyAtts := make([]api.DraftAttachment, api.MaxDraftAttachments+1)
	for i := range manyAtts {
		manyAtts[i] = api.DraftAttachment{ID: "att_x"}
	}

	cases := map[string]func(d *api.Draft){
		"no account":        func(d *api.Draft) { d.AccountID = "" },
		"bad address":       func(d *api.Draft) { d.To = []api.Address{{Address: "not an address"}} },
		"address with name": func(d *api.Draft) { d.To = []api.Address{{Address: "Alice <a@example.invalid>"}} },
		"name with CRLF":    func(d *api.Draft) { d.To = []api.Address{{Name: "x\r\nBcc: y", Address: "a@example.invalid"}} },
		"subject with LF":   func(d *api.Draft) { d.Subject = "a\nb" },
		"subject with NUL":  func(d *api.Draft) { d.Subject = "a\x00b" },
		"subject too long":  func(d *api.Draft) { d.Subject = strings.Repeat("s", api.MaxDraftSubjectBytes+1) },
		"invalid utf8 body": func(d *api.Draft) { d.TextBody = "\xff\xfe" },
		"body too long":     func(d *api.Draft) { d.TextBody = strings.Repeat("b", api.MaxDraftBodyBytes+1) },
		"html too long":     func(d *api.Draft) { d.HTMLBody = strings.Repeat("b", api.MaxDraftBodyBytes+1) },
		"reply and forward": func(d *api.Draft) { d.InReplyTo, d.Forwarding = "m1", "m2" },
		"too many rcpts":    func(d *api.Draft) { d.To = many },
		"too many atts":     func(d *api.Draft) { d.Attachments = manyAtts },
		"att without id":    func(d *api.Draft) { d.Attachments = []api.DraftAttachment{{}} },
		"negative version":  func(d *api.Draft) { d.Version = -1 },
	}
	for name, mutate := range cases {
		d := ok
		mutate(&d)
		_, err := b.Drafts().Save(ctx, api.DraftSaveParams{Draft: d})
		if code := errCode(t, err); code != api.CodeInvalidArgument {
			t.Errorf("%s: code %d, want invalidArgument", name, code)
		}
	}
}

func TestPlainDraftRoundTrip(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	d := b.Drafts()

	res, err := d.Save(ctx, api.DraftSaveParams{Draft: api.Draft{
		AccountID: "acc", To: []api.Address{{Name: "Alice", Address: "a@example.invalid"}},
		Subject: "Hello", TextBody: "line1\r\nline2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.DraftID == "" || res.Version != 1 || res.TextBody != "line1\nline2" || res.HTMLBody != "" {
		t.Fatalf("save = %+v", res)
	}

	list, err := d.List(ctx, api.DraftListParams{AccountID: "acc"})
	if err != nil {
		t.Fatal(err)
	}
	if list.Page.Total != 1 || len(list.Drafts) != 1 || list.Drafts[0].ID != res.DraftID ||
		list.Drafts[0].To[0].Name != "Alice" || list.Drafts[0].UpdatedAt.IsZero() {
		t.Fatalf("list = %+v", list)
	}

	// Stale version → conflict; unknown id → draftNotFound.
	_, err = d.Save(ctx, api.DraftSaveParams{Draft: api.Draft{ID: res.DraftID, AccountID: "acc", Version: 0, To: list.Drafts[0].To}})
	if errCode(t, err) != api.CodeConflict {
		t.Errorf("stale version: %v", err)
	}
	_, err = d.Save(ctx, api.DraftSaveParams{Draft: api.Draft{ID: "d_nope", AccountID: "acc", Version: 1}})
	if errCode(t, err) != api.CodeDraftNotFound {
		t.Errorf("unknown id: %v", err)
	}
	_, err = d.List(ctx, api.DraftListParams{AccountID: "acc", Page: api.Page{Cursor: "??"}})
	if errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("bad cursor: %v", err)
	}

	if _, err := d.Delete(ctx, api.DraftDeleteParams{AccountID: "acc", DraftID: res.DraftID}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Delete(ctx, api.DraftDeleteParams{AccountID: "acc", DraftID: res.DraftID}); err != nil {
		t.Errorf("second delete: %v", err)
	}
	list, _ = d.List(ctx, api.DraftListParams{AccountID: "acc"})
	if list.Page.Total != 0 {
		t.Errorf("draft survived delete: %+v", list)
	}
}

// With the stub sanitiser an HTML draft must fail closed and leave the
// store untouched.
func TestHTMLDraftFailsClosedWithStubSanitiser(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	d := b.Drafts()

	first, err := d.Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: "acc", TextBody: "plain"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Save(ctx, api.DraftSaveParams{Draft: api.Draft{
		ID: first.DraftID, AccountID: "acc", Version: 1, HTMLBody: `<script>alert(1)</script><p>hi</p>`,
	}})
	if errCode(t, err) != api.CodeSanitizeFailed {
		t.Fatalf("html with stub sanitiser: %v", err)
	}
	list, _ := d.List(ctx, api.DraftListParams{AccountID: "acc"})
	if list.Page.Total != 1 || list.Drafts[0].Version != 1 || list.Drafts[0].TextBody != "plain" || list.Drafts[0].HTMLBody != "" {
		t.Fatalf("failed save changed the draft: %+v", list.Drafts)
	}
	if _, err := d.Create(ctx, api.DraftCreateParams{AccountID: "acc", Mode: api.ComposeNew}); errCode(t, err) != api.CodeNotImplemented {
		t.Errorf("create: %v", err)
	}
}

// A fake sanitiser lets the inline-attachment reconciliation be exercised
// before the real one exists.
func TestHTMLDraftInlineReconciliation(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t, config.Default())
	b.Sanitize = func(in sanitize.Input) (sanitize.Output, error) {
		out := sanitize.Output{HTML: in.HTML, Text: "text of " + in.HTML}
		for cid := range in.KnownCIDs {
			if strings.Contains(in.HTML, "cid:"+cid) {
				out.CIDs = append(out.CIDs, cid)
			}
		}
		return out, nil
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	imp := func(name string, inline bool) api.DraftAttachment {
		t.Helper()
		res, err := b.Attachments().Import(ctx, api.AttachmentImportParams{
			AccountID: "acc", Path: writeTestFile(t, name, png), Inline: inline,
		})
		if err != nil {
			t.Fatal(err)
		}
		return res.Attachment
	}
	used, unused, regular := imp("used.png", true), imp("unused.png", true), imp("plain.png", false)
	if used.ContentID == "" || !used.Inline || regular.ContentID != "" {
		t.Fatalf("import metadata: used=%+v regular=%+v", used, regular)
	}

	res, err := b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{
		AccountID:   "acc",
		HTMLBody:    `<p>pic <img src="cid:` + used.ContentID + `"></p>`,
		TextBody:    "ignored",
		Attachments: []api.DraftAttachment{{ID: used.ID}, {ID: unused.ID}, {ID: regular.ID}, {ID: used.ID}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.TextBody, "text of ") || res.HTMLBody == "" {
		t.Errorf("derived bodies: %+v", res)
	}
	var ids []string
	for _, a := range res.Attachments {
		ids = append(ids, a.ID)
	}
	if strings.Join(ids, ",") != used.ID+","+regular.ID {
		t.Errorf("bound attachments = %v, want [%s %s]", ids, used.ID, regular.ID)
	}

	// Foreign / unknown attachment ids.
	_, err = b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: "acc", Attachments: []api.DraftAttachment{{ID: "att_nope"}}}})
	if errCode(t, err) != api.CodeAttachmentNotFound {
		t.Errorf("unknown attachment: %v", err)
	}
	// Bound to another draft.
	_, err = b.Drafts().Save(ctx, api.DraftSaveParams{Draft: api.Draft{AccountID: "acc", Attachments: []api.DraftAttachment{{ID: regular.ID}}}})
	if errCode(t, err) != api.CodeAttachmentNotFound {
		t.Errorf("bound elsewhere: %v", err)
	}
}
