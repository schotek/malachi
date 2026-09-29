// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// The one-shot requests (macOS AssistantRequestTests) against the fake
// claude of the panel's tests.

// structuredResult is a result line with a structured_output (raw JSON)
// and a text.
func structuredResult(structured, text string) string {
	return `{"type":"result","subtype":"success","is_error":false,"result":"` + text + `","structured_output":` + structured + `,"total_cost_usd":0.001}`
}

const errorResult = `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"","total_cost_usd":0}`

// requestHarness is a Request over a fake claude.
type requestHarness struct {
	t             *testing.T
	loop          *testLoop
	fake          *fakeClaude
	settings      *memSettings
	request       *Request
	work          string
	consentAsked  int
	consentAnswer bool
}

func newRequestHarness(t *testing.T, fake *fakeClaude, consent, found bool) *requestHarness {
	t.Helper()
	loop := newTestLoop()
	s := &memSettings{consent: consent, claudePath: fake.path, model: assistant.Haiku}
	work := filepath.Join(fake.dir, "work")
	loc := NewLocator(s, []string{"HOME=" + fake.dir}, loop, "", discardLog())
	loc.timeout = 5 * time.Second
	prefix := fake.dir + "/"
	loc.usable = func(p string) bool { return found && strings.HasPrefix(p, prefix) && IsExecutableFile(p) }
	r := NewRequest(RequestConfig{
		Settings: s, Locator: loc, Loop: loop, Log: discardLog(), Directory: work,
		Env:       []string{"HOME=" + fake.dir, "LANG=cs_CZ.UTF-8", "ANTHROPIC_API_KEY=sk-never", "CLAUDECODE=1"},
		KillGrace: 300 * time.Millisecond,
	})
	r.Timeout = 10 * time.Second
	h := &requestHarness{t: t, loop: loop, fake: fake, settings: s, request: r, work: work, consentAnswer: true}
	r.Consent = func(done func(bool)) {
		h.consentAsked++
		answer := h.consentAnswer
		loop.Post(func() { done(answer) })
	}
	t.Cleanup(r.Cancel)
	return h
}

// One turn without the bridge: the one-shot command line, the message on
// stdin, stdin closed, the streamed text, then the result.
func TestRequestAnswersOnce(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{
		fakeInit, fakeDelta("Dear "), fakeDelta("Jana"), fakeText("Dear Jana,"), fakeResult("Dear Jana,", true),
	}})
	h := newRequestHarness(t, fake, true, true)
	var outcomes []Outcome
	var texts []string
	h.request.Start("SYS", "line one\nline two", "", func(s string) { texts = append(texts, s) },
		func(o Outcome) { outcomes = append(outcomes, o) })
	if !h.request.Running() {
		t.Fatal("not running after Start")
	}
	h.loop.runUntil(t, func() bool { return len(outcomes) > 0 })
	if len(outcomes) != 1 || outcomes[0].Kind != OutcomeAnswered || outcomes[0].Text != "Dear Jana," || outcomes[0].Structured != nil {
		t.Errorf("outcomes %+v", outcomes)
	}
	if want := []string{"Dear ", "Dear Jana", "Dear Jana,"}; !slices.Equal(texts, want) {
		t.Errorf("texts %q, want %q", texts, want)
	}
	if h.request.Running() || h.consentAsked != 0 || fake.starts() != 1 {
		t.Errorf("running %v, consent %d, starts %d", h.request.Running(), h.consentAsked, fake.starts())
	}
	if !slices.Equal(fake.prompts(), []string{"line one\nline two"}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	wantArgs := assistant.Args(assistant.Options{Model: assistant.Haiku, SystemPrompt: "SYS"})
	if got := fake.args(); !slices.Equal(got, wantArgs) || slices.Contains(got, "--mcp-config") || slices.Contains(got, "--allowedTools") {
		t.Errorf("args %q", got)
	}
	env := fake.env()
	if strings.Contains(env, "ANTHROPIC") || strings.Contains(env, "CLAUDECODE") || !strings.Contains(env, "LANG=cs_CZ.UTF-8") {
		t.Errorf("environment:\n%s", env)
	}
	if fake.cwd() != h.work {
		t.Errorf("cwd %q", fake.cwd())
	}
	if fi, err := os.Stat(h.work); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("work directory: %v %v", fi, err)
	}
}

