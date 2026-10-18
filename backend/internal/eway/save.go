// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// maxRecipientsBytes caps the To and Cc fields sent with a message, cut at
// an address boundary (D7).
const maxRecipientsBytes = 4 << 10

// Message is what SaveMessage sends: the raw message as the server
// delivered it, and the fields of D7, all from the daemon's own store.
type Message struct {
	Raw         []byte
	FileName    string // "<subject>.eml", already safe
	MessageID   string // the Message-ID header, without or with brackets; "" = none
	Subject     string
	Sender      string // the From address
	To, Cc      []string
	SentOn      time.Time // the Date header, else the internal date
	Received    time.Time
	Note        string // plain text, ≤ api.MaxCrmNoteBytes
	Attachments int
	Private     bool
	Superior    *Ref // the deal or project set as the superior item when created
}

// Saved is the outcome of SaveMessage.
type Saved struct {
	GUID    string
	Created bool // this call created the item
}

// NewGUID is a random (version 4) GUID.
func NewGUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// SaveMessage saves m as an e-mail item (D8): an item with its Message-ID
// already in the CRM is that item (nothing is uploaded); else the raw
// message goes up under a new GUID first and SaveEmail names it. A
// duplicate by the server's hash is the item found by Message-ID once
// more; when there is none the uploaded binary is left orphaned (a
// best-effort DeleteEmail of its GUID, T0 12) and the error is
// crmDuplicate.
func (c *Client) SaveMessage(ctx context.Context, m Message) (Saved, error) {
	sent := SentOnOf(m)
	if m.MessageID != "" {
		found, err := c.FindEmail(ctx, m.MessageID, m.Sender, sent)
		if err != nil {
			return Saved{}, err
		}
		if found != nil {
			return Saved{GUID: found.GUID}, nil
		}
	}
	guid := NewGUID()
	if err := c.upload(ctx, guid, m.FileName, m.Raw); err != nil {
		// The service may have kept what it received.
		c.forgetDetached(ctx, guid)
		return Saved{}, err
	}
	env, err := c.call(ctx, "SaveEmail", map[string]any{"transmitObject": transmitOf(guid, m)})
	if err != nil {
		// Whether the item exists is unknown: drop it (or the upload).
		c.forgetDetached(ctx, guid)
		return Saved{}, err
	}
	switch rc := string(env.ReturnCode); rc {
	case rcSuccess:
		return Saved{GUID: guid, Created: true}, nil
	case rcDuplicateEmail:
		if m.MessageID != "" {
			found, err := c.FindEmail(ctx, m.MessageID, m.Sender, sent)
			if err != nil {
				return Saved{}, err
			}
			if found != nil {
				c.forget(ctx, guid)
				return Saved{GUID: found.GUID}, nil
			}
		}
		c.forget(ctx, guid)
		return Saved{}, api.NewError(api.CodeCrmDuplicate, "eway: the CRM already has this message")
	default:
		c.forget(ctx, guid)
		return Saved{}, api.NewError(api.CodeServerError, "eway: SaveEmail: %s", rcClass(rc))
	}
}

// SentOnOf is the date sent as SentOn (and matched by FindEmail): the
// Date header, else the time received.
func SentOnOf(m Message) time.Time {
	if !m.SentOn.IsZero() {
		return m.SentOn
	}
	return m.Received
}

// forgetDetachedTimeout bounds the clean-up after a failed save.
const forgetDetachedTimeout = 10 * time.Second

// forgetDetached is forget with a short context of its own, so that the
// clean-up runs even when the caller's ended (that may be why it failed);
// never after the client is closed.
func (c *Client) forgetDetached(ctx context.Context, guid string) {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), forgetDetachedTimeout)
	defer cancel()
	c.forget(fctx, guid)
}

// forget asks the CRM to drop what was uploaded under guid; it answers
// success whether or not anything went (T0 12).
func (c *Client) forget(ctx context.Context, guid string) {
	if _, err := c.call(ctx, "DeleteEmail", map[string]any{"itemGuid": guid}); err != nil {
		c.log.Debug("eway could not drop an upload", "code", asAPI(err).Code.String())
	}
}

