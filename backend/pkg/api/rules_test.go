// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// rulesMethods follow the update check's in AllMethods; only the
// signatures' (signature_test.go) come after them. notify.rulesChanged is
// followed by notify.signaturesChanged.
var rulesMethods = []string{"rules.list", "rules.create", "rules.update", "rules.delete",
	"rules.reorder", "rules.test", "rules.apply"}

func TestRulesMethodsOrder(t *testing.T) {
	n := len(AllMethods) - len(signatureMethods)
	if !reflect.DeepEqual(AllMethods[n-len(rulesMethods):n], rulesMethods) {
		t.Fatalf("AllMethods does not have the rules methods before the signatures': %v", AllMethods[n-len(rulesMethods):n])
	}
	if AllMethods[n-len(rulesMethods)-1] != MethodUpdateDismissNotice {
		t.Fatal("the rules methods do not follow the update check's")
	}
	if AllNotifications[len(AllNotifications)-2] != NotifyRulesChanged {
		t.Fatalf("AllNotifications: %v", AllNotifications)
	}
}

// A rule's wire form: the input is inline, optional fields are left out.
func TestRuleWire(t *testing.T) {
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	r := Rule{ID: "r_1", RuleInput: RuleInput{Name: "n", Enabled: true, Match: RuleMatchAll,
		Conditions: []RuleCondition{{Field: RuleFieldFrom, Op: RuleOpDomainIs, Value: "example.com"}},
		Actions:    []RuleAction{{Kind: RuleActionMarkRead}, {Kind: RuleActionForward, To: "a@b.c"}}},
		Outgoing: true, CreatedAt: at, UpdatedAt: at}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"r_1","name":"n","enabled":true,"match":"all","conditions":[{"field":"from","op":"domainIs","value":"example.com"}],` +
		`"actions":[{"kind":"markRead"},{"kind":"forward","to":"a@b.c"}],"stop":false,"outgoing":true,` +
		`"createdAt":"2026-11-02T08:00:00Z","updatedAt":"2026-11-02T08:00:00Z","applied":0,"skipped":0}`
	if string(raw) != want {
		t.Errorf("got  %s\nwant %s", raw, want)
	}
	var back Rule
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, r) {
		t.Errorf("round trip %+v %v", back, err)
	}
}

func TestRuleActionOutgoing(t *testing.T) {
	for k, want := range map[RuleActionKind]bool{RuleActionForward: true, RuleActionAutoReply: true,
		RuleActionMove: false, RuleActionMarkRead: false, RuleActionFlag: false, RuleActionTrash: false, RuleActionJunk: false} {
		if k.IsOutgoing() != want {
			t.Errorf("%s: %v", k, !want)
		}
	}
}