// stdin is closed after the one message: a Claude Code that answers
// nothing ends at once, and that is a failure with its status.
func TestRequestStdinIsClosed(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit}})
	h := newRequestHarness(t, fake, true, true)
	var outcomes []Outcome
	h.request.Start("S", "m", "", nil, func(o Outcome) { outcomes = append(outcomes, o) })
	h.loop.runUntil(t, func() bool { return len(outcomes) > 0 })
	if want := failed(FailureStopped, "claude exited with status 0"); !slices.EqualFunc(outcomes, []Outcome{want}, sameOutcome) {
		t.Errorf("outcomes %+v", outcomes)
	}
}

func sameOutcome(a, b Outcome) bool {
	return a.Kind == b.Kind && a.Text == b.Text && string(a.Structured) == string(b.Structured) && a.Failure == b.Failure
}

// A schema goes on the command line; the structured_output comes back as it
// was written.
func TestRequestStructuredAnswer(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit, structuredResult(`{"query":"x"}`, "")}})
	h := newRequestHarness(t, fake, true, true)
	var outcomes []Outcome
	h.request.Start("S", "m", assistant.SearchSchema, nil, func(o Outcome) { outcomes = append(outcomes, o) })
	h.loop.runUntil(t, func() bool { return len(outcomes) > 0 })
	if want := (Outcome{Kind: OutcomeAnswered, Structured: []byte(`{"query":"x"}`)}); !sameOutcome(outcomes[0], want) {
		t.Errorf("outcome %+v", outcomes[0])
	}
	if args := fake.args(); !slices.Equal(args[len(args)-2:], []string{"--json-schema", assistant.SearchSchema}) {
		t.Errorf("args %q", args)
	}
}

// Consent: declined, nothing starts; allowed, it is kept, and the answer
// counts even when the request was cancelled meanwhile.
func TestRequestConsent(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newRequestHarness(t, fake, false, true)
	var outcomes []Outcome
	record := func(o Outcome) { outcomes = append(outcomes, o) }
	h.consentAnswer = false
	h.request.Start("S", "m", "", nil, record)
	h.loop.runUntil(t, func() bool { return len(outcomes) > 0 })
	if outcomes[0].Kind != OutcomeDeclined || h.consentAsked != 1 || h.settings.consent || fake.starts() != 0 {
		t.Errorf("outcome %+v, asked %d, consent %v, starts %d", outcomes[0], h.consentAsked, h.settings.consent, fake.starts())
	}

	// Allowed while the request was cancelled: kept, nothing sent.
	var answer func(bool)
	h.request.Consent = func(done func(bool)) { answer = done }
	h.request.Start("S", "m", "", nil, record)
	h.loop.runUntil(t, func() bool { return answer != nil })
	h.request.Cancel()
	answer(true)
	h.loop.settle(t, 100*time.Millisecond)
	if !h.settings.consent || len(outcomes) != 1 || fake.starts() != 0 {
		t.Errorf("consent %v, outcomes %d, starts %d", h.settings.consent, len(outcomes), fake.starts())
	}

	// From now on nobody is asked.
	h.request.Consent = nil
	h.request.Start("S", "m", "", nil, record)
	h.loop.runUntil(t, func() bool { return len(outcomes) == 2 })
	if want := (Outcome{Kind: OutcomeAnswered, Text: "done"}); !sameOutcome(outcomes[1], want) {
		t.Errorf("outcome %+v", outcomes[1])
	}
}