// transmitOf is SaveEmail's item (D7). FileAs, SenderEmailAddress, To and
// SentOn go all four or none (the server's duplicate hash needs them):
// without a sender or a recipient none of them goes, and the subject is
// the item's only name.
func transmitOf(guid string, m Message) map[string]any {
	subject := cleanName(m.Subject)
	sender := CleanEmail(m.Sender)
	to := joinAddresses(m.To)
	t := map[string]any{
		"ItemGUID":           guid,
		"Subject":            subject,
		"ReceivedTime":       m.Received.UTC().Format(time.RFC3339),
		"AttachmentsCount":   m.Attachments,
		"EmailFileExtension": ".eml",
		"IsPrivate":          m.Private,
	}
	if sender != "" && to != "" {
		fileAs := subject
		if fileAs == "" {
			fileAs = sender
		}
		t["FileAs"] = fileAs
		t["SenderEmailAddress"] = sender
		t["To"] = to
		t["SentOn"] = SentOnOf(m).Format(time.RFC3339)
	}
	if cc := joinAddresses(m.Cc); cc != "" {
		t["Cc"] = cc
	}
	if mid := bracketed(m.MessageID); mid != "" {
		t["MessageId"] = mid
	}
	if m.Note != "" {
		t["Note"] = m.Note
	}
	if m.Superior != nil {
		setSuperior(t, *m.Superior)
	}
	return t
}

func joinAddresses(in []string) string {
	var out []string
	for _, a := range in {
		if a = CleanEmail(a); a != "" {
			out = append(out, a)
		}
	}
	return joinCapped(out, "; ", maxRecipientsBytes)
}

// setSuperior sets the e-mail's foreign key of the superior item: setting
// one creates the OUTLOOKPROJECT relation and replaces the other (T0 4).
func setSuperior(t map[string]any, r Ref) {
	switch r.Kind {
	case api.CrmDeal:
		t["Leads_OutlookProjectGuid"] = r.GUID
	case api.CrmProject:
		t["Projects_OutlookProjectGuid"] = r.GUID
	}
}

// upload sends the raw message as the binary of guid
// (SaveBinaryAttachment, before SaveEmail), signing in once more on
// rcBadSession.
func (c *Client) upload(ctx context.Context, guid, name string, raw []byte) error {
	for attempt := 0; ; attempt++ {
		sid, err := c.logIn(ctx)
		if err != nil {
			return err
		}
		q := url.Values{"sessionId": {sid}, "itemGuid": {guid}, "fileName": {name}}
		data, err := c.exchange(ctx, request{method: http.MethodPost, path: "SaveBinaryAttachment?" + q.Encode(),
			body: raw, ctype: "application/octet-stream", timeout: uploadTimeout, secret: sid})
		if err != nil {
			if errors.Is(err, errMethodUnknown) {
				err = api.NewError(api.CodeServerError, "eway: the service does not take uploads")
			}
			c.record(ctx, asAPI(err))
			return err
		}
		env, err := decodeEnvelope(data)
		if err != nil {
			return err
		}
		switch rc := string(env.ReturnCode); rc {
		case rcSuccess:
			return nil
		case rcBadSession:
			c.dropSession(sid)
			if attempt == 0 {
				continue
			}
			return api.NewError(api.CodeServerError, "eway: upload: session refused twice")
		default:
			return api.NewError(api.CodeServerError, "eway: SaveBinaryAttachment: %s", rcClass(rc))
		}
	}
}

// SetPrivate changes who sees the item (T0 11; the binary stays).
func (c *Client) SetPrivate(ctx context.Context, guid string, private bool) error {
	_, err := c.ok(ctx, "SaveEmail", map[string]any{"transmitObject": map[string]any{"ItemGUID": guid, "IsPrivate": private}})
	return toAPI(err)
}

// Delete removes the e-mail item with its relations, for every user of
// the CRM.
func (c *Client) Delete(ctx context.Context, guid string) error {
	_, err := c.ok(ctx, "DeleteEmail", map[string]any{"itemGuid": guid})
	return toAPI(err)
}

func toAPI(err error) error {
	if err == nil {
		return nil
	}
	return asAPI(err)
}

