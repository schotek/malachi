// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Email is a saved e-mail item, as GetEmailsByItemGuids returns it.
type Email struct {
	GUID      string
	Private   bool
	Superior  *Ref // the deal or project of the e-mail's foreign keys
	Relations []Relation
	Created   string // ItemCreated as sent, for ordering duplicates
	Sender    string // SenderEmailAddress, cleaned
	SentOn    string // SentOn as sent
}

// Matches reports whether the item is the message with that sender and
// date: the same sender address (ignoring case) and the same SentOn to the
// minute. A Message-ID alone is no proof — any mail can claim another's —
// so an item found by it is used only when it matches (docs/eway.md §6).
// A SentOn without a zone is compared as UTC and as the date's own wall
// clock; one that does not parse never matches.
func (e *Email) Matches(sender string, sent time.Time) bool {
	s := CleanEmail(sender)
	if e == nil || s == "" || e.Sender != s || sent.IsZero() {
		return false
	}
	t, zoned := parseSentOn(e.SentOn)
	if t.IsZero() {
		return false
	}
	minute := func(x time.Time) time.Time { return x.Truncate(time.Minute) }
	if zoned {
		return minute(t.UTC()).Equal(minute(sent.UTC()))
	}
	wall := func(x time.Time) time.Time {
		return time.Date(x.Year(), x.Month(), x.Day(), x.Hour(), x.Minute(), 0, 0, time.UTC)
	}
	return wall(t).Equal(wall(sent.UTC())) || wall(t).Equal(wall(sent))
}

// parseSentOn reads a SentOn and says whether it carried a zone.
func parseSentOn(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, false
		}
	}
	return time.Time{}, false
}

func emailOf(w wireItem) Email {
	e := Email{GUID: cleanGUID(string(w.ItemGUID)), Private: w.IsPrivate != nil && *w.IsPrivate,
		Relations: relationsOf(w), Created: string(w.ItemCreated),
		Sender: CleanEmail(string(w.SenderEmailAddress)), SentOn: string(w.SentOn)}
	if g := cleanGUID(string(w.LeadsOutlookProject)); g != "" {
		e.Superior = &Ref{Kind: api.CrmDeal, GUID: g}
	} else if g := cleanGUID(string(w.ProjectsOutlookProjec)); g != "" {
		e.Superior = &Ref{Kind: api.CrmProject, GUID: g}
	}
	return e
}

// bracketed is a Message-ID in the form eWay-CRM stores and searches
// (T0 10: with the angle brackets, matched case-insensitively).
func bracketed(id string) string {
	id = strings.Trim(strings.TrimSpace(id), "<>")
	if id == "" {
		return ""
	}
	return "<" + id + ">"
}

// FindEmail is the saved e-mail with the Message-ID that is the message of
// that sender and date (Email.Matches), nil when none (D8); several (an
// old double save) → the oldest. An item with the Message-ID but another
// sender or date is someone else's and is never returned.
func (c *Client) FindEmail(ctx context.Context, messageID, sender string, sent time.Time) (*Email, error) {
	mid := bracketed(messageID)
	if mid == "" {
		return nil, nil
	}
	env, err := c.ok(ctx, "SearchEmails", map[string]any{"transmitObject": map[string]any{"MessageId": mid}})
	if err != nil {
		return nil, err
	}
	items, err := decodeItems(env.Data)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := parseTime(string(items[i].ItemCreated)), parseTime(string(items[j].ItemCreated))
		return !a.IsZero() && (b.IsZero() || a.Before(b))
	})
	for _, w := range items {
		if found := emailOf(w); found.Matches(sender, sent) {
			return c.GetEmail(ctx, found.GUID)
		}
	}
	return nil, nil
}

// GetEmail reads an e-mail item with its relations and foreign keys; nil
// when the CRM has none of that GUID (deleted, or private to someone else).
func (c *Client) GetEmail(ctx context.Context, guid string) (*Email, error) {
	g := cleanGUID(guid)
	if g == "" {
		return nil, nil
	}
	env, err := c.ok(ctx, "GetEmailsByItemGuids", map[string]any{
		"itemGuids": []string{g}, "includeRelations": true, "includeForeignKeys": true})
	if err != nil {
		return nil, err
	}
	items, err := decodeItems(env.Data)
	if err != nil {
		return nil, err
	}
	for _, w := range items {
		if cleanGUID(string(w.ItemGUID)) == g {
			e := emailOf(w)
			return &e, nil
		}
	}
	return nil, nil
}

