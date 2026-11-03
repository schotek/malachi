// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// updateMethods follow the address books' in AllMethods; the rules'
// (rules_test.go) and the signatures' (signature_test.go) come after them.
// notify.updateChanged is just before notify.rulesChanged.
var updateMethods = []string{"update.status", "update.check", "update.setPrefs", "update.dismiss", "update.dismissNotice"}

func TestUpdateMethodsOrder(t *testing.T) {
	n := len(AllMethods) - len(rulesMethods) - len(signatureMethods)
	if !reflect.DeepEqual(AllMethods[n-len(updateMethods):n], updateMethods) {
		t.Fatalf("AllMethods does not end with the update methods: %v", AllMethods[n-len(updateMethods):n])
	}
	if AllMethods[n-len(updateMethods)-1] != MethodContactPhoto {
		t.Fatal("the update methods do not follow the address books'")
	}
	if AllNotifications[len(AllNotifications)-3] != NotifyUpdateChanged {
		t.Fatalf("AllNotifications: %v", AllNotifications)
	}
}

// The minimal status sends the required fields only.
func TestUpdateStatusWire(t *testing.T) {
	raw, _ := json.Marshal(UpdateStatus{Enabled: true, Current: "0.1.2", State: UpdateIdle})
	if string(raw) != `{"enabled":true,"current":"0.1.2","state":"idle"}` {
		t.Errorf("minimal %s", raw)
	}
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	raw, _ = json.Marshal(UpdateInfo{Version: "0.1.3", PublishedAt: at, Notes: "n", InfoURL: "i", URL: "u", Size: 1})
	want := `{"version":"0.1.3","publishedAt":"2026-11-02T08:00:00Z","security":false,"notes":"n","infoUrl":"i","url":"u","size":1,"skipped":false,"prompt":false}`
	if string(raw) != want {
		t.Errorf("info %s", raw)
	}
}
