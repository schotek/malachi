// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"errors"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// TestModelMsgidsInTemplate: the model and the view model bring no text of
// their own. Every msgid they translate — through the texts of text.go,
// triage.go and reply.go, and the one they reuse, "(No subject)" — is in
// po/malachi.pot with its context and plural.
func TestModelMsgidsInTemplate(t *testing.T) {
	used := recorder{}
	env := Env{Tr: used, Dates: testDates{time.UTC}, Loc: time.UTC}
	snap := SampleSnapshot(testNow, time.UTC)
	extra := mk("x", StateYou, withSubject(""), withVisibility(Visibility{Kind: VisibleSnoozed, At: testNow.Add(time.Hour)}),
		notLoaded())
	snap.Cases = append(snap.Cases, extra)
	for _, p := range []Phase{PhaseLoading, PhasePreparing, PhaseReady, PhaseUnavailable, PhaseFailed, PhaseUnsupported, PhaseOff} {
		s := snap
		s.Phase = p
		s.Truncated = true
		for _, style := range Styles {
			for _, f := range []Filter{{}, filterState(StateYou), doneFilter} {
				for _, sel := range []CaseID{"", "sample-1", "sample-2", "sample-6", "sample-14", "x"} {
					View(s, ViewState{Style: style, Filter: f, Selection: sel, InlineDetail: true}, testNow, env)
				}
			}
		}
		s.Annotated = false
		View(s, NewViewState(), testNow, env)
	}
	RemindPresets(testNow, env)
	for _, k := range []SuggestReplyStateKind{SuggestIdle, SuggestRunning, SuggestFailed} {
		SuggestReplyViewOf(SuggestReplyInputs{Offered: true, Available: true, ClaudeFound: true, State: SuggestReplyState{Kind: k}},
			PanelWords{}, used)
	}
	for _, a := range Actions {
		for _, err := range []error{errors.New("x"), &api.Error{Code: api.CodeCaseNotFound}} {
			FailedText(a, err, used)
		}
	}
	src := NewInMemorySource(snap, used)
	src.SetHandlers(Handlers{Notice: func(string) {}})
	src.Archive("sample-1")
	src.Unflag("sample-2")

	pot := template(t)
	for k := range used {
		if _, ok := pot[k]; !ok {
			t.Errorf("po/malachi.pot lacks msgid %q (context %q, plural %q)", k.msgid, k.ctx, k.plural)
		}
	}
	if !used[msgKey{msgid: "(No subject)"}] {
		t.Error("the subject's placeholder was never asked for")
	}
}