// Items reads items of one kind by GUID (with their relations and foreign
// keys); GUIDs the CRM does not have are left out.
func (c *Client) Items(ctx context.Context, kind api.CrmItemKind, guids []string) ([]Item, error) {
	folder := FolderOf(kind)
	if folder == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "eway: unknown item kind")
	}
	var clean []string
	seen := map[string]bool{}
	for _, g := range guids {
		if g = cleanGUID(g); g != "" && !seen[g] {
			seen[g] = true
			clean = append(clean, g)
		}
	}
	if len(clean) == 0 {
		return nil, nil
	}
	env, err := c.ok(ctx, "Get"+folder+"ByItemGuids", map[string]any{
		"itemGuids": clean, "includeRelations": true, "includeForeignKeys": true})
	if err != nil {
		return nil, err
	}
	items, err := decodeItems(env.Data)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(items))
	for _, w := range items {
		if seen[cleanGUID(string(w.ItemGUID))] {
			out = append(out, itemOf(kind, w))
		}
	}
	return out, nil
}

// ContactsByEmail finds the contacts with addr in any of their three
// e-mail fields (exact, case-insensitive).
func (c *Client) ContactsByEmail(ctx context.Context, addr string) ([]Item, error) {
	env, err := c.ok(ctx, "SearchContacts", map[string]any{
		"transmitObject":        map[string]any{"Email1Address": addr, "Email2Address": addr, "Email3Address": addr},
		"binaryLogicalOperator": "OR",
	})
	if err != nil {
		return nil, err
	}
	return itemsOf(api.CrmContact, env)
}

// CompaniesByEmail finds the companies whose e-mail is addr.
func (c *Client) CompaniesByEmail(ctx context.Context, addr string) ([]Item, error) {
	env, err := c.ok(ctx, "SearchCompanies", map[string]any{"transmitObject": map[string]any{"Email": addr}})
	if err != nil {
		return nil, err
	}
	return itemsOf(api.CrmCompany, env)
}

// maxDomainRows bounds the companies one tracked-domain query reads.
const maxDomainRows = 50

// CompaniesByDomain finds the companies tracking the domain: a LIKE query
// on TrackedDomains (T0 6), then only those whose list names the domain
// exactly.
func (c *Client) CompaniesByDomain(ctx context.Context, domain string) ([]Item, error) {
	domain = strings.ToLower(likeValue(domain))
	if domain == "" || strings.ContainsAny(domain, " ") {
		return nil, nil
	}
	q := likeQuery(FolderCompanies, "TrackedDomains", "%"+domain+"%", maxDomainRows, "ItemGUID", "FileAs", "TrackedDomains")
	env, err := c.ok(ctx, "Query", map[string]any{"query": q})
	if err != nil {
		return nil, err
	}
	items, err := decodeItems(env.Data)
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, w := range items {
		if tracksDomain(string(w.TrackedDomains), domain) {
			it := itemOf(api.CrmCompany, w)
			it.Detail = cleanDetail(domain)
			out = append(out, it)
		}
	}
	return out, nil
}

// tracksDomain reports whether a TrackedDomains value names domain as one
// of its entries (separated by anything but letters, digits, dots and
// hyphens; a leading "@" or "*." ignored).
func tracksDomain(list, domain string) bool {
	for _, d := range strings.FieldsFunc(strings.ToLower(list), func(r rune) bool {
		return !(r == '.' || r == '-' || r == '@' || r == '*' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 0x7f)
	}) {
		if strings.TrimPrefix(strings.TrimPrefix(d, "@"), "*.") == domain {
			return true
		}
	}
	return false
}

// SearchByName finds items of the kind whose name contains text (the
// typed query, T0 7), contacts also by their first e-mail; at most take.
func (c *Client) SearchByName(ctx context.Context, kind api.CrmItemKind, text string, take int) ([]Item, error) {
	folder := FolderOf(kind)
	v := likeValue(text)
	if folder == "" || v == "" {
		return nil, nil
	}
	cols := []string{"FileAs"}
	if kind == api.CrmContact {
		cols = append(cols, "Email1Address")
	}
	if kind == api.CrmCompany {
		cols = append(cols, "Email")
	}
	var out []Item
	seen := map[string]bool{}
	for _, col := range cols {
		fields := []string{"ItemGUID", "FileAs"}
		if col != "FileAs" {
			fields = append(fields, col)
		}
		env, err := c.ok(ctx, "Query", map[string]any{"query": likeQuery(folder, col, "%"+v+"%", take, fields...)})
		if err != nil {
			return nil, err
		}
		items, err := itemsOf(kind, env)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if !seen[it.GUID] && len(out) < take {
				seen[it.GUID] = true
				out = append(out, it)
			}
		}
	}
	return out, nil
}

func itemsOf(kind api.CrmItemKind, env *envelope) ([]Item, error) {
	items, err := decodeItems(env.Data)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(items))
	for _, w := range items {
		out = append(out, itemOf(kind, w))
	}
	return out, nil
}
