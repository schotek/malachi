// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// assistantMethods end AllMethods, after the CRM's; their notification
// ends AllNotifications.
var assistantMethods = []string{"assistant.profileGet", "assistant.profileSet", "assistant.profilePropose", "assistant.profileResolve"}

func TestAssistantMethodsOrder(t *testing.T) {
	n := len(AllMethods)
	if !reflect.DeepEqual(AllMethods[n-len(assistantMethods):], assistantMethods) {
		t.Fatalf("AllMethods does not end with the assistant profile methods: %v", AllMethods[n-len(assistantMethods):])
	}
	if AllMethods[n-len(assistantMethods)-1] != MethodCrmRemove {
		t.Fatal("the assistant profile methods do not follow the CRM's")
	}
	if AllNotifications[len(AllNotifications)-1] != NotifyAssistantProfileChanged {
		t.Fatalf("AllNotifications: %v", AllNotifications)
	}
}

func TestAssistantProfileJSON(t *testing.T) {
	at := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	got := roundTrip(t, "profile", AssistantProfileGetResult{
		Profile: AssistantProfile{
			Text: "Píšu stručně.", Gender: GenderMasculine, ShareWithMCP: true, UpdatedAt: &at,
			Accounts: []ProfileAccountNote{{AccountID: "acc_1", Note: "Podepisuj jako studio."}},
		},
		Proposals: []ProfileProposal{{ID: "p_1", Text: "Schůzky v pondělí.", Source: ProfileFromPanel, CreatedAt: at}},
		Limits:    DefaultProfileLimits(),
	})
	want := `{"profile":{"text":"Píšu stručně.","gender":"masculine",` +
		`"accounts":[{"accountId":"acc_1","note":"Podepisuj jako studio."}],"shareWithMcp":true,` +
		`"updatedAt":"2026-10-21T08:00:00Z"},` +
		`"proposals":[{"id":"p_1","text":"Schůzky v pondělí.","source":"panel","createdAt":"2026-10-21T08:00:00Z"}],` +
		`"limits":{"text":2000,"accountNote":500,"proposal":300}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// Absent fields of a set stay absent: the daemon reads them as "no change".
	raw, _ := json.Marshal(AssistantProfileSetParams{})
	if string(raw) != `{}` {
		t.Fatalf("empty set %s", raw)
	}
}

func TestProfileGenderValid(t *testing.T) {
	for _, g := range []ProfileGender{GenderUnset, GenderMasculine, GenderFeminine, GenderNeutral} {
		if !g.Valid() {
			t.Errorf("%q not valid", g)
		}
	}
	for _, g := range []ProfileGender{"", "male", "Masculine"} {
		if g.Valid() {
			t.Errorf("%q valid", g)
		}
	}
}
