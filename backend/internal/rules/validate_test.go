// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package rules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func valid() api.RuleInput {
	return api.RuleInput{
		Name: "  Newsletters ", Enabled: true, AccountID: "acc_1",
		Conditions: []api.RuleCondition{{Field: api.RuleFieldFrom, Op: api.RuleOpDomainIs, Value: " news.example "}},
		Actions: []api.RuleAction{
			{Kind: api.RuleActionMove, FolderID: "f_1", To: "leak@x.y", Text: "leak"},
			{Kind: api.RuleActionAutoReply, Subject: " Away ", Text: "Gone.\r\nBack Monday. \r\n\r\n"},
		},
	}
}

func TestNormalizeCleans(t *testing.T) {
	out, err := Normalize(valid())
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "Newsletters" || out.Match != api.RuleMatchAll || out.Conditions[0].Value != "news.example" {
		t.Errorf("%+v", out)
	}
	if out.Actions[0] != (api.RuleAction{Kind: api.RuleActionMove, FolderID: "f_1"}) {
		t.Errorf("move keeps only its folder: %+v", out.Actions[0])
	}
	if out.Actions[1].Subject != "Away" || out.Actions[1].Text != "Gone.\nBack Monday." {
		t.Errorf("reply %+v", out.Actions[1])
	}
}

func TestNormalizeRefuses(t *testing.T) {
	type mut func(*api.RuleInput)
	long := strings.Repeat("a", api.MaxRuleValueBytes+1)
	cases := []struct {
		name  string
		mut   mut
		field string
		index int
	}{
		{"empty name", func(r *api.RuleInput) { r.Name = " \t " }, "name", 0},
		{"long name", func(r *api.RuleInput) { r.Name = strings.Repeat("é", api.MaxRuleNameBytes) }, "name", 0},
		{"NUL in name", func(r *api.RuleInput) { r.Name = "a\x00b" }, "name", 0},
		{"bidi in name", func(r *api.RuleInput) { r.Name = "a‮b" }, "name", 0},
		{"zero width in name", func(r *api.RuleInput) { r.Name = "a​b" }, "name", 0},
		{"line separator in name", func(r *api.RuleInput) { r.Name = "a b" }, "name", 0},
		{"invalid UTF-8 name", func(r *api.RuleInput) { r.Name = "a\xffb" }, "name", 0},
		{"control in account", func(r *api.RuleInput) { r.AccountID = "a\nb" }, "accountId", 0},
		{"bad match", func(r *api.RuleInput) { r.Match = "some" }, "conditions", 0},
		{"no condition", func(r *api.RuleInput) { r.Conditions = nil }, "conditions", 0},
		{"too many conditions", func(r *api.RuleInput) {
			for len(r.Conditions) <= api.MaxRuleConditions {
				r.Conditions = append(r.Conditions, r.Conditions[0])
			}
		}, "conditions", 0},
		{"unknown field", func(r *api.RuleInput) {
			r.Conditions = append(r.Conditions, api.RuleCondition{Field: "header", Op: api.RuleOpIs, Value: "x"})
		}, "conditions", 1},
		{"domainIs on subject", func(r *api.RuleInput) { r.Conditions[0].Field = api.RuleFieldSubject }, "conditions", 0},
		{"empty value", func(r *api.RuleInput) { r.Conditions[0].Value = "  " }, "conditions", 0},
		{"long value", func(r *api.RuleInput) { r.Conditions[0].Value = long }, "conditions", 0},
		{"NUL value", func(r *api.RuleInput) { r.Conditions[0].Value = "x\x00" }, "conditions", 0},
		{"empty domain", func(r *api.RuleInput) { r.Conditions[0].Value = "@." }, "conditions", 0},
		{"bulk contains", func(r *api.RuleInput) {
			r.Conditions[0] = api.RuleCondition{Field: api.RuleFieldBulk, Op: api.RuleOpContains, Value: "any"}
		}, "conditions", 0},
		{"bulk value", func(r *api.RuleInput) {
			r.Conditions[0] = api.RuleCondition{Field: api.RuleFieldBulk, Op: api.RuleOpIs, Value: "spam"}
		}, "conditions", 0},
		{"attachments value", func(r *api.RuleInput) {
			r.Conditions[0] = api.RuleCondition{Field: api.RuleFieldHasAttachments, Op: api.RuleOpIs, Value: "1"}
		}, "conditions", 0},
		{"no action", func(r *api.RuleInput) { r.Actions = nil }, "actions", 0},
		{"too many actions", func(r *api.RuleInput) {
			for len(r.Actions) <= api.MaxRuleActions {
				r.Actions = append(r.Actions, api.RuleAction{Kind: api.RuleActionFlag})
			}
		}, "actions", 0},
		{"kind twice", func(r *api.RuleInput) {
			r.Actions = append(r.Actions, api.RuleAction{Kind: api.RuleActionAutoReply, Text: "x"})
		}, "actions", 2},
		{"two places", func(r *api.RuleInput) { r.Actions = append(r.Actions, api.RuleAction{Kind: api.RuleActionTrash}) }, "actions", 2},
		{"move without account", func(r *api.RuleInput) { r.AccountID = "" }, "actions", 0},
		{"move without folder", func(r *api.RuleInput) { r.Actions[0].FolderID = "" }, "actions", 0},
		{"unknown action", func(r *api.RuleInput) { r.Actions[0] = api.RuleAction{Kind: "delete"} }, "actions", 0},
		{"forward two addresses", func(r *api.RuleInput) {
			r.Actions[0] = api.RuleAction{Kind: api.RuleActionForward, To: "a@b.c, d@e.f"}
		}, "actions", 0},
		{"forward header injection", func(r *api.RuleInput) {
			r.Actions[0] = api.RuleAction{Kind: api.RuleActionForward, To: "a@b.c\r\nBcc: x@y.z"}
		}, "actions", 0},
		{"forward with name", func(r *api.RuleInput) {
			r.Actions[0] = api.RuleAction{Kind: api.RuleActionForward, To: "A <a@b.c>"}
		}, "actions", 0},
		{"forward no domain dot", func(r *api.RuleInput) { r.Actions[0] = api.RuleAction{Kind: api.RuleActionForward, To: "a@b"} }, "actions", 0},
		{"reply empty", func(r *api.RuleInput) { r.Actions[1].Text = " \n\t" }, "actions", 1},
		{"reply long", func(r *api.RuleInput) { r.Actions[1].Text = strings.Repeat("a", api.MaxRuleReplyBytes+1) }, "actions", 1},
		{"reply subject newline", func(r *api.RuleInput) { r.Actions[1].Subject = "a\nBcc: x@y.z" }, "actions", 1},
		{"reply text bidi", func(r *api.RuleInput) { r.Actions[1].Text = "ok⁦hidden" }, "actions", 1},
		{"reply text NUL", func(r *api.RuleInput) { r.Actions[1].Text = "ok\x00" }, "actions", 1},
	}
	for _, c := range cases {
		in := valid()
		in.Conditions = append([]api.RuleCondition(nil), in.Conditions...)
		in.Actions = append([]api.RuleAction(nil), in.Actions...)
		c.mut(&in)
		_, err := Normalize(in)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if err.Data.Field != c.field || err.Data.Index != c.index {
			t.Errorf("%s: %+v, want %s[%d]", c.name, err.Data, c.field, c.index)
		}
	}
}

