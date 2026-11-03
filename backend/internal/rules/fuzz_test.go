// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rules

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// FuzzRules feeds arbitrary values to the validation and the matcher:
// nothing panics, an accepted rule stays accepted when normalised again
// (Normalize is idempotent) and every string it keeps is clean.
func FuzzRules(f *testing.F) {
	f.Add("name", "from", "contains", "x@y.z", "Alice", "a@b.c", "subject", "body", "move", "f_1", "away")
	f.Add("‮", "body", "notContains", "\x00", "\xff", "@", "", "", "autoReply", "", "a\r\nb")
	f.Add("n", "bulk", "is", "any", "", "", "", "", "forward", "a@b.c", "")
	f.Add("n", "toOrCc", "domainIs", "@ex.ample.", "B", "x@sub.ex.ample", "s", "b", "trash", "", "")
	f.Fuzz(func(t *testing.T, name, field, op, value, fromName, fromAddr, subject, body, kind, target, text string) {
		in := api.RuleInput{
			Name: name, Enabled: true, AccountID: "acc_1", Match: api.RuleMatchAny,
			Conditions: []api.RuleCondition{{Field: api.RuleField(field), Op: api.RuleOperator(op), Value: value}},
			Actions:    []api.RuleAction{{Kind: api.RuleActionKind(kind), FolderID: api.FolderID(target), To: target, Subject: name, Text: text}},
		}
		m := Message{
			From: []api.Address{{Name: fromName, Address: fromAddr}}, To: []api.Address{{Address: target}},
			Subject: subject, Body: body, HasBody: true, ListID: value, Bulk: api.BulkKind(value),
		}
		Matches(in, m)
		steps := Plan([]api.Rule{{ID: "r", RuleInput: in}, {ID: "s", RuleInput: in}}, m)
		EffectsOf(steps)
		out, err := Normalize(in)
		if err != nil {
			return
		}
		again, err := Normalize(out)
		if err != nil {
			t.Fatalf("a normalised rule is refused: %v", err)
		}
		if again.Name != out.Name || again.Conditions[0] != out.Conditions[0] || again.Actions[0] != out.Actions[0] {
			t.Fatalf("Normalize is not idempotent: %+v / %+v", out, again)
		}
		if !Clean(out.Name) || !Clean(out.Conditions[0].Value) || !Clean(out.Actions[0].To) || !Clean(out.Actions[0].Subject) {
			t.Fatalf("an accepted rule keeps an unclean string: %+v", out)
		}
		Matches(out, m)
	})
}
