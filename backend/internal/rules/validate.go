// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rules

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// InvalidError is a rule the daemon refuses: Data is error.data of the
// invalidArgument (docs/api.md §4.21), Reason the technical message.
type InvalidError struct {
	Data   api.RuleInvalidData
	Reason string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("invalid rule %s[%d]: %s", e.Data.Field, e.Data.Index, e.Reason)
}

func invalid(field string, index int, format string, args ...any) *InvalidError {
	return &InvalidError{Data: api.RuleInvalidData{Field: field, Index: index}, Reason: fmt.Sprintf(format, args...)}
}

// bulkValues are the values of a bulk condition.
var bulkValues = map[string]bool{
	api.RuleBulkAny: true, string(api.BulkNewsletter): true, string(api.BulkList): true, string(api.BulkAutomated): true,
}

// textOps are the operators of a text field; address fields add
// domainIs.
var textOps = map[api.RuleOperator]bool{
	api.RuleOpContains: true, api.RuleOpNotContains: true, api.RuleOpIs: true,
	api.RuleOpIsNot: true, api.RuleOpStartsWith: true, api.RuleOpEndsWith: true,
}

// IsAddressField reports whether the field holds addresses.
func IsAddressField(f api.RuleField) bool {
	switch f {
	case api.RuleFieldFrom, api.RuleFieldTo, api.RuleFieldCc, api.RuleFieldToOrCc:
		return true
	}
	return false
}

// Normalize checks a rule as the user edited it and returns it cleaned:
// strings trimmed (the reply text only at its end, its line breaks as
// LF), the fields of other action kinds cleared, an empty match as
// "all". The checks are those of the clients' Validate
// (ui/internal/rules) and the limits of pkg/api; what the store has to
// say (the account, the folder) is the caller's.
func Normalize(in api.RuleInput) (api.RuleInput, *InvalidError) {
	out := api.RuleInput{
		Name:      strings.TrimSpace(in.Name),
		Enabled:   in.Enabled,
		AccountID: api.AccountID(strings.TrimSpace(string(in.AccountID))),
		Match:     in.Match,
		Stop:      in.Stop,
	}
	switch {
	case out.Name == "":
		return out, invalid("name", 0, "name is empty")
	case len(out.Name) > api.MaxRuleNameBytes:
		return out, invalid("name", 0, "name is longer than %d bytes", api.MaxRuleNameBytes)
	case !Clean(out.Name):
		return out, invalid("name", 0, "name has control, format or line-breaking characters")
	}
	if !Clean(string(out.AccountID)) || len(out.AccountID) > api.MaxRuleValueBytes {
		return out, invalid("accountId", 0, "accountId is not usable")
	}
	switch out.Match {
	case "":
		out.Match = api.RuleMatchAll
	case api.RuleMatchAll, api.RuleMatchAny:
	default:
		return out, invalid("conditions", 0, "match must be all or any")
	}

	switch {
	case len(in.Conditions) == 0:
		return out, invalid("conditions", 0, "a rule needs a condition")
	case len(in.Conditions) > api.MaxRuleConditions:
		return out, invalid("conditions", 0, "more than %d conditions", api.MaxRuleConditions)
	}
	out.Conditions = make([]api.RuleCondition, 0, len(in.Conditions))
	for i, c := range in.Conditions {
		nc, err := normalizeCondition(c)
		if err != nil {
			err.Data.Index = i
			return out, err
		}
		out.Conditions = append(out.Conditions, nc)
	}

	switch {
	case len(in.Actions) == 0:
		return out, invalid("actions", 0, "a rule needs an action")
	case len(in.Actions) > api.MaxRuleActions:
		return out, invalid("actions", 0, "more than %d actions", api.MaxRuleActions)
	}
	seen := map[api.RuleActionKind]bool{}
	placed := false
	out.Actions = make([]api.RuleAction, 0, len(in.Actions))
	for i, a := range in.Actions {
		if seen[a.Kind] {
			return out, invalid("actions", i, "action %q twice", a.Kind)
		}
		seen[a.Kind] = true
		if IsPlacing(a.Kind) {
			if placed {
				return out, invalid("actions", i, "only one of move, junk and trash")
			}
			placed = true
		}
		na, err := normalizeAction(a, out.AccountID)
		if err != nil {
			err.Data.Index = i
			return out, err
		}
		out.Actions = append(out.Actions, na)
	}
	return out, nil
}