// Without a consent hook nothing is ever sent.
func TestRequestNoConsentHookSendsNothing(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newRequestHarness(t, fake, false, true)
	h.request.Consent = nil
	var outcomes []Outcome
	h.request.Start("S", "m", "", nil, func(o Outcome) { outcomes = append(outcomes, o) })
	h.loop.runUntil(t, func() bool { return len(outcomes) > 0 })
	if outcomes[0].Kind != OutcomeDeclined || fake.starts() != 0 {
		t.Errorf("outcome %+v, starts %d", outcomes[0], fake.starts())
	}
}

// Claude Code missing or signed out: the panel's texts.
func TestRequestNotFoundAndNotSignedIn(t *testing.T) {
	missing := newRequestHarness(t, newFakeClaude(t, "true", "", answerTurn("x")), true, false)
	var outcomes []Outcome
	record := func(o Outcome) { outcomes = append(outcomes, o) }
	missing.request.Start("S", "m", "", nil, record)
	missing.loop.runUntil(t, func() bool { return len(outcomes) > 0 })
	if !sameOutcome(outcomes[0], failed(FailureNotFound, "")) || missing.fake.starts() != 0 {
		t.Errorf("outcome %+v", outcomes[0])
	}
	signedOut := newRequestHarness(t, newFakeClaude(t, "false", "", answerTurn("x")), true, true)
	signedOut.request.Start("S", "m", "", nil, record)
	signedOut.loop.runUntil(t, func() bool { return len(outcomes) == 2 })
	if !sameOutcome(outcomes[1], failed(FailureNotSignedIn, "")) || signedOut.fake.starts() != 0 {
		t.Errorf("outcome %+v", outcomes[1])
	}
	for _, c := range []struct {
		f            Failure
		text, reason string
	}{
		{Failure{Kind: FailureNotFound}, "Claude Code was not found on this computer", "Claude Code was not found on this computer"},
		{Failure{Kind: FailureNotSignedIn}, "Claude Code is not signed in. Run claude in Terminal and sign in.", "Not signed in: run claude in Terminal and sign in"},
		{Failure{Kind: FailureStopped, Reason: "x"}, "The assistant stopped: x", "x"},
	} {
		if c.f.Text(tr) != c.text || c.f.ReasonText(tr) != c.reason {
			t.Errorf("%+v: %q, %q", c.f, c.f.Text(tr), c.f.ReasonText(tr))
		}
	}
}

// A result that is not a success, and an exit before the result.
func TestRequestFailures(t *testing.T) {
	fake := newFakeClaude(t, "true", `if [ "$n" = 2 ]; then read -r line; echo 'Error: boom' >&2; exit 1; fi`,
		fakeTurn{lines: []string{fakeInit, errorResult}})
	h := newRequestHarness(t, fake, true, true)
	var outcomes []Outcome
	record := func(o Outcome) { outcomes = append(outcomes, o) }
	h.request.Start("S", "m", "", nil, record)
	h.loop.runUntil(t, func() bool { return len(outcomes) == 1 })
	if !sameOutcome(outcomes[0], failed(FailureStopped, "error_during_execution")) {
		t.Errorf("outcome %+v", outcomes[0])
	}
	h.request.Start("S", "m", "", nil, record)
	h.loop.runUntil(t, func() bool { return len(outcomes) == 2 })
	if !sameOutcome(outcomes[1], failed(FailureStopped, "Error: boom")) {
		t.Errorf("outcome %+v", outcomes[1])
	}
}

// No answer in time: the process is ended and the request fails.
func TestRequestTimeout(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit, fakeDelta("thinking")}, shell: "sleep 30"})
	h := newRequestHarness(t, fake, true, true)
	h.request.Timeout = 500 * time.Millisecond
	var outcomes []Outcome
	var texts []string
	h.request.Start("S", "m", "", func(s string) { texts = append(texts, s) }, func(o Outcome) { outcomes = append(outcomes, o) })
	h.loop.runUntil(t, func() bool { return len(outcomes) > 0 })
	if !sameOutcome(outcomes[0], failed(FailureStopped, timedOut)) || !slices.Equal(texts, []string{"thinking"}) || h.request.Running() {
		t.Errorf("outcome %+v, texts %q", outcomes[0], texts)
	}
}

