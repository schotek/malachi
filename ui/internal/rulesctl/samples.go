// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package rulesctl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/rules"
)

// The invented rules of MALACHI_RULES_SAMPLES=1 (like
// MALACHI_CONTACTS_SAMPLES): an in-memory daemon that answers rules.* and
// the account.list and folder.list the Rules page and the editor read, so
// that the page and the editor can be seen and clicked through without a
// daemon and the smoke tests have something to show. Writes change the
// memory only. Every name and address is invented. Other methods answer
// methodNotFound.

// SamplesEnv turns the samples on ("1").
const SamplesEnv = "MALACHI_RULES_SAMPLES"

// SamplesOn reports MALACHI_RULES_SAMPLES=1.
func SamplesOn() bool { return os.Getenv(SamplesEnv) == "1" }

// SampleCaller is the in-memory daemon of the samples; safe for calls
// from any goroutine.
type SampleCaller struct {
	mu       sync.Mutex
	accounts []api.Account
	folders  map[api.AccountID][]api.Folder
	rules    []api.Rule
	next     int
	// Notify, when set, gets notify.rulesChanged after a write (on the
	// caller's goroutine).
	Notify func(method string, params json.RawMessage)
}

// NewSampleCaller is the sample daemon as of now.
func NewSampleCaller(now time.Time) *SampleCaller {
	s := &SampleCaller{folders: map[api.AccountID][]api.Folder{}}
	s.accounts = []api.Account{
		{ID: "acc_work", Enabled: true, Capabilities: api.MailCapabilities,
			Config: api.AccountConfig{Name: "Práce", Email: "jana@firma.cz", Kind: api.AccountGraph}},
		{ID: "acc_home", Enabled: true, Capabilities: api.MailCapabilities,
			Config: api.AccountConfig{Name: "Doma", Email: "jana@example.org"}},
	}
	for _, a := range s.accounts {
		id := string(a.ID)
		mk := func(fid, name string, role api.FolderRole) api.Folder {
			return api.Folder{ID: api.FolderID(id + "/" + fid), AccountID: a.ID, Name: name, Path: name,
				Role: role, Subscribed: true, Selectable: true, Synced: true, Total: 40}
		}
		s.folders[a.ID] = []api.Folder{
			mk("inbox", "INBOX", api.RoleInbox), mk("sent", "Sent", api.RoleSent), mk("drafts", "Drafts", api.RoleDrafts),
			mk("archive", "Archive", api.RoleArchive), mk("junk", "Junk", api.RoleJunk), mk("trash", "Trash", api.RoleTrash),
			mk("news", "Newsletters", api.RoleNone), mk("invoices", "Invoices", api.RoleNone), mk("outbox", "Outbox", api.RoleOutbox),
		}
	}
	at := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	mk := func(name, account string, match api.RuleMatch, stop, enabled bool, conds []api.RuleCondition, acts []api.RuleAction) api.Rule {
		s.next++
		in := api.RuleInput{Name: name, Enabled: enabled, AccountID: api.AccountID(account), Match: match,
			Conditions: conds, Actions: acts, Stop: stop}
		return api.Rule{ID: fmt.Sprintf("r_%032d", s.next), RuleInput: in, Outgoing: rules.IsOutgoing(in),
			CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-24 * time.Hour)}
	}
	news := mk("Newsletters", "acc_work", api.RuleMatchAny, false, true,
		[]api.RuleCondition{
			{Field: api.RuleFieldBulk, Op: api.RuleOpIs, Value: string(api.BulkNewsletter)},
			{Field: api.RuleFieldListID, Op: api.RuleOpContains, Value: "news.example.com"},
		},
		[]api.RuleAction{{Kind: api.RuleActionMarkRead}, {Kind: api.RuleActionMove, FolderID: "acc_work/news"}})
	news.Applied, news.LastAppliedAt = 31, at(2*time.Hour)
	invoices := mk("Invoices to accounting", "acc_work", api.RuleMatchAll, true, true,
		[]api.RuleCondition{
			{Field: api.RuleFieldFrom, Op: api.RuleOpDomainIs, Value: "dodavatel.example.com"},
			{Field: api.RuleFieldHasAttachments, Op: api.RuleOpIs, Value: rules.ValueTrue},
		},
		[]api.RuleAction{{Kind: api.RuleActionFlag}, {Kind: api.RuleActionForward, To: "ucetni@firma.cz"}})
	invoices.Applied, invoices.LastAppliedAt, invoices.Skipped = 4, at(26*time.Hour), 1
	away := mk("Out of office", "", api.RuleMatchAll, false, false,
		[]api.RuleCondition{{Field: api.RuleFieldToOrCc, Op: api.RuleOpContains, Value: "jana@firma.cz"}},
		[]api.RuleAction{{Kind: api.RuleActionAutoReply, Subject: "Mimo kancelář", Text: "Jsem mimo kancelář, odpovím po návratu."}})
	away.Problem = api.RuleProblemSendFailed
	gone := mk("Projekt Alfa", "acc_home", api.RuleMatchAll, false, false,
		[]api.RuleCondition{{Field: api.RuleFieldSubject, Op: api.RuleOpStartsWith, Value: "[Alfa]"}},
		[]api.RuleAction{{Kind: api.RuleActionMove, FolderID: "acc_home/removed"}})
	gone.Problem = api.RuleProblemFolderGone
	s.rules = []api.Rule{news, invoices, away, gone}
	return s
}

