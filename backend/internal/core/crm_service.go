// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/eway"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/safename"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The crm.* methods (docs/api.md §4.17, docs/eway.md D6–D13): saving
// messages of mail accounts to an eWay-CRM account, what they are linked
// to there, and the local record (store/crm_links.go), a display cache of
// what the CRM said. Only explicit calls send anything; nothing here runs
// on its own except the background check of an old record that
// message.get returned.

// crmStaleAfter is the age of a record after which message.get has it
// checked again (D12).
const crmStaleAfter = 24 * time.Hour

// crmCheckTimeout bounds one background check.
const crmCheckTimeout = 30 * time.Second

// crmState is the in-memory state of the crm methods: saves and removals
// in flight (per CRM account, mail account and message key: a second one
// is conflict; a background check claims the key too).
type crmState struct {
	mu   sync.Mutex
	busy map[string]bool

	follow crmFollow // following saved conversations (crm_follow.go)
}

func crmBusyKey(crm, mail, key string) string { return crm + "\x00" + mail + "\x00" + key }

// claim marks the keys busy, or reports false (nothing marked) when one is.
func (s *crmState) claim(keys []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy == nil {
		s.busy = map[string]bool{}
	}
	for _, k := range keys {
		if s.busy[k] {
			return false
		}
	}
	for _, k := range keys {
		s.busy[k] = true
	}
	return true
}

func (s *crmState) release(keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		delete(s.busy, k)
	}
}

type crmService struct{ b *Backend }

// Crm is the CRM service.
func (b *Backend) Crm() api.CrmService { return &crmService{b} }

// crmAccount resolves the eway account of a call: the named one, or
// without a name the first enabled eway account in account.list order
// (D11). A paused account is unavailable.
func (b *Backend) crmAccount(ctx context.Context, id api.AccountID) (store.Account, *eway.Client, error) {
	var a store.Account
	if id == "" {
		list, err := b.store.ListAccounts(ctx)
		if err != nil {
			return a, nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		found := false
		for _, x := range list {
			if isCrmAccount(x) && x.Enabled {
				a, found = x, true
				break
			}
		}
		if !found {
			return a, nil, api.NewError(api.CodeAccountNotFound, "no enabled eWay-CRM account")
		}
	} else {
		var err error
		if a, err = b.requireAccount(ctx, string(id)); err != nil {
			return a, nil, err
		}
		if !isCrmAccount(a) {
			return a, nil, api.NewError(api.CodeInvalidArgument, "account %q is not a CRM account", id)
		}
		if !a.Enabled {
			return a, nil, api.NewError(api.CodeUnavailable, "account %q is paused", id)
		}
	}
	c, err := b.crmClient(a)
	return a, c, err
}

// crmClient is the running client of an enabled eway account, started on
// demand (a test that replaced the supervisor, a call before StartSync).
func (b *Backend) crmClient(a store.Account) (*eway.Client, error) {
	if c, ok := b.ewaySync.Client(a.ID); ok {
		return c, nil
	}
	b.ewaySync.Start(a)
	if c, ok := b.ewaySync.Client(a.ID); ok {
		return c, nil
	}
	return nil, api.NewError(api.CodeInvalidArgument, "account %q has no usable eWay-CRM settings", a.ID)
}

// crmMessages resolves the mail account and the messages of a call (D6):
// a mail account (imap, graph), 1..MaxCrmMessages distinct messages, none
// in the Outbox or a Drafts folder.
func (b *Backend) crmMessages(ctx context.Context, mailID api.AccountID, ids []api.MessageID) (store.Account, []store.Message, error) {
	mail, err := b.requireAccount(ctx, string(mailID))
	if err != nil {
		if mailID == "" {
			return mail, nil, api.NewError(api.CodeInvalidArgument, "messageAccountId is required")
		}
		return mail, nil, err
	}
	if k := mail.Config.Protocol(); k != api.AccountIMAP && k != api.AccountGraph {
		return mail, nil, api.NewError(api.CodeInvalidArgument, "only messages of mail accounts can be saved to a CRM")
	}
	if len(ids) == 0 || len(ids) > api.MaxCrmMessages {
		return mail, nil, api.NewError(api.CodeInvalidArgument, "messageIds: 1 to %d messages", api.MaxCrmMessages)
	}
	seen := map[api.MessageID]bool{}
	roles := map[string]api.FolderRole{}
	out := make([]store.Message, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			return mail, nil, api.NewError(api.CodeInvalidArgument, "messageIds must be distinct and not empty")
		}
		seen[id] = true
		m, err := b.getMessage(ctx, mail.ID, string(id))
		if err != nil {
			return mail, nil, err
		}
		role, ok := roles[m.FolderID]
		if !ok {
			f, err := b.store.GetFolder(ctx, mail.ID, m.FolderID)
			if err != nil {
				return mail, nil, api.NewError(api.CodeStorageError, "%v", err)
			}
			role, roles[m.FolderID] = f.Role, f.Role
		}
		if role == api.RoleOutbox || role == api.RoleDrafts {
			return mail, nil, api.NewError(api.CodeInvalidArgument, "messages of the Outbox or a Drafts folder cannot be saved to a CRM")
		}
		out = append(out, m)
	}
	return mail, out, nil
}

