// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestCalendarFreeBusy(t *testing.T) {
	f := newFakeCal(t)
	var asked map[string]any
	f.schedule = func(body map[string]any) (int, any) {
		asked = body
		return 200, map[string]any{"value": []any{
			map[string]any{"scheduleId": "JANA@example.org", "availabilityView": "0220", "scheduleItems": []any{
				map[string]any{"status": "busy", "start": gtime(calT0), "end": gtime(calT0.Add(time.Hour))},
				map[string]any{"status": "free", "start": gtime(calT0.Add(2 * time.Hour)), "end": gtime(calT0.Add(3 * time.Hour))},
				map[string]any{"status": "oof", "start": gtime(calT0.Add(-time.Hour)), "end": gtime(calT0.Add(-30 * time.Minute))},
			}},
			map[string]any{"scheduleId": "petr@example.org", "availabilityView": "0120"},
			map[string]any{"scheduleId": "ext@other.example", "error": map[string]any{"message": "not found", "responseCode": "ErrorMailRecipientNotFound"}},
		}}
	}
	p := f.provider(CalendarProviderOptions{})
	from, to := calT0.Add(-time.Hour), calT0.Add(time.Hour)
	got, err := p.FreeBusy(context.Background(), []string{"jana@example.org", "petr@example.org", "ext@other.example", "nobody@example.org"}, from, to, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if asked["availabilityViewInterval"] != float64(30) || len(asked["schedules"].([]any)) != 4 {
		t.Errorf("asked %v", asked)
	}
	if len(got) != 4 || !got[0].Known || !got[1].Known || got[2].Known || got[3].Known {
		t.Fatalf("known %+v", got)
	}
	if b := got[0].Busy; len(b) != 2 || b[0].ShowAs != api.ShowAsOOF || b[1].ShowAs != api.ShowAsBusy || !b[1].End.Equal(calT0.Add(time.Hour)) {
		t.Errorf("items %+v", b)
	}
	// From the view: 0 free, 1 tentative, 2 busy (30-minute steps).
	if b := got[1].Busy; len(b) != 2 || b[0].ShowAs != api.ShowAsTentative || !b[0].Start.Equal(from.Add(30*time.Minute)) || b[1].ShowAs != api.ShowAsBusy {
		t.Errorf("view %+v", b)
	}
	if got[2].Busy == nil || got[3].Busy == nil {
		t.Error("busy must never be null")
	}
	if !p.Features().FreeBusy {
		t.Error("free/busy stays on")
	}

	// Limits are refused before asking.
	many := make([]string, api.MaxFreeBusyAttendees+1)
	for i := range many {
		many[i] = fmt.Sprintf("p%d@example.org", i)
	}
	if _, err := p.FreeBusy(context.Background(), many, from, to, time.Hour); apiCodeOf(err) != api.CodeInvalidArgument {
		t.Errorf("too many: %v", err)
	}
	if _, err := p.FreeBusy(context.Background(), []string{"a@example.org"}, from, from.AddDate(0, 0, 63), time.Hour); apiCodeOf(err) != api.CodeInvalidArgument {
		t.Errorf("too long: %v", err)
	}

	// A personal account asks nothing.
	before := len(f.reqs("POST", "getSchedule"))
	pp := f.provider(CalendarProviderOptions{Personal: true})
	got, err = pp.FreeBusy(context.Background(), []string{"jana@example.org"}, from, to, time.Hour)
	if err != nil || len(got) != 1 || got[0].Known || len(f.reqs("POST", "getSchedule")) != before {
		t.Errorf("personal %v %+v", err, got)
	}

	// A refusal of the whole call (what a mailbox without the feature
	// answers) is unknown for everyone, and free/busy is off after it.
	f.schedule = func(map[string]any) (int, any) {
		return 400, map[string]any{"error": map[string]string{"code": "ErrorInvalidRequest", "message": "not supported"}}
	}
	got, err = p.FreeBusy(context.Background(), []string{"jana@example.org"}, from, to, 15*time.Minute)
	if err != nil || got[0].Known || p.Features().FreeBusy {
		t.Errorf("refused %v %+v %+v", err, got, p.Features())
	}
	// Consent is an error, not unknown.
	f.fail["POST /me/calendar/getSchedule"] = fakeFailure{403, "ErrorAccessDenied"}
	p2 := f.provider(CalendarProviderOptions{})
	if _, err := p2.FreeBusy(context.Background(), []string{"jana@example.org"}, from, to, time.Hour); apiCodeOf(err) != api.CodeCalendarConsentRequired {
		t.Errorf("consent %v", err)
	}
}