// Call implements board.Caller.
func (s *SampleCaller) Call(_ context.Context, method string, params any, result any) error {
	raw, _ := json.Marshal(params)
	s.mu.Lock()
	v, notify, err := s.handle(method, raw)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if notify && s.Notify != nil {
		s.Notify(api.NotifyRulesChanged, json.RawMessage(`{}`))
	}
	if result == nil || v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return json.Unmarshal(b, result)
}

func (s *SampleCaller) find(id string) int {
	return slices.IndexFunc(s.rules, func(r api.Rule) bool { return r.ID == id })
}

func (s *SampleCaller) handle(method string, raw json.RawMessage) (v any, notify bool, err error) {
	bad := &api.Error{Code: api.CodeInvalidArgument, Message: "bad params"}
	switch method {
	case api.MethodAccountList:
		return api.AccountListResult{Accounts: s.accounts}, false, nil
	case api.MethodFolderList:
		var p api.FolderListParams
		if json.Unmarshal(raw, &p) != nil {
			return nil, false, bad
		}
		return api.FolderListResult{Folders: s.folders[p.AccountID]}, false, nil
	case api.MethodRulesList:
		return api.RulesListResult{Rules: s.rules}, false, nil
	case api.MethodRulesCreate:
		var p api.RulesCreateParams
		if json.Unmarshal(raw, &p) != nil {
			return nil, false, bad
		}
		if rules.IsOutgoing(p.Rule) && p.Rule.Enabled && !p.ConfirmOutgoing {
			return nil, false, &api.Error{Code: api.CodeInvalidArgument, Message: "confirm",
				Data: api.RuleInvalidData{Field: "confirmOutgoing"}}
		}
		s.next++
		r := api.Rule{ID: fmt.Sprintf("r_%032d", s.next), RuleInput: p.Rule, Outgoing: rules.IsOutgoing(p.Rule),
			CreatedAt: time.Now(), UpdatedAt: time.Now()}
		s.rules = append(s.rules, r)
		return api.RulesCreateResult{Rule: r}, true, nil
	case api.MethodRulesUpdate:
		var p api.RulesUpdateParams
		if json.Unmarshal(raw, &p) != nil {
			return nil, false, bad
		}
		i := s.find(p.ID)
		if i < 0 {
			return nil, false, &api.Error{Code: api.CodeRuleNotFound, Message: "no such rule"}
		}
		if rules.NeedsConfirm(&s.rules[i], p.Rule) && !p.ConfirmOutgoing {
			return nil, false, &api.Error{Code: api.CodeInvalidArgument, Message: "confirm",
				Data: api.RuleInvalidData{Field: "confirmOutgoing"}}
		}
		s.rules[i].RuleInput, s.rules[i].Outgoing, s.rules[i].UpdatedAt = p.Rule, rules.IsOutgoing(p.Rule), time.Now()
		if p.Rule.Enabled {
			s.rules[i].Problem = ""
		}
		return api.RulesUpdateResult{Rule: s.rules[i]}, true, nil
	case api.MethodRulesDelete:
		var p api.RulesDeleteParams
		if json.Unmarshal(raw, &p) != nil {
			return nil, false, bad
		}
		i := s.find(p.ID)
		if i < 0 {
			return nil, false, &api.Error{Code: api.CodeRuleNotFound, Message: "no such rule"}
		}
		s.rules = slices.Delete(s.rules, i, i+1)
		return api.RulesDeleteResult{}, true, nil
	case api.MethodRulesReorder:
		var p api.RulesReorderParams
		if json.Unmarshal(raw, &p) != nil || len(p.IDs) != len(s.rules) {
			return nil, false, bad
		}
		out := make([]api.Rule, 0, len(p.IDs))
		for _, id := range p.IDs {
			i := s.find(id)
			if i < 0 {
				return nil, false, &api.Error{Code: api.CodeRuleNotFound, Message: "no such rule"}
			}
			out = append(out, s.rules[i])
		}
		s.rules = out
		return api.RulesReorderResult{Rules: s.rules}, true, nil
	case api.MethodRulesTest:
		return api.RulesTestResult{Count: 3}, false, nil
	case api.MethodRulesApply:
		return api.RulesApplyResult{Examined: 40, Matched: 5, Changed: 5}, false, nil
	}
	return nil, false, &api.Error{Code: api.CodeMethodNotFound, Message: method}
}