// TestNormalizeTestdata runs the pathological rules of
// backend/testdata/rules: each must be refused, never accepted or crash.
func TestNormalizeTestdata(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "rules", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no test data: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var cases []struct {
			Name  string        `json:"name"`
			Rule  api.RuleInput `json:"rule"`
			Field string        `json:"field"`
		}
		if err := json.Unmarshal(data, &cases); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, c := range cases {
			_, err := Normalize(c.Rule)
			if err == nil {
				t.Errorf("%s %s: accepted", filepath.Base(f), c.Name)
			} else if err.Data.Field != c.Field {
				t.Errorf("%s %s: field %s, want %s", filepath.Base(f), c.Name, err.Data.Field, c.Field)
			}
			// Whatever the rule, matching it does not panic.
			Matches(c.Rule, Message{Subject: c.Name, HasBody: true, Body: c.Name})
		}
	}
}

func TestValidAddress(t *testing.T) {
	for s, want := range map[string]bool{
		"a@b.c": true, "a.b+c@sub.example.org": true,
		"": false, "a": false, "@b.c": false, "a@": false, "a@b": false, "a@@b.c": false,
		"a@.b.c": false, "a@b.c.": false, "a@b..c": false, "a b@c.d": false,
		"<a@b.c>": false, "a@b.c,d@e.f": false, "a\x00@b.c": false, "a@b.c\n": false,
		strings.Repeat("a", 250) + "@b.cd": false,
	} {
		if got := ValidAddress(s); got != want {
			t.Errorf("%q: %v", s, got)
		}
	}
}