// Cancelled: no completion, the process ended; a new request cancels the
// one under way.
func TestRequestCancelAndReplace(t *testing.T) {
	fake := newFakeClaude(t, "true", "",
		fakeTurn{lines: []string{fakeInit, fakeDelta("slow")}, shell: "sleep 30"},
		answerTurn("second"))
	h := newRequestHarness(t, fake, true, true)
	var outcomes []Outcome
	var texts []string
	record := func(o Outcome) { outcomes = append(outcomes, o) }
	h.request.Start("S", "one", "", func(s string) { texts = append(texts, s) }, record)
	h.loop.runUntil(t, func() bool { return slices.Equal(texts, []string{"slow"}) })
	h.request.Cancel()
	if h.request.Running() {
		t.Error("running after Cancel")
	}
	h.loop.settle(t, 200*time.Millisecond)
	if len(outcomes) != 0 {
		t.Fatalf("a cancelled request completed: %+v", outcomes)
	}
	// The fake numbers its turns over every start: the second start gets
	// "second".
	h.request.Start("S", "two", "", nil, record)
	h.request.Start("S", "two again", "", nil, record)
	h.loop.runUntil(t, func() bool { return len(outcomes) > 0 })
	h.loop.settle(t, 200*time.Millisecond)
	if len(outcomes) != 1 || !sameOutcome(outcomes[0], Outcome{Kind: OutcomeAnswered, Text: "done"}) {
		t.Errorf("outcomes %+v", outcomes)
	}
	prompts := fake.prompts()
	if prompts[0] != "one" || prompts[len(prompts)-1] != "two again" || slices.Contains(prompts, "two") {
		t.Errorf("prompts %q", prompts)
	}
}

// The rewrite streams its cleaned answer and ends with it.
func TestRewriteRunsAndCleans(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{
		fakeInit, fakeDelta(`` + "```" + `\n„Dobrý`), fakeDelta(` den.“\n` + "```"), fakeText("```" + `\n„Dobrý den.“\n` + "```"),
		fakeResult("```"+`\n„Dobrý den.“\n`+"```", true),
	}})
	h := newRequestHarness(t, fake, true, true)
	w := NewRewriter(tr, h.request)
	var states []RewriteState
	w.OnState = func(s RewriteState) { states = append(states, s) }
	if !w.Start(assistant.Politer, "", "  Ahoj.\n") || !w.Running() {
		t.Fatal("rewrite did not start")
	}
	h.loop.runUntil(t, func() bool { return !w.Running() })
	done := RewriteState{Kind: RewriteDone, Text: "Dobrý den."}
	if w.State() != done || states[0] != (RewriteState{Kind: RewriteRunning}) || states[len(states)-1] != done ||
		!slices.Contains(states, RewriteState{Kind: RewriteRunning, Text: "```\n„Dobrý"}) {
		t.Errorf("states %+v", states)
	}
	msg, _ := assistant.RewriteMessage(assistant.Politer, "", "Ahoj.")
	if !slices.Equal(fake.prompts(), []string{msg}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	if want := assistant.Args(assistant.Options{Model: assistant.Haiku, SystemPrompt: assistant.RewriteSystemPrompt()}); !slices.Equal(fake.args(), want) {
		t.Errorf("args %q", fake.args())
	}
}

