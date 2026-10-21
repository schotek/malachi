// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// crmMethods follow the calendar's in AllMethods (docs/eway.md D13);
// only the assistant profile's (assistant_test.go) come after them.
var crmMethods = []string{"account.detectEway", "crm.lookup", "crm.search", "crm.save", "crm.remove"}

func TestCrmMethodsOrder(t *testing.T) {
	n := len(AllMethods) - len(assistantMethods)
	if !reflect.DeepEqual(AllMethods[n-len(crmMethods):n], crmMethods) {
		t.Fatalf("AllMethods does not end with the CRM methods: %v", AllMethods[n-len(crmMethods):n])
	}
	if AllMethods[n-len(crmMethods)-1] != MethodInvitationApplyCancel {
		t.Fatal("the CRM methods do not follow the calendar's")
	}
}

// The codes are the free ones of their groups (docs/eway.md D14).
func TestCrmErrorCodes(t *testing.T) {
	for code, name := range map[ErrorCode]string{1109: "crmItemNotFound", 1205: "crmSignInUnsupported", 1307: "crmDuplicate"} {
		if code.String() != name {
			t.Errorf("%d = %s, want %s", code, code.String(), name)
		}
	}
	raw, _ := json.Marshal(CrmSignInUnsupportedData{Reason: CrmSignInMicrosoft})
	if string(raw) != `{"reason":"microsoft"}` {
		t.Errorf("data %s", raw)
	}
	raw, _ = json.Marshal(MessagesNotDownloadedData{MessageIDs: []MessageID{"m_1"}})
	if string(raw) != `{"messageIds":["m_1"]}` {
		t.Errorf("data %s", raw)
	}
}

// The wire form of the CRM types: optional fields left out, a lookup's
// unsaved message carries "record": null.
func TestCrmJSON(t *testing.T) {
	at := time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"config", AccountConfig{Name: "acme", Email: "jan@acme.example", Kind: AccountEway,
			Eway: &EwayConfig{ServiceURL: "https://hosting.eway-crm.com/acme", Username: "jan", FollowThreads: true}},
			`{"name":"acme","email":"jan@acme.example","kind":"eway","eway":{"serviceUrl":"https://hosting.eway-crm.com/acme","username":"jan","followThreads":true}}`},
		{"auto record", CrmRecord{AccountID: "acc_3", ItemGUID: "g", Auto: true, Items: []CrmItem{}, URL: "u"},
			`{"accountId":"acc_3","itemGuid":"g","savedAt":"0001-01-01T00:00:00Z","checkedAt":"0001-01-01T00:00:00Z","private":false,"auto":true,"items":[],"url":"u"}`},
		{"item", CrmItem{Kind: CrmContact, GUID: "g", Name: "Jan", URL: "u"},
			`{"kind":"contact","guid":"g","name":"Jan","url":"u"}`},
		{"lookup", CrmLookupResult{AccountID: "acc_3", Saved: []CrmSavedMessage{{MessageID: "m_1"}}, Suggestions: []CrmItem{}},
			`{"accountId":"acc_3","saved":[{"messageId":"m_1","record":null}],"suggestions":[]}`},
		{"record", CrmRecord{AccountID: "acc_3", ItemGUID: "g", SavedAt: at, CheckedAt: at, Items: []CrmItem{}, URL: "u"},
			`{"accountId":"acc_3","itemGuid":"g","savedAt":"2026-10-17T08:00:00Z","checkedAt":"2026-10-17T08:00:00Z","private":false,"items":[],"url":"u"}`},
		{"save", CrmSaveParams{MessageAccountID: "acc_1", MessageIDs: []MessageID{"m_1"}, Superior: &CrmItemRef{Kind: CrmDeal, GUID: "g"}},
			`{"messageAccountId":"acc_1","messageIds":["m_1"],"superior":{"kind":"deal","guid":"g"}}`},
		{"summary", MessageSummary{ID: "m_1", CrmSaved: true, Date: at},
			`{"id":"m_1","accountId":"","folderId":"","from":null,"subject":"","date":"2026-10-17T08:00:00Z","snippet":"","flags":null,"hasAttachments":false,"size":0,"crmSaved":true}`},
		{"detect", AccountDetectEwayResult{ServiceURL: "https://h/x"}, `{"serviceUrl":"https://h/x","microsoftSignIn":false}`},
	}
	for _, c := range cases {
		raw, err := json.Marshal(c.v)
		if err != nil || string(raw) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, raw, c.want)
		}
	}
}