// crmWholeSize reports, without reading it, whether the message is whole
// on this device (the copy message.download holds in memory, else a
// stored file that is not a skeleton, as storedWhole) and its size.
func (b *Backend) crmWholeSize(ctx context.Context, accountID string, m store.Message) (int64, bool) {
	if held, ok := b.mem.get(accountID, m.ID); ok {
		return int64(len(held.raw)), true
	}
	if m.RawState == store.RawPartial || len(m.RemoteParts) > 0 {
		return 0, false
	}
	f, err := b.store.OpenMessageRaw(ctx, accountID, m.ID)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	n, err := f.Size()
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// crmWhole is the message as the server delivered it, read right before
// its upload (crmWholeSize decided before that it is whole). A file over
// ingest.MaxMessageBytes comes back one byte over it.
func (b *Backend) crmWhole(ctx context.Context, accountID string, m store.Message) ([]byte, bool) {
	if held, ok := b.mem.get(accountID, m.ID); ok {
		return held.raw, true
	}
	if m.RawState == store.RawPartial || len(m.RemoteParts) > 0 {
		return nil, false
	}
	f, err := b.store.OpenMessageRaw(ctx, accountID, m.ID)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, ingest.MaxMessageBytes+1))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

// crmAddresses are the addresses the suggestions search for (D9): From,
// Reply-To, To and Cc of every message, lower case, without the user's
// own addresses of every account, From first, at most
// MaxCrmLookupAddresses.
func (b *Backend) crmAddresses(ctx context.Context, msgs []store.Message) []string {
	own := map[string]bool{}
	if list, err := b.store.ListAccounts(ctx); err == nil {
		for _, a := range list {
			addrs, err := b.boardOwn(ctx, a)
			if err != nil {
				continue
			}
			for _, s := range addrs {
				own[s] = true
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(list []api.Address) {
		for _, a := range list {
			e := eway.CleanEmail(a.Address)
			if e == "" || own[e] || seen[e] || len(out) >= api.MaxCrmLookupAddresses {
				continue
			}
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, m := range msgs {
		add(m.From)
	}
	for _, m := range msgs {
		add(m.ReplyTo)
		add(m.To)
		add(m.CC)
	}
	return out
}

// crmItem is the wire form of an item of the CRM account.
func crmItem(serviceURL string, kind api.CrmItemKind, guid, name, detail string, reason api.CrmReason, superior bool) api.CrmItem {
	return api.CrmItem{Kind: kind, GUID: guid, Name: name, Detail: detail, Reason: reason, Superior: superior,
		URL: eway.ItemLink(serviceURL, kind, guid)}
}

// crmDescribe reads the names of what the e-mail item is linked to and
// builds its record and the relations kept locally.
func (b *Backend) crmDescribe(ctx context.Context, crm store.Account, c *eway.Client, e *eway.Email) ([]api.CrmItem, []store.CrmRelation, error) {
	byKind := map[api.CrmItemKind][]string{}
	type rel struct {
		kind     api.CrmItemKind
		guid     string
		superior bool
	}
	var rels []rel
	seen := map[string]int{}
	for _, r := range e.Relations {
		if r.Kind == "" {
			continue
		}
		sup := r.Type == eway.RelationSuperior || (e.Superior != nil && e.Superior.GUID == r.ForeignGUID)
		if i, ok := seen[r.ForeignGUID]; ok {
			rels[i].superior = rels[i].superior || sup
			continue
		}
		seen[r.ForeignGUID] = len(rels)
		rels = append(rels, rel{r.Kind, r.ForeignGUID, sup})
		byKind[r.Kind] = append(byKind[r.Kind], r.ForeignGUID)
	}
	if e.Superior != nil {
		if _, ok := seen[e.Superior.GUID]; !ok {
			seen[e.Superior.GUID] = len(rels)
			rels = append(rels, rel{e.Superior.Kind, e.Superior.GUID, true})
			byKind[e.Superior.Kind] = append(byKind[e.Superior.Kind], e.Superior.GUID)
		}
	}
	names := map[string]eway.Item{}
	for kind, guids := range byKind {
		items, err := c.Items(ctx, kind, guids)
		if err != nil {
			return nil, nil, eway.ToAPIError(err)
		}
		for _, it := range items {
			names[it.GUID] = it
		}
	}
	svc := crm.Config.Eway.ServiceURL
	items := make([]api.CrmItem, 0, len(rels))
	stored := make([]store.CrmRelation, 0, len(rels))
	for _, r := range rels {
		it := names[r.guid]
		items = append(items, crmItem(svc, r.kind, r.guid, it.Name, it.Detail, api.CrmReasonSaved, r.superior))
		stored = append(stored, store.CrmRelation{Kind: r.kind, GUID: r.guid, Name: it.Name, Detail: it.Detail, Superior: r.superior})
	}
	return items, stored, nil
}

// crmRecord is the wire form of a stored link.
func crmRecord(crm store.Account, l store.CrmLink) api.CrmRecord {
	svc := crm.Config.Eway.ServiceURL
	rec := api.CrmRecord{AccountID: api.AccountID(crm.ID), ItemGUID: l.ItemGUID, SavedAt: l.SavedAt, CheckedAt: l.CheckedAt,
		Private: l.Private, Auto: l.Auto, Items: make([]api.CrmItem, 0, len(l.Relations)), URL: eway.OpenLink(svc, eway.FolderEmails, l.ItemGUID)}
	for _, r := range l.Relations {
		if !eway.ValidGUID(r.GUID) || eway.FolderOf(r.Kind) == "" {
			continue
		}
		rec.Items = append(rec.Items, crmItem(svc, r.Kind, strings.ToLower(r.GUID), r.Name, r.Detail, api.CrmReasonSaved, r.Superior))
	}
	return rec
}

// crmIdentity is the sender address and date a CRM item must have to be
// taken for m when it is found by Message-ID (eway.Email.Matches): the
// From address, and the Date header else the time received, as crm.save
// sends them.
func crmIdentity(m store.Message) (string, time.Time) {
	var sender string
	if len(m.From) > 0 {
		sender = m.From[0].Address
	}
	sent := m.Date
	if sent.IsZero() {
		sent = m.InternalDate
	}
	return sender, sent
}

// crmFind is the CRM item of m: with a local record the recorded item
// (gone = nil; the GUID is never swapped for another item), without one
// the item with m's Message-ID that is m by sender and date (a forged
// Message-ID finds nothing).
func (b *Backend) crmFind(ctx context.Context, c *eway.Client, m store.Message, local *store.CrmLink) (*eway.Email, error) {
	if local != nil {
		e, err := c.GetEmail(ctx, local.ItemGUID)
		return e, toAPIErr(err)
	}
	if m.RFCMessageID == "" {
		return nil, nil
	}
	sender, sent := crmIdentity(m)
	e, err := c.FindEmail(ctx, m.RFCMessageID, sender, sent)
	return e, toAPIErr(err)
}

func toAPIErr(err error) error {
	if err == nil {
		return nil
	}
	return eway.ToAPIError(err)
}

// crmCheck asks the CRM whether m is saved there (crmFind) and brings the
// local record in step: written when saved (the first save time kept),
// deleted only when the recorded item is gone from the CRM, and
// notify.messagesChanged when what the list shows changed. nil = not
// saved.
func (b *Backend) crmCheck(ctx context.Context, crm store.Account, c *eway.Client, mail store.Account, m store.Message, local *store.CrmLink) (*api.CrmRecord, error) {
	key := store.CrmMessageKey(m)
	e, err := b.crmFind(ctx, c, m, local)
	if err != nil {
		return nil, err
	}
	if e == nil {
		if local != nil {
			if err := b.store.DeleteCrmLink(ctx, crm.ID, mail.ID, key); err != nil {
				return nil, api.NewError(api.CodeStorageError, "%v", err)
			}
			b.crmChanged(ctx, mail.ID, []string{key})
		}
		return nil, nil
	}
	_, rels, err := b.crmDescribe(ctx, crm, c, e)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	l := store.CrmLink{CrmAccountID: crm.ID, MailAccountID: mail.ID, MsgKey: key, ItemGUID: e.GUID,
		SavedAt: now, CheckedAt: now, Private: e.Private, Relations: rels}
	if local != nil {
		l.SavedAt, l.Auto = local.SavedAt, local.Auto
	}
	if err := b.store.UpsertCrmLink(ctx, l); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if local == nil {
		b.crmChanged(ctx, mail.ID, []string{key})
	}
	rec := crmRecord(crm, l)
	return &rec, nil
}

// crmChanged sends notify.messagesChanged for the folders holding the
// messages of the keys (coalesced with the other changes of messages).
func (b *Backend) crmChanged(ctx context.Context, mailID string, keys []string) {
	if len(keys) == 0 {
		return
	}
	folders, err := b.store.CrmFoldersOf(ctx, mailID, keys)
	if err != nil {
		b.log.Warn("crm: folders of saved messages", "err", err)
		folders = nil
	}
	if folders == nil {
		folders = []string{}
	}
	b.messagesChanged(map[string][]string{mailID: folders})
}

// localLink is the record of the CRM account among a key's links.
func localLink(links []store.CrmLink, crmID string) *store.CrmLink {
	for i := range links {
		if links[i].CrmAccountID == crmID {
			return &links[i]
		}
	}
	return nil
}

func keysOf(msgs []store.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = store.CrmMessageKey(m)
	}
	return out
}

// Lookup is crm.lookup: whether each message is saved (asked of the CRM,
// the local record brought in step) and what the messages' addresses lead
// to (D9).
func (s *crmService) Lookup(ctx context.Context, p api.CrmLookupParams) (*api.CrmLookupResult, error) {
	b := s.b
	crm, c, err := b.crmAccount(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	mail, msgs, err := b.crmMessages(ctx, p.MessageAccountID, p.MessageIDs)
	if err != nil {
		return nil, err
	}
	links, err := b.store.CrmLinksFor(ctx, mail.ID, keysOf(msgs))
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	res := &api.CrmLookupResult{AccountID: api.AccountID(crm.ID), Saved: make([]api.CrmSavedMessage, len(msgs)), Suggestions: []api.CrmItem{}}
	for i, m := range msgs {
		rec, err := b.crmCheck(ctx, crm, c, mail, m, localLink(links[store.CrmMessageKey(m)], crm.ID))
		if err != nil {
			return nil, err
		}
		res.Saved[i] = api.CrmSavedMessage{MessageID: api.MessageID(m.ID), Record: rec}
	}
	sug, err := c.Suggest(ctx, b.crmAddresses(ctx, msgs), eway.LookupBudget)
	if err != nil {
		return nil, eway.ToAPIError(err)
	}
	svc := crm.Config.Eway.ServiceURL
	for _, it := range sug.Items {
		res.Suggestions = append(res.Suggestions, crmItem(svc, it.Kind, it.GUID, it.Name, it.Detail, it.Reason, it.Superior))
	}
	res.Partial = sug.Partial
	b.log.Info("crm lookup", "messages", len(msgs), "suggestions", len(res.Suggestions), "partial", res.Partial)
	return res, nil
}

// Search is crm.search: items whose name (contacts: or e-mail) contains
// the query.
func (s *crmService) Search(ctx context.Context, p api.CrmSearchParams) (*api.CrmSearchResult, error) {
	q := strings.TrimSpace(p.Query)
	if len(q) < api.MinCrmQueryBytes || len(q) > api.MaxCrmQueryBytes || hasControl(q) {
		return nil, api.NewError(api.CodeInvalidArgument, "query: %d to %d bytes of text", api.MinCrmQueryBytes, api.MaxCrmQueryBytes)
	}
	kinds := p.Kinds
	if len(kinds) == 0 {
		kinds = []api.CrmItemKind{api.CrmContact, api.CrmCompany, api.CrmDeal, api.CrmProject}
	}
	seenKind := map[api.CrmItemKind]bool{}
	for _, k := range kinds {
		if eway.FolderOf(k) == "" || seenKind[k] {
			return nil, api.NewError(api.CodeInvalidArgument, "kinds: contact, company, deal or project, each once")
		}
		seenKind[k] = true
	}
	crm, c, err := s.b.crmAccount(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	res := &api.CrmSearchResult{Items: []api.CrmItem{}}
	svc := crm.Config.Eway.ServiceURL
	for _, k := range kinds {
		items, err := c.SearchByName(ctx, k, q, api.MaxCrmSearchResults+1)
		if err != nil {
			return nil, eway.ToAPIError(err)
		}
		for _, it := range items {
			if len(res.Items) >= api.MaxCrmSearchResults {
				res.Truncated = true
				break
			}
			res.Items = append(res.Items, crmItem(svc, it.Kind, it.GUID, it.Name, it.Detail, api.CrmReasonSearch, false))
		}
	}
	return res, nil
}

// crmRefs validates and cleans the item references of crm.save.
func crmRefs(field string, in []api.CrmItemRef) ([]eway.Ref, error) {
	out := make([]eway.Ref, 0, len(in))
	seen := map[string]bool{}
	for _, r := range in {
		if eway.FolderOf(r.Kind) == "" || !eway.ValidGUID(r.GUID) {
			return nil, api.NewError(api.CodeInvalidArgument, "%s: an item of kind contact, company, deal or project with a GUID", field)
		}
		g := strings.ToLower(r.GUID)
		if seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, eway.Ref{Kind: r.Kind, GUID: g})
	}
	return out, nil
}

// fatalCrm: an error that ends a whole crm.save (the sign-in, the
// keyring, cancellation), not one message.
func fatalCrm(e *api.Error) bool {
	switch e.Code {
	case api.CodeAuthFailed, api.CodeAuthRequired, api.CodeKeyringError, api.CodeCrmSignInUnsupported, api.CodeCancelled,
		api.CodeNetworkError, api.CodeTLSError:
		return true
	}
	return false
}

// Save is crm.save (D6–D8, D10, D18).
func (s *crmService) Save(ctx context.Context, p api.CrmSaveParams) (*api.CrmSaveResult, error) {
	b := s.b
	link, err := crmRefs("link", p.Link)
	if err != nil {
		return nil, err
	}
	unlink, err := crmRefs("unlink", p.Unlink)
	if err != nil {
		return nil, err
	}
	var superior *eway.Ref
	if p.Superior != nil {
		refs, err := crmRefs("superior", []api.CrmItemRef{*p.Superior})
		if err != nil {
			return nil, err
		}
		if k := refs[0].Kind; k != api.CrmDeal && k != api.CrmProject {
			return nil, api.NewError(api.CodeInvalidArgument, "superior: a deal or a project")
		}
		superior = &refs[0]
	}
	n := len(link) + len(unlink)
	if superior != nil {
		n++
	}
	if n > api.MaxCrmItems {
		return nil, api.NewError(api.CodeInvalidArgument, "at most %d items to link and unlink", api.MaxCrmItems)
	}
	if len(unlink) > 0 && len(p.MessageIDs) != 1 {
		return nil, api.NewError(api.CodeInvalidArgument, "unlink needs exactly one message")
	}
	crm, c, err := b.crmAccount(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	mail, msgs, err := b.crmMessages(ctx, p.MessageAccountID, p.MessageIDs)
	if err != nil {
		return nil, err
	}
	keys := keysOf(msgs)
	busy := make([]string, len(keys))
	for i, k := range keys {
		busy[i] = crmBusyKey(crm.ID, mail.ID, k)
	}
	if !b.crm.claim(busy) {
		return nil, api.NewError(api.CodeConflict, "a message is being saved or removed already")
	}
	defer b.crm.release(busy)

	// Every message whole on this device before anything goes out (D6),
	// decided without reading any: each is read right before its upload.
	sizes := make([]int64, len(msgs))
	var missing []api.MessageID
	for i, m := range msgs {
		n, ok := b.crmWholeSize(ctx, mail.ID, m)
		if !ok {
			missing = append(missing, api.MessageID(m.ID))
			continue
		}
		sizes[i] = n
	}
	if len(missing) > 0 {
		return nil, &api.Error{Code: api.CodePartNotDownloaded, Message: "messages are not whole on this device; message.download fetches them",
			Data: api.MessagesNotDownloadedData{MessageIDs: missing}}
	}
	// Every item to link exists, before anything goes out (an unknown
	// one is crmItemNotFound and nothing is saved). Items to unlink may be
	// gone already.
	check := append([]eway.Ref{}, link...)
	if superior != nil {
		check = append(check, *superior)
	}
	if err := c.CheckItems(ctx, check); err != nil {
		return nil, eway.ToAPIError(err)
	}
	links, err := b.store.CrmLinksFor(ctx, mail.ID, keys)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}

	res := &api.CrmSaveResult{Results: make([]api.CrmSaveOutcome, len(msgs))}
	var touched []string
	var stop *api.Error
	saved := 0
	for i, m := range msgs {
		out := &res.Results[i]
		out.MessageID = api.MessageID(m.ID)
		if stop != nil {
			out.Error = stop
			continue
		}
		if sizes[i] > ingest.MaxMessageBytes {
			out.Error = &api.Error{Code: api.CodeAttachmentTooBig, Message: "the message is larger than a CRM takes",
				Data: map[string]int64{"limit": ingest.MaxMessageBytes, "size": sizes[i]}}
			continue
		}
		rec, created, err := b.crmSaveOne(ctx, crm, c, mail, m, localLink(links[keys[i]], crm.ID),
			eway.Links{Link: link, Unlink: unlink, Superior: superior}, p.Private, false)
		if err != nil {
			if fatalCrm(err) {
				if saved == 0 {
					return nil, err
				}
				stop = err
			}
			out.Error = err
			continue
		}
		saved++
		out.Record, out.Created = rec, created
		touched = append(touched, keys[i])
	}
	if saved > 0 {
		c.ForgetCache() // what the addresses lead to may have changed
	}
	b.crmChanged(ctx, mail.ID, touched)
	b.log.Info("crm save", "messages", len(msgs), "saved", saved)
	return res, nil
}

// crmSaveOne saves one message and applies the links (D8, D10, D18):
// privacy is changed only on an item this call created or one the local
// record already knew (an edit), never on an item found that someone else
// saved.
func (b *Backend) crmSaveOne(ctx context.Context, crm store.Account, c *eway.Client, mail store.Account, m store.Message,
	local *store.CrmLink, links eway.Links, private, auto bool) (*api.CrmRecord, bool, *api.Error) {
	// A recorded item still in the CRM is the saved message; a recorded
	// item gone is forgotten and the message saved anew.
	var existing *eway.Email
	if local != nil {
		e, err := c.GetEmail(ctx, local.ItemGUID)
		if err != nil {
			return nil, false, eway.ToAPIError(err)
		}
		if e == nil {
			if err := b.store.DeleteCrmLink(ctx, crm.ID, mail.ID, local.MsgKey); err != nil {
				return nil, false, api.NewError(api.CodeStorageError, "%v", err)
			}
			local = nil
		}
		existing = e
	}
	var raw []byte
	if existing == nil {
		var ok bool
		if raw, ok = b.crmWhole(ctx, mail.ID, m); !ok {
			return nil, false, api.NewError(api.CodePartNotDownloaded, "the message is no longer whole on this device")
		}
	}
	if len(raw) > ingest.MaxMessageBytes {
		return nil, false, &api.Error{Code: api.CodeAttachmentTooBig, Message: "the message is larger than a CRM takes",
			Data: map[string]int64{"limit": ingest.MaxMessageBytes, "size": int64(len(raw))}}
	}
	text, _, _, err := b.store.GetMessageText(ctx, mail.ID, m.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, api.NewError(api.CodeStorageError, "%v", err)
	}
	attachments := 0
	for _, a := range m.Attachments {
		if !a.Inline {
			attachments++
		}
	}
	sender, sent := crmIdentity(m)
	addrs := func(list []api.Address) []string {
		out := make([]string, 0, len(list))
		for _, a := range list {
			out = append(out, a.Address)
		}
		return out
	}
	subject := cleanSubject(m.Subject)
	msg := eway.Message{
		Raw:         raw,
		FileName:    safename.Filename(strings.TrimSpace(subject) + ".eml"),
		MessageID:   m.RFCMessageID,
		Subject:     subject,
		Sender:      sender,
		To:          addrs(m.To),
		Cc:          addrs(m.CC),
		SentOn:      sent,
		Received:    m.InternalDate,
		Note:        calendar.CleanBlock(text, api.MaxCrmNoteBytes),
		Attachments: attachments,
		Private:     private,
		Superior:    links.Superior,
	}
	if msg.Received.IsZero() {
		msg.Received = sent
	}
	var s eway.Saved
	if existing != nil {
		s = eway.Saved{GUID: existing.GUID}
	} else {
		var err error
		if s, err = c.SaveMessage(ctx, msg); err != nil {
			return nil, false, eway.ToAPIError(err)
		}
	}
	raw, msg.Raw = nil, nil // dropped before the links are written
	e, err := c.GetEmail(ctx, s.GUID)
	if err != nil {
		return nil, false, eway.ToAPIError(err)
	}
	if e == nil {
		return nil, false, api.NewError(api.CodeServerError, "eway: the saved message cannot be read back")
	}
	if err := c.ApplyLinks(ctx, e, links); err != nil {
		return nil, s.Created, eway.ToAPIError(err)
	}
	if !s.Created && local != nil && e.Private != private {
		if err := c.SetPrivate(ctx, e.GUID, private); err != nil {
			return nil, false, eway.ToAPIError(err)
		}
	}
	if e, err = c.GetEmail(ctx, s.GUID); err != nil || e == nil {
		if err == nil {
			err = api.NewError(api.CodeServerError, "eway: the saved message cannot be read back")
		}
		return nil, s.Created, eway.ToAPIError(err)
	}
	_, rels, aerr := b.crmDescribe(ctx, crm, c, e)
	if aerr != nil {
		return nil, s.Created, eway.ToAPIError(aerr)
	}
	now := time.Now().UTC().Truncate(time.Second)
	l := store.CrmLink{CrmAccountID: crm.ID, MailAccountID: mail.ID, MsgKey: store.CrmMessageKey(m), ItemGUID: e.GUID,
		SavedAt: now, CheckedAt: now, Private: e.Private, Relations: rels, Auto: auto}
	if local != nil {
		l.SavedAt = local.SavedAt
	}
	if err := b.store.UpsertCrmLink(ctx, l); err != nil {
		return nil, s.Created, api.NewError(api.CodeStorageError, "%v", err)
	}
	rec := crmRecord(crm, l)
	return &rec, s.Created, nil
}

// Remove is crm.remove: the e-mail item deleted from the CRM with its
// relations (for every user of the CRM; the client asked first), then the
// local record.
func (s *crmService) Remove(ctx context.Context, p api.CrmRemoveParams) (*api.CrmRemoveResult, error) {
	b := s.b
	if p.MessageID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "messageId is required")
	}
	crm, c, err := b.crmAccount(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	mail, msgs, err := b.crmMessages(ctx, p.MessageAccountID, []api.MessageID{p.MessageID})
	if err != nil {
		return nil, err
	}
	m := msgs[0]
	key := store.CrmMessageKey(m)
	busy := []string{crmBusyKey(crm.ID, mail.ID, key)}
	if !b.crm.claim(busy) {
		return nil, api.NewError(api.CodeConflict, "the message is being saved or removed already")
	}
	defer b.crm.release(busy)
	links, err := b.store.CrmLinksFor(ctx, mail.ID, []string{key})
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	local := localLink(links[key], crm.ID)
	// Only the recorded item, or without a record the item that is this
	// message by sender and date: a forged Message-ID deletes nothing.
	e, err := b.crmFind(ctx, c, m, local)
	if err != nil {
		return nil, err
	}
	if e != nil {
		if err := c.Delete(ctx, e.GUID); err != nil {
			return nil, eway.ToAPIError(err)
		}
	}
	if local != nil {
		if err := b.store.DeleteCrmLink(ctx, crm.ID, mail.ID, key); err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		b.crmChanged(ctx, mail.ID, []string{key})
	}
	if e == nil {
		return nil, api.NewError(api.CodeCrmItemNotFound, "the message is not saved in the CRM")
	}
	b.log.Info("crm remove")
	return &api.CrmRemoveResult{}, nil
}

// crmRecordsOf is Message.Crm of message.get: the local records of the
// message in enabled CRM accounts; a record older than crmStaleAfter is
// checked again in the background (gone → its row deleted and
// notify.messagesChanged).
func (b *Backend) crmRecordsOf(ctx context.Context, mail store.Account, m store.Message) []api.CrmRecord {
	if hasNoMail(mail.Config) || isIssueAccount(mail) {
		return nil
	}
	key := store.CrmMessageKey(m)
	links, err := b.store.CrmLinksFor(ctx, mail.ID, []string{key})
	if err != nil || len(links[key]) == 0 {
		return nil
	}
	var out []api.CrmRecord
	for _, l := range links[key] {
		crm, err := b.store.GetAccount(ctx, l.CrmAccountID)
		if err != nil || !isCrmAccount(crm) || !crm.Enabled || crm.Config.Eway == nil {
			continue
		}
		out = append(out, crmRecord(crm, l))
		if time.Since(l.CheckedAt) > crmStaleAfter {
			b.crmRecheck(crm, mail, m, l)
		}
	}
	return out
}

// crmRecheckBackoff is how long a failed background check waits before
// message.get tries again.
const crmRecheckBackoff = time.Hour

// crmRecheck checks one stale record in the background: never beside a
// save or removal of the message (it claims the same key, and skips when
// busy), cancelled with the account's client (paused, removed, daemon
// stopping), and after a failure not again for crmRecheckBackoff.
func (b *Backend) crmRecheck(crm, mail store.Account, m store.Message, l store.CrmLink) {
	key := []string{crmBusyKey(crm.ID, mail.ID, l.MsgKey)}
	c, err := b.crmClient(crm)
	if err != nil || !b.crm.claim(key) {
		return
	}
	go func() {
		defer b.crm.release(key)
		ctx, cancel := context.WithTimeout(c.Context(), crmCheckTimeout)
		defer cancel()
		if _, err := b.crmCheck(ctx, crm, c, mail, m, &l); err != nil {
			code := api.CodeServerError
			var ae *api.Error
			if errors.As(err, &ae) {
				code = ae.Code
			}
			b.log.Debug("crm record not checked", "code", code.String())
			if c.Context().Err() == nil {
				at := time.Now().Add(crmRecheckBackoff - crmStaleAfter)
				if err := b.store.MarkCrmChecked(context.Background(), crm.ID, mail.ID, l.MsgKey, at); err != nil {
					b.log.Warn("crm record backoff", "err", err)
				}
			}
		}
	}()
}