// Links is what ApplyLinks changes on a saved e-mail.
type Links struct {
	Link     []Ref
	Unlink   []Ref
	Superior *Ref
}

// CheckItems reports crmItemNotFound unless the CRM has every item named
// (one Get…ByItemGuids per kind). crm.save calls it before anything goes
// out, so an unknown item changes nothing.
func (c *Client) CheckItems(ctx context.Context, refs []Ref) error {
	want := map[api.CrmItemKind][]string{}
	for _, r := range refs {
		want[r.Kind] = append(want[r.Kind], r.GUID)
	}
	for kind, guids := range want {
		have, err := c.Items(ctx, kind, guids)
		if err != nil {
			return toAPI(err)
		}
		found := map[string]bool{}
		for _, it := range have {
			found[it.GUID] = true
		}
		for _, g := range guids {
			if !found[strings.ToLower(g)] {
				return api.NewError(api.CodeCrmItemNotFound, "eway: no such %s", kind)
			}
		}
	}
	return nil
}

// ApplyLinks changes a saved e-mail's links (the items checked before,
// CheckItems): the superior item through the foreign key unless it
// already is (then the relations are read again: the old superior's
// relation went with the replaced key); a GENERAL relation for each Link
// item the e-mail is not related to now, a demoted superior included (a
// duplicate relation counts as done); the relations of each Unlink item
// deleted (DeleteRelation by RelationDataGUID, T0 5), and when the
// superior item is unlinked its foreign key set to null as well — the
// relation and the key are both cleared whatever the service does with
// either.
func (c *Client) ApplyLinks(ctx context.Context, e *Email, l Links) error {
	if l.Superior != nil && (e.Superior == nil || *e.Superior != *l.Superior) {
		t := map[string]any{"ItemGUID": e.GUID}
		setSuperior(t, *l.Superior)
		if _, err := c.ok(ctx, "SaveEmail", map[string]any{"transmitObject": t}); err != nil {
			return toAPI(err)
		}
		// The new key replaced the old superior's OUTLOOKPROJECT relation:
		// the links are what the CRM has now, so a demoted superior that
		// stays in Link gets its GENERAL relation below.
		now, err := c.GetEmail(ctx, e.GUID)
		if err != nil {
			return toAPI(err)
		}
		if now == nil {
			return api.NewError(api.CodeServerError, "eway: the saved message cannot be read back")
		}
		e = now
	}
	related := map[string]bool{}
	for _, r := range e.Relations {
		related[r.ForeignGUID] = true
	}
	if l.Superior != nil {
		related[l.Superior.GUID] = true
	}
	for _, r := range l.Link {
		if related[r.GUID] {
			continue
		}
		env, err := c.call(ctx, "SaveRelation", map[string]any{"transmitObject": map[string]any{
			"ItemGUID1": e.GUID, "FolderName1": FolderEmails,
			"ItemGUID2": r.GUID, "FolderName2": FolderOf(r.Kind),
			"RelationType": RelationGeneral,
		}})
		if err != nil {
			return err
		}
		if rc := string(env.ReturnCode); rc != rcSuccess && rc != rcDuplicateRelation {
			return api.NewError(api.CodeServerError, "eway: SaveRelation: %s", rcClass(rc))
		}
		related[r.GUID] = true
	}
	for _, r := range l.Unlink {
		for _, rel := range e.Relations {
			if rel.ForeignGUID != r.GUID || rel.RelationData == "" {
				continue
			}
			if _, err := c.ok(ctx, "DeleteRelation", map[string]any{"relationDataGuid": rel.RelationData}); err != nil {
				return toAPI(err)
			}
		}
		if e.Superior != nil && e.Superior.GUID == r.GUID && (l.Superior == nil || l.Superior.GUID != r.GUID) {
			key := "Leads_OutlookProjectGuid"
			if e.Superior.Kind == api.CrmProject {
				key = "Projects_OutlookProjectGuid"
			}
			// An explicit JSON null (a nil interface value in the map).
			t := map[string]any{"ItemGUID": e.GUID, key: nil}
			if _, err := c.ok(ctx, "SaveEmail", map[string]any{"transmitObject": t}); err != nil {
				return toAPI(err)
			}
		}
	}
	return nil
}
