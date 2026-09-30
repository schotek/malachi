// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jiratest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// Workflow transitions: GET and POST /rest/api/{3,2}/issue/{id}/transitions.
// An issue offers the transitions of its workflow (SetTransitions, else
// DefaultTransitions) that lead somewhere else than its current status,
// as the sites list the transitions out of a status. A POST names one by
// id and may carry fields; the fake refuses an id the issue does not offer
// and a screen whose required fields are missing with the sites' 400
// envelopes, otherwise it moves the issue, as the token's user, with a
// changelog entry, and answers 204.

// Transition is a workflow transition of the fake.
type Transition struct {
	ID, Name string
	To       string // status id
	// HasScreen says the transition opens a screen on the site; Required
	// and Optional are the field ids of that screen (listed with
	// expand=transitions.fields).
	HasScreen bool
	Required  []string
	Optional  []string
}

// DefaultTransitions is the workflow of every issue that has no transitions
// of its own: Start Progress (11) to In Progress, Resolve (21) to Done
// through a screen with a required resolution, Wait for customer (31) to
// Waiting for customer through a screen with an optional comment, and
// Reopen (41) to To Do.
func DefaultTransitions() []*Transition {
	return []*Transition{
		{ID: "11", Name: "Start Progress", To: "3"},
		{ID: "21", Name: "Resolve", To: "10001", HasScreen: true, Required: []string{"resolution"}, Optional: []string{"comment"}},
		{ID: "31", Name: "Wait for customer", To: "10002", HasScreen: true, Optional: []string{"comment"}},
		{ID: "41", Name: "Reopen", To: "1"},
	}
}

// SetTransitions replaces the issue's workflow.
func (f *Server) SetTransitions(issueID string, ts ...*Transition) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mustIssue(issueID).Transitions = ts
}

// TransitionsPerformed lists the ids of the transitions clients performed
// on the issue (POST), in order.
func (f *Server) TransitionsPerformed(issueID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.mustIssue(issueID).performed)
}

// offeredLocked is the transitions the issue offers now.
func (f *Server) offeredLocked(is *Issue) []*Transition {
	ts := is.Transitions
	if ts == nil {
		ts = DefaultTransitions()
	}
	var out []*Transition
	for _, t := range ts {
		if t.To != is.Status {
			out = append(out, t)
		}
	}
	return out
}

func (f *Server) transitions(w http.ResponseWriter, ref string, q url.Values) {
	is := f.issueByRef(ref)
	if is == nil {
		f.fail(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
		return
	}
	withFields := slices.Contains(strings.Split(q.Get("expand"), ","), "transitions.fields")
	var out []any
	for _, t := range f.offeredLocked(is) {
		out = append(out, f.transitionJSON(t, withFields))
	}
	f.reply(w, map[string]any{"expand": "transitions", "transitions": orEmpty(out)})
}

func (f *Server) transitionJSON(t *Transition, withFields bool) map[string]any {
	st := f.statusLocked(t.To)
	to := f.statusJSON(t.To).(map[string]any)
	to["self"] = f.Site.String() + f.apiPrefix() + "/status/" + st.ID
	to["description"] = ""
	to["iconUrl"] = f.Site.String() + "/images/icons/statuses/generic.png"
	j := map[string]any{"id": t.ID, "name": t.Name, "to": to, "hasScreen": t.HasScreen,
		"isGlobal": false, "isInitial": false, "isAvailable": true, "isConditional": false}
	if withFields {
		fields := map[string]any{}
		for _, id := range t.Required {
			fields[id] = f.fieldJSON(id, true)
		}
		for _, id := range t.Optional {
			fields[id] = f.fieldJSON(id, false)
		}
		j["fields"] = fields
	}
	return j
}

func (f *Server) fieldJSON(id string, required bool) map[string]any {
	j := map[string]any{"required": required, "key": id, "name": strings.ToUpper(id[:1]) + id[1:],
		"hasDefaultValue": false, "operations": []string{"set"}}
	switch id {
	case "resolution":
		j["schema"] = map[string]any{"type": "resolution", "system": "resolution"}
		j["allowedValues"] = []any{map[string]any{"self": f.Site.String() + f.apiPrefix() + "/resolution/10000", "id": "10000", "name": "Done"}}
	case "comment":
		j["schema"] = map[string]any{"type": "comments-page", "system": "comment"}
		j["operations"] = []string{"add", "edit", "remove"}
	default:
		j["schema"] = map[string]any{"type": "string", "system": id}
	}
	return j
}

func (f *Server) postTransition(w http.ResponseWriter, ref string, body []byte) {
	is := f.issueByRef(ref)
	if is == nil {
		f.fail(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
		return
	}
	var req struct {
		Transition *struct {
			ID json.RawMessage `json:"id"`
		} `json:"transition"`
		Fields          map[string]json.RawMessage `json:"fields"`
		Update          json.RawMessage            `json:"update"`
		HistoryMetadata json.RawMessage            `json:"historyMetadata"`
		Properties      json.RawMessage            `json:"properties"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.Transition == nil {
		f.fail(w, http.StatusBadRequest, "Invalid request payload: a transition is required.")
		return
	}
	id := strings.Trim(strings.TrimSpace(string(req.Transition.ID)), `"`)
	var chosen *Transition
	for _, t := range f.offeredLocked(is) {
		if t.ID == id {
			chosen = t
		}
	}
	if chosen == nil {
		f.fail(w, http.StatusBadRequest, "Transition id '"+id+"' is not valid for this issue.")
		return
	}
	missing := map[string]string{}
	for _, fid := range chosen.Required {
		if _, ok := req.Fields[fid]; !ok {
			missing[fid] = strings.ToUpper(fid[:1]) + fid[1:] + " is required."
		}
	}
	if len(missing) > 0 {
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"errorMessages": []string{}, "errors": missing})
		return
	}
	from, to := f.statusLocked(is.Status), f.statusLocked(chosen.To)
	now := f.Now()
	is.Status = chosen.To
	is.Histories = append(is.Histories, &History{ID: f.nextID(), Author: f.Me, Created: now, Items: []Item{{
		Field: "status", FieldType: "jira", From: from.ID, FromString: from.Name, To: to.ID, ToString: to.Name,
	}}})
	is.performed = append(is.performed, chosen.ID)
	f.touchLocked(is, now)
	w.WriteHeader(http.StatusNoContent)
}