// Nothing to ask is refused; too long fails at once; an empty answer is a
// failure, never an empty replacement.
func TestRewriteRefusesAndFails(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit, fakeResult(`  \"\"  `, true)}})
	h := newRequestHarness(t, fake, true, true)
	w := NewRewriter(tr, h.request)
	if w.Start(assistant.Fix, "", " \n ") || w.Start(assistant.Custom, "  ", "text") || w.State() != (RewriteState{}) {
		t.Error("nothing to ask was taken")
	}
	if !w.Start(assistant.Fix, "", strings.Repeat("a", assistant.MaxPassage+1)) {
		t.Fatal("too long refused")
	}
	if want := "The assistant stopped: assistant: the passage is too long: 20001 characters, at most 20000"; w.State() != (RewriteState{Kind: RewriteFailed, Text: want}) || fake.starts() != 0 {
		t.Errorf("state %+v", w.State())
	}
	if !w.Start(assistant.Custom, "Make it shorter", "text") {
		t.Fatal("custom refused")
	}
	h.loop.runUntil(t, func() bool { return !w.Running() })
	if w.State() != (RewriteState{Kind: RewriteFailed, Text: "The assistant stopped: the answer is empty"}) {
		t.Errorf("state %+v", w.State())
	}
	if !slices.Equal(fake.prompts(), []string{"Follow this instruction: Make it shorter\n\nPassage:\n<<<\ntext\n>>>"}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	w.Cancel()
	if w.State() != (RewriteState{}) {
		t.Errorf("state after Cancel %+v", w.State())
	}
}

func TestRewriteErrorsAndDeclinedConsent(t *testing.T) {
	missing := newRequestHarness(t, newFakeClaude(t, "true", "", answerTurn("x")), true, false)
	w := NewRewriter(tr, missing.request)
	w.Start(assistant.Shorter, "", "text")
	missing.loop.runUntil(t, func() bool { return !w.Running() })
	if w.State() != (RewriteState{Kind: RewriteFailed, Text: "Claude Code was not found on this computer"}) {
		t.Errorf("state %+v", w.State())
	}
	asked := newRequestHarness(t, newFakeClaude(t, "true", "", answerTurn("x")), false, true)
	asked.consentAnswer = false
	declined := NewRewriter(tr, asked.request)
	declined.Start(assistant.Shorter, "", "text")
	asked.loop.runUntil(t, func() bool { return !declined.Running() })
	if declined.State() != (RewriteState{}) || asked.consentAsked != 1 {
		t.Errorf("state %+v, asked %d", declined.State(), asked.consentAsked)
	}
}

// Cancelled while running: idle, and the late answer is dropped.
func TestRewriteCancelled(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{fakeInit, fakeDelta("x")}, shell: "sleep 1; echo '" + fakeResult("late", true) + "'"})
	h := newRequestHarness(t, fake, true, true)
	w := NewRewriter(tr, h.request)
	w.Start(assistant.Fix, "", "text")
	h.loop.runUntil(t, func() bool { return w.State() == RewriteState{Kind: RewriteRunning, Text: "x"} })
	w.Cancel()
	if w.State() != (RewriteState{}) {
		t.Errorf("state %+v", w.State())
	}
	h.loop.settle(t, 1500*time.Millisecond)
	if w.State() != (RewriteState{}) {
		t.Errorf("late state %+v", w.State())
	}
}

