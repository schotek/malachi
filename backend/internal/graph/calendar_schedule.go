// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package graph

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/calendar"
	"github.com/schotek/malachi/backend/internal/calsync"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Free/busy of other people (D22): POST /me/calendar/getSchedule.

type scheduleItem struct {
	Status string            `json:"status"`
	Start  *dateTimeTimeZone `json:"start"`
	End    *dateTimeTimeZone `json:"end"`
}

type scheduleError struct {
	Message      string `json:"message"`
	ResponseCode string `json:"responseCode"`
}

type scheduleInformation struct {
	ScheduleID       string         `json:"scheduleId"`
	AvailabilityView string         `json:"availabilityView"`
	ScheduleItems    []scheduleItem `json:"scheduleItems"`
	Error            *scheduleError `json:"error"`
}

// maxBusyPerPerson bounds the busy intervals read of one person.
const maxBusyPerPerson = 2000

// scheduleStep is the availabilityViewInterval Graph is asked for: 15, 30
// or 60 minutes (the nearest).
func scheduleStep(step time.Duration) int {
	switch m := int(step / time.Minute); {
	case m <= 22:
		return 15
	case m <= 45:
		return 30
	}
	return 60
}

func unknownAll(addrs []string) []calsync.Availability {
	out := make([]calsync.Availability, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, calsync.Availability{Address: a, Known: false, Busy: []api.BusyInterval{}})
	}
	return out
}

// scheduleRefusedAsWhole: an answer to the whole getSchedule call that
// says the mailbox has no free/busy of others (a personal account, a
// mailbox without the feature), as opposed to a transient or auth failure.
func scheduleRefusedAsWhole(err error) bool {
	se, ok := statusOf(err)
	if !ok || isAccessDenied(err) {
		return false
	}
	switch se.Status {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	}
	return false
}

// FreeBusy asks getSchedule about up to api.MaxFreeBusyAttendees people
// for at most api.MaxFreeBusyDays. A person the answer leaves out, or
// whose item carries an error, is known: false. A personal account
// (Options.Personal), and an account whose getSchedule is refused as a
// whole (4xx other than 401, consent and throttling), answers known:
// false for everyone; after such a refusal Features reports no freeBusy.
func (p *CalendarProvider) FreeBusy(ctx context.Context, attendees []string, from, to time.Time, step time.Duration) ([]calsync.Availability, error) {
	if len(attendees) == 0 {
		return []calsync.Availability{}, nil
	}
	if len(attendees) > api.MaxFreeBusyAttendees {
		return nil, api.NewError(api.CodeInvalidArgument, "graph: more than %d people", api.MaxFreeBusyAttendees)
	}
	if !from.Before(to) || to.Sub(from) > time.Duration(api.MaxFreeBusyDays)*24*time.Hour {
		return nil, api.NewError(api.CodeInvalidArgument, "graph: free/busy range")
	}
	addrs := make([]string, 0, len(attendees))
	for _, a := range attendees {
		a = calendar.CleanLine(a, calendar.MaxNameBytes)
		if a == "" || !strings.Contains(a, "@") {
			return nil, api.NewError(api.CodeInvalidArgument, "graph: bad address")
		}
		addrs = append(addrs, a)
	}
	p.mu.Lock()
	off := p.opts.Personal || p.noFreeBusy
	p.mu.Unlock()
	if off {
		return unknownAll(addrs), nil
	}
	body := map[string]any{
		"schedules":                addrs,
		"startTime":                map[string]string{"dateTime": from.UTC().Format(localLayout), "timeZone": "UTC"},
		"endTime":                  map[string]string{"dateTime": to.UTC().Format(localLayout), "timeZone": "UTC"},
		"availabilityViewInterval": scheduleStep(step),
	}
	var resp struct {
		Value []scheduleInformation `json:"value"`
	}
	if err := p.c.calDo(ctx, calReq{method: http.MethodPost, url: "me/calendar/getSchedule", body: body, out: &resp, prio: PriorityHigh}); err != nil {
		if scheduleRefusedAsWhole(err) {
			p.mu.Lock()
			p.noFreeBusy = true
			p.mu.Unlock()
			return unknownAll(addrs), nil
		}
		return nil, p.readError(err)
	}
	byAddr := map[string]*scheduleInformation{}
	for i := range resp.Value {
		si := &resp.Value[i]
		k := strings.ToLower(strings.TrimSpace(si.ScheduleID))
		if _, dup := byAddr[k]; !dup {
			byAddr[k] = si
		}
	}
	out := make([]calsync.Availability, 0, len(addrs))
	for _, a := range addrs {
		av := calsync.Availability{Address: a, Busy: []api.BusyInterval{}}
		si := byAddr[strings.ToLower(a)]
		if si == nil || si.Error != nil {
			out = append(out, av)
			continue
		}
		av.Known = true
		if len(si.ScheduleItems) > 0 {
			av.Busy = busyFromItems(si.ScheduleItems, from, to)
		} else {
			av.Busy = busyFromView(si.AvailabilityView, from, to, time.Duration(scheduleStep(step))*time.Minute)
		}
		out = append(out, av)
	}
	return out, nil
}

func busyShowAs(status string) (api.ShowAs, bool) {
	switch status {
	case "free":
		return "", false
	case "tentative":
		return api.ShowAsTentative, true
	case "oof":
		return api.ShowAsOOF, true
	case "workingElsewhere":
		return api.ShowAsWorkingElsewhere, true
	case "busy":
		return api.ShowAsBusy, true
	}
	return api.ShowAsUnknown, true
}

func busyFromItems(items []scheduleItem, from, to time.Time) []api.BusyInterval {
	out := []api.BusyInterval{}
	for _, it := range items {
		if len(out) >= maxBusyPerPerson {
			break
		}
		show, busy := busyShowAs(it.Status)
		if !busy {
			continue
		}
		_, s, ok1 := parseGraphTime(it.Start)
		_, e, ok2 := parseGraphTime(it.End)
		if !ok1 || !ok2 || !e.After(s) {
			continue
		}
		if s.Before(from) {
			s = from
		}
		if e.After(to) {
			e = to
		}
		if !e.After(s) {
			continue
		}
		out = append(out, api.BusyInterval{Start: s.UTC(), End: e.UTC(), ShowAs: show})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// busyFromView reads availabilityView (one digit per interval: 0 free, 1
// tentative, 2 busy, 3 oof, 4 working elsewhere), merging runs.
func busyFromView(view string, from, to time.Time, step time.Duration) []api.BusyInterval {
	out := []api.BusyInterval{}
	var shows = map[byte]api.ShowAs{'1': api.ShowAsTentative, '2': api.ShowAsBusy, '3': api.ShowAsOOF, '4': api.ShowAsWorkingElsewhere}
	for i := 0; i < len(view) && len(out) < maxBusyPerPerson; i++ {
		s := from.Add(time.Duration(i) * step)
		if !s.Before(to) {
			break
		}
		show, ok := shows[view[i]]
		if !ok {
			continue
		}
		e := s.Add(step)
		if e.After(to) {
			e = to
		}
		if n := len(out); n > 0 && out[n-1].End.Equal(s) && out[n-1].ShowAs == show {
			out[n-1].End = e
			continue
		}
		out = append(out, api.BusyInterval{Start: s.UTC(), End: e.UTC(), ShowAs: show})
	}
	return out
}