func normalizeCondition(c api.RuleCondition) (api.RuleCondition, *InvalidError) {
	out := api.RuleCondition{Field: c.Field, Op: c.Op, Value: c.Value}
	switch c.Field {
	case api.RuleFieldBulk:
		if (c.Op != api.RuleOpIs && c.Op != api.RuleOpIsNot) || !bulkValues[c.Value] {
			return out, invalid("conditions", 0, "a bulk condition takes is or isNot with any or a bulk kind")
		}
		return out, nil
	case api.RuleFieldHasAttachments:
		if c.Op != api.RuleOpIs || (c.Value != "true" && c.Value != "false") {
			return out, invalid("conditions", 0, "hasAttachments takes is with true or false")
		}
		return out, nil
	case api.RuleFieldFrom, api.RuleFieldTo, api.RuleFieldCc, api.RuleFieldToOrCc,
		api.RuleFieldSubject, api.RuleFieldBody, api.RuleFieldListID:
	default:
		return out, invalid("conditions", 0, "unknown field")
	}
	if !textOps[c.Op] && (c.Op != api.RuleOpDomainIs || !IsAddressField(c.Field)) {
		return out, invalid("conditions", 0, "operator not for this field")
	}
	out.Value = strings.TrimSpace(c.Value)
	switch {
	case out.Value == "":
		return out, invalid("conditions", 0, "value is empty")
	case len(out.Value) > api.MaxRuleValueBytes:
		return out, invalid("conditions", 0, "value is longer than %d bytes", api.MaxRuleValueBytes)
	case !Clean(out.Value):
		return out, invalid("conditions", 0, "value has control, format or line-breaking characters")
	}
	if c.Op == api.RuleOpDomainIs && strings.Trim(out.Value, "@.") == "" {
		return out, invalid("conditions", 0, "domain is empty")
	}
	return out, nil
}

func normalizeAction(a api.RuleAction, account api.AccountID) (api.RuleAction, *InvalidError) {
	out := api.RuleAction{Kind: a.Kind}
	switch a.Kind {
	case api.RuleActionMarkRead, api.RuleActionFlag, api.RuleActionTrash, api.RuleActionJunk:
	case api.RuleActionMove:
		out.FolderID = api.FolderID(strings.TrimSpace(string(a.FolderID)))
		switch {
		case account == "":
			return out, invalid("actions", 0, "move needs a rule for one account")
		case out.FolderID == "" || len(out.FolderID) > api.MaxRuleValueBytes || !Clean(string(out.FolderID)):
			return out, invalid("actions", 0, "move needs a folder")
		}
	case api.RuleActionForward:
		out.To = strings.TrimSpace(a.To)
		if len(out.To) > api.MaxRuleValueBytes || !ValidAddress(out.To) {
			return out, invalid("actions", 0, "forward needs one plain address")
		}
	case api.RuleActionAutoReply:
		out.Subject = strings.TrimSpace(a.Subject)
		out.Text = strings.TrimRight(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(a.Text), " \t\n")
		switch {
		case strings.TrimSpace(out.Text) == "":
			return out, invalid("actions", 0, "autoReply needs a text")
		case len(out.Text) > api.MaxRuleReplyBytes:
			return out, invalid("actions", 0, "text is longer than %d bytes", api.MaxRuleReplyBytes)
		case len(out.Subject) > api.MaxRuleValueBytes:
			return out, invalid("actions", 0, "subject is longer than %d bytes", api.MaxRuleValueBytes)
		case !Clean(out.Subject) || !cleanText(out.Text):
			return out, invalid("actions", 0, "subject or text has control, format or line-breaking characters")
		}
	default:
		return out, invalid("actions", 0, "unknown action")
	}
	return out, nil
}

// Clean reports whether a one-line value is valid UTF-8 without control,
// format (bidi, invisible) or line-breaking characters: the rule of
// every string of a rule (ui/internal/rules Clean).
func Clean(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' {
			return false
		}
	}
	return true
}

// cleanText is Clean that lets line feeds and tabs through (the text of
// an automatic reply, its line breaks already LF).
func cleanText(s string) bool {
	return Clean(strings.NewReplacer("\n", "", "\t", "").Replace(s))
}

// ValidAddress is a bare address forward accepts: local@domain, no
// spaces, angle brackets, commas, quotes or control characters, a dot in
// the domain (ui/internal/rules ValidAddress).
func ValidAddress(s string) bool {
	if s == "" || len(s) > 254 || !Clean(s) || strings.ContainsAny(s, " <>,;:\"()[]\\") {
		return false
	}
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 || strings.Count(s, "@") != 1 {
		return false
	}
	domain := s[at+1:]
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") &&
		!strings.HasSuffix(domain, ".") && !strings.Contains(domain, "..")
}