func TestSearchConverts(t *testing.T) {
	fake := newFakeClaude(t, "true", "",
		fakeTurn{lines: []string{fakeInit, structuredResult(`{"query":"from:jan  faktur\nafter:2026-03-01"}`, "")}},
		// No structured_output: the text is read the same way.
		fakeTurn{lines: []string{fakeInit, fakeResult(`{\"query\":\"is:unread\"}`, true)}},
		fakeTurn{lines: []string{fakeInit, structuredResult(`{"q":"x"}`, "no JSON")}})
	h := newRequestHarness(t, fake, true, true)
	s := NewSearcher(tr, h.request)
	s.Today = func() string { return "2026-09-29" }
	var got []SearchOutcome
	record := func(o SearchOutcome) { got = append(got, o) }
	if !s.Convert("  faktury od Jany z března  ", record) || !s.Running() {
		t.Fatal("conversion did not start")
	}
	h.loop.runUntil(t, func() bool { return len(got) == 1 })
	if got[0] != (SearchOutcome{Kind: SearchQuery, Text: "from:jan faktur after:2026-03-01"}) {
		t.Errorf("outcome %+v", got[0])
	}
	if !slices.Equal(fake.prompts(), []string{"faktury od Jany z března"}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	// The fake writes one argument per line, and the prompt has lines.
	want := assistant.Args(assistant.Options{Model: assistant.Haiku, SystemPrompt: assistant.SearchSystemPrompt("2026-09-29"), JSONSchema: assistant.SearchSchema})
	if strings.Join(fake.args(), "\n") != strings.Join(want, "\n") {
		t.Errorf("args %q", fake.args())
	}
	s.Convert("unread", record)
	h.loop.runUntil(t, func() bool { return len(got) == 2 })
	if got[1] != (SearchOutcome{Kind: SearchQuery, Text: "is:unread"}) {
		t.Errorf("outcome %+v", got[1])
	}
	s.Convert("x", record)
	h.loop.runUntil(t, func() bool { return len(got) == 3 })
	if got[2] != (SearchOutcome{Kind: SearchFailed, Text: "The search could not be converted: the answer holds no query"}) {
		t.Errorf("outcome %+v", got[2])
	}
}

func TestSearchRefusesAndFails(t *testing.T) {
	missing := newRequestHarness(t, newFakeClaude(t, "true", "", answerTurn("x")), true, false)
	s := NewSearcher(tr, missing.request)
	var got []SearchOutcome
	record := func(o SearchOutcome) { got = append(got, o) }
	if s.Convert("   ", record) {
		t.Error("no words taken")
	}
	if !s.Convert(strings.Repeat("a", assistant.MaxSearchWords+1), record) {
		t.Error("too many words refused")
	}
	if len(got) != 0 {
		t.Error("completion called inside Convert")
	}
	missing.loop.runUntil(t, func() bool { return len(got) == 1 })
	if got[0].Text != "The search could not be converted: assistant: the words are too long: 501 characters, at most 500" {
		t.Errorf("outcome %+v", got[0])
	}
	s.Convert("faktury", record)
	missing.loop.runUntil(t, func() bool { return len(got) == 2 })
	if got[1].Text != "The search could not be converted: Claude Code was not found on this computer" {
		t.Errorf("outcome %+v", got[1])
	}
	signedOut := newRequestHarness(t, newFakeClaude(t, "false", "", answerTurn("x")), true, true)
	other := NewSearcher(tr, signedOut.request)
	other.Convert("faktury", record)
	signedOut.loop.runUntil(t, func() bool { return len(got) == 3 })
	if got[2].Text != "The search could not be converted: Not signed in: run claude in Terminal and sign in" {
		t.Errorf("outcome %+v", got[2])
	}
}

func TestSearchOutcomes(t *testing.T) {
	for _, c := range []struct {
		name string
		in   Outcome
		want SearchOutcome
	}{
		{"declined", Outcome{Kind: OutcomeDeclined}, SearchOutcome{Kind: SearchDeclined}},
		{"failed", failed(FailureStopped, "API Error: 500\nmore"), SearchOutcome{Kind: SearchFailed, Text: "The search could not be converted: API Error: 500"}},
		{"structured", Outcome{Kind: OutcomeAnswered, Structured: []byte(`{"query":" x "}`)}, SearchOutcome{Kind: SearchQuery, Text: "x"}},
		{"the structured answer wins", Outcome{Kind: OutcomeAnswered, Text: `{"query":"text"}`, Structured: []byte(`{"query":"structured"}`)}, SearchOutcome{Kind: SearchQuery, Text: "structured"}},
		{"an empty structured answer", Outcome{Kind: OutcomeAnswered, Text: `{"query":"text"}`, Structured: []byte(`{"query":""}`)}, SearchOutcome{Kind: SearchQuery, Text: "text"}},
		{"nothing", Outcome{Kind: OutcomeAnswered}, SearchOutcome{Kind: SearchFailed, Text: "The search could not be converted: the answer holds no query"}},
	} {
		if got := searchOutcome(tr, c.in); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
