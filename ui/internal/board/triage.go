// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"fmt"
	"time"
)

// The texts of the board's triage run (BoardTriageText.swift): its
// control, progress, outcome, the status strip, automatic triage's pause,
// the consent sheet and Settings → AI's Board group. The buttons that
// exist already (Stop, Sign In…, Get Claude Code…, Allow, Cancel) are the
// assistant panel's (ui/internal/assistant).

// TriageToolTip is the Triage button's tooltip.
func TriageToolTip(tr Translator) string {
	return tr.T("Ask the assistant to read the conversations that wait for it and add notes")
}

// TriageStopToolTip is the button's tooltip while a run works.
func TriageStopToolTip(tr Translator) string { return tr.T("Stop the assistant’s triage") }

// TriageNeedsClaudeCode is the button's tooltip without Claude Code.
func TriageNeedsClaudeCode(tr Translator) string {
	return tr.T("The triage runs your Claude Code, which was not found on this computer")
}

// TriageNeedsSignIn is the button's tooltip while Claude Code is signed
// out (the assistant panel's msgid).
func TriageNeedsSignIn(tr Translator) string { return tr.T("Claude Code is not signed in") }

// TriageStarting is the progress while the run starts.
func TriageStarting(tr Translator) string { return tr.T("Starting the triage…") }

// TriageProgress is "Triaging… 3 of 12" ("Triaging…" without a total).
func TriageProgress(done, total int, tr Translator) string {
	if total <= 0 {
		return tr.T("Triaging…")
	}
	// TRANSLATORS: the progress of the assistant's triage: conversations
	// done of all.
	return fmt.Sprintf(tr.T("Triaging… %d of %d"), min(done, total), total)
}

// TriageRunningLine is the strip while a run works: "The assistant is
// triaging the board… 3 of 12" (without the counts while it starts).
func TriageRunningLine(done, total int, tr Translator) string {
	if total <= 0 {
		return tr.T("The assistant is triaging the board…")
	}
	// TRANSLATORS: conversations done of all.
	return fmt.Sprintf(tr.T("The assistant is triaging the board… %d of %d"), min(done, total), total)
}

// TriageFinished says how a run ended well; refused > 0 adds
// TriageRefused as a second sentence.
func TriageFinished(n, refused int, tr Translator) string {
	var done string
	if n < 1 {
		done = tr.T("Triage finished. No conversation needed new notes.")
	} else {
		done = fmt.Sprintf(tr.N("Triage finished: %d conversation refined.", "Triage finished: %d conversations refined.", n), n)
	}
	if refused <= 0 {
		return done
	}
	return done + " " + TriageRefused(refused, tr)
}

// TriageRefused is the sentence after a finished run's line when the
// board refused notes.
func TriageRefused(n int, tr Translator) string {
	return fmt.Sprintf(tr.N("The board refused %d of the assistant’s notes.", "The board refused %d of the assistant’s notes.", n), n)
}

// TriagedToday is the cases automatic runs triaged today, for Settings'
// status row.
func TriagedToday(n int, tr Translator) string {
	return fmt.Sprintf(tr.N("%d conversation triaged automatically today", "%d conversations triaged automatically today", n), n)
}

// TriageWaiting is the cases the assistant has not triaged yet
// (board.list triage.queue), a segment of the status line after its " · ".
func TriageWaiting(n int, tr Translator) string {
	// TRANSLATORS: a segment of the board's status line after " · ", such
	// as "Triaged by rules · refined by the assistant 5 minutes ago · 3
	// conversations wait for the assistant": conversations it has not
	// triaged yet.
	return fmt.Sprintf(tr.N("%d conversation waits for the assistant", "%d conversations wait for the assistant", n), n)
}

// TriageStopped is the outcome of a run the user stopped.
func TriageStopped(tr Translator) string { return tr.T("Triage stopped.") }

// TriageFailure is why a run failed (Board.TriageFailure).
type TriageFailure int

// The failures.
const (
	FailNotSignedIn TriageFailure = iota
	FailNotFound
	FailToolsMissing
	FailTimeout
	FailCancelled
	FailDeclined
	FailAssistantOff
	FailBackend
	FailStopped
	FailNothingToDo
	FailNotesRefused
	FailNoProgress
	// FailLimit: the assistant's usage limit (of the user's plan) was
	// reached.
	FailLimit
)

// TriageFailures lists every failure.
var TriageFailures = []TriageFailure{
	FailNotSignedIn, FailNotFound, FailToolsMissing, FailTimeout, FailCancelled,
	FailDeclined, FailAssistantOff, FailBackend, FailStopped, FailNothingToDo,
	FailNotesRefused, FailNoProgress, FailLimit,
}

// TriageFailed is how a run failed: "Triage failed: Claude Code is not
// signed in."
func TriageFailed(f TriageFailure, tr Translator) string {
	// TRANSLATORS: %s is the reason, such as "it took too long".
	return fmt.Sprintf(tr.T("Triage failed: %s."), TriageFailureText(f, tr))
}

// TriageFailureText is why a run failed, inside a sentence (also a
// tooltip on its own).
func TriageFailureText(f TriageFailure, tr Translator) string {
	switch f {
	case FailNotSignedIn:
		return TriageNeedsSignIn(tr)
	case FailNotFound:
		return tr.T("Claude Code was not found")
	case FailToolsMissing:
		return tr.T("the Malachi Mail tools are not available to the assistant")
	case FailTimeout:
		return tr.T("it took too long")
	case FailCancelled:
		return tr.T("it was stopped")
	case FailDeclined:
		return tr.T("sending mail to the assistant was not allowed")
	case FailAssistantOff:
		return tr.T("the assistant is off")
	case FailBackend:
		return tr.T("the mail backend did not answer")
	case FailStopped:
		return tr.T("the assistant stopped")
	case FailNothingToDo:
		return tr.T("no conversation waits for the assistant")
	case FailNotesRefused:
		return tr.T("the board refused the assistant’s notes")
	case FailNoProgress:
		return tr.T("the assistant added no notes")
	case FailLimit:
		// TRANSLATORS: why a triage or a suggested reply failed, inside a
		// sentence such as "Triage failed: %s.": the user's plan allows no
		// more use of the assistant for now.
		return tr.T("the assistant’s usage limit was reached")
	}
	return ""
}

// TriageStatusLine is the status strip when no run works: who sorted the
// board, and when the assistant last refined it (lastRun nil: never or
// still running).
func TriageStatusLine(assistantOn bool, lastRun *time.Time, now time.Time, tr Translator) string {
	if !assistantOn {
		return assistantOffLine(tr)
	}
	if lastRun == nil {
		return tr.T("Triaged by rules · not refined by the assistant yet")
	}
	// TRANSLATORS: %s is when, such as "5 minutes ago" or "yesterday".
	return fmt.Sprintf(tr.T("Triaged by rules · refined by the assistant %s"), RelativeTime(*lastRun, now, tr))
}

// PauseKind is why automatic triage pauses (Board.AutoTriagePause).
type PauseKind int

// The pauses.
const (
	// PauseFailed: the last automatic runs failed; the next try waits.
	PauseFailed PauseKind = iota
	PauseSignedOut
	PauseUnavailable
	PauseNoConsent
)

// Pause is why automatic triage pauses; Failure and Until count for
// PauseFailed only.
type Pause struct {
	Kind    PauseKind
	Failure TriageFailure
	Until   time.Time
}

// AutoTriagePaused is "Automatic triage paused: …".
func AutoTriagePaused(p Pause, now time.Time, tr Translator) string {
	var why string
	switch p.Kind {
	case PauseFailed:
		// TRANSLATORS: the first %s is the reason, such as "it took too
		// long", the second when the next try comes, such as "in 5 minutes".
		return fmt.Sprintf(tr.T("Automatic triage paused: %s · next try %s"),
			TriageFailureText(p.Failure, tr), RelativeFuture(p.Until, now, tr))
	case PauseSignedOut:
		why = TriageNeedsSignIn(tr)
	case PauseUnavailable:
		why = tr.T("the assistant cannot run")
	case PauseNoConsent:
		why = tr.T("sending mail to the assistant is not allowed")
	}
	// TRANSLATORS: %s is the reason, such as "the assistant cannot run".
	return fmt.Sprintf(tr.T("Automatic triage paused: %s"), why)
}

// TriageConsentHeading and TriageConsentBody are the consent sheet before
// the first triage (its buttons are the panel's Allow and Cancel).
func TriageConsentHeading(tr Translator) string { return tr.T("Let the Assistant Triage the Board?") }

// TriageConsentBody: see TriageConsentHeading.
func TriageConsentBody(tr Translator) string {
	return tr.T("The assistant reads the conversations on the board that need notes, and any other mail and attachments it needs to understand them, and sends their text to Anthropic through your Claude Code, under your Claude account. It adds titles, summaries, tasks, deadlines and suggested replies to the board, and a triage you start yourself may also write replies, which stay on the board in Malachi Mail, not in your Drafts folder, until you send them. It cannot send, move or delete mail, and messages may contain instructions from their senders that it is told not to follow. Which accounts it triages, and whether it runs at all, you choose in Settings.")
}

// Settings → AI, the Board group.

// TriageSettingsConsent is the consent switch's row.
func TriageSettingsConsent(tr Translator) string { return tr.T("Let the assistant refine the board") }

// TriageSettingsConsentSubtitle says what the consent sheet says.
func TriageSettingsConsentSubtitle(tr Translator) string {
	return tr.T("Sends the newest messages of conversations that need sorting to Anthropic through your Claude Code. It cannot send, move or delete mail; a triage you start yourself may write replies, which stay on the board until you send them.")
}

// TriageSettingsAccounts heads the list of the accounts the triage reads
// (board preferences triageAccounts; none chosen = every account).
func TriageSettingsAccounts(tr Translator) string {
	// TRANSLATORS: Settings → AI → Board: a list of the user's accounts
	// with a check box each; the assistant triages only the checked ones.
	return tr.T("Triage These Accounts")
}

// TriageSettingsAccountsAll is that list's line while no account is
// checked: then every account is triaged.
func TriageSettingsAccountsAll(tr Translator) string {
	// TRANSLATORS: under "Triage These Accounts" when none is checked.
	return tr.T("All accounts, while none is checked")
}

// TriageSettingsAutomatic is the row of automatic runs.
func TriageSettingsAutomatic(tr Translator) string { return tr.T("Triage new mail automatically") }

// TriageSettingsInterval is the row of the interval between automatic
// runs; its value is TriageInterval.
func TriageSettingsInterval(tr Translator) string {
	// TRANSLATORS: a row followed by a choice such as "15 minutes" or
	// "1 hour": automatic triage runs at most this often.
	return tr.T("At most every")
}

// TriageSettingsDaily is the row of the daily cap; its value is
// TriageDailyCap.
func TriageSettingsDaily(tr Translator) string {
	// TRANSLATORS: a row followed by a choice such as "Up to 60": how many
	// conversations automatic triage sends to the assistant a day.
	return tr.T("Conversations a day")
}

// TriageSettingsNeedsClaudeCode is the group's description without
// Claude Code.
func TriageSettingsNeedsClaudeCode(tr Translator) string {
	return tr.T("The triage runs your Claude Code, which was not found on this computer. The Claude Code row above offers to get it.")
}

// TriageSettingsNeedsSignIn is the group's description while Claude Code
// is signed out.
func TriageSettingsNeedsSignIn(tr Translator) string {
	return tr.T("Claude Code is not signed in. The Claude Code row above offers to sign in.")
}

// TriageSettingsNoTools is the group's description without the bridge.
func TriageSettingsNoTools(tr Translator) string {
	return tr.T("The Malachi Mail tools are not available to the assistant, so the board cannot be triaged.")
}

// TriageSettingsNoBackend is the group's description without the board's
// preferences.
func TriageSettingsNoBackend(tr Translator) string {
	return tr.T("The mail backend did not answer with the board’s settings, so they cannot be changed now.")
}

// TriageInterval is a value of "At most every": "15 minutes", "1 hour",
// "3 hours".
func TriageInterval(minutes int, tr Translator) string {
	if minutes >= 60 && minutes%60 == 0 {
		h := minutes / 60
		// TRANSLATORS: a value after "At most every".
		return fmt.Sprintf(tr.N("%d hour", "%d hours", h), h)
	}
	// TRANSLATORS: a value after "At most every".
	return fmt.Sprintf(tr.N("%d minute", "%d minutes", minutes), minutes)
}

// TriageDailyCap is a value of "Conversations a day": "Up to 60"; 0 or
// less is "None".
func TriageDailyCap(cases int, tr Translator) string {
	if cases <= 0 {
		// TRANSLATORS: a value of "Conversations a day": automatic triage
		// sends none.
		return tr.C("daily cap", "None")
	}
	// TRANSLATORS: a value of "Conversations a day"; %d is the most.
	return fmt.Sprintf(tr.T("Up to %d"), cases)
}

// TriageSettingsUsage is the row of the tokens the board's triage runs
// used in the last 24 hours; its value is their sum, a number formatted
// for the locale, or TriageUsageNone.
func TriageSettingsUsage(tr Translator) string { return tr.T("Tokens in the Last 24 Hours") }

// TriageUsageNone is that row's value when no run of the last 24 hours
// reported its tokens.
func TriageUsageNone(tr Translator) string {
	// TRANSLATORS: the value of "Tokens in the Last 24 Hours" when no
	// triage run reported any.
	return tr.C("token usage", "None")
}

// TriageUsageSplit is that row's first line of detail: the tokens by kind,
// each a number formatted for the locale ("12,345").
func TriageUsageSplit(input, output, cacheWrite, cacheRead string, tr Translator) string {
	// TRANSLATORS: the tokens the board's triage used in the last 24 hours,
	// by kind: sent to the model, written by it, written to the prompt
	// cache, read from the prompt cache. Each %s is a number such as
	// "12,345".
	return fmt.Sprintf(tr.T("Input %s · output %s · written to cache %s · read from cache %s"), input, output, cacheWrite, cacheRead)
}

// TriageUsageRuns is that row's second line of detail: how many triage
// runs the tokens come from.
func TriageUsageRuns(runs int, tr Translator) string {
	// TRANSLATORS: under the tokens of the last 24 hours: the number of
	// the assistant's triage runs they come from.
	return fmt.Sprintf(tr.N("From %d triage run", "From %d triage runs", runs), runs)
}

// TriageUsageAtLeast is that row's value when a run summed in it reported
// only part of its tokens (api.BoardUsage.LowerBound): "at least 12,345".
func TriageUsageAtLeast(value string, tr Translator) string {
	// TRANSLATORS: the value of "Tokens in the Last 24 Hours" when a run
	// was stopped before it reported all its tokens; %s is a number such
	// as "12,345".
	return fmt.Sprintf(tr.T("at least %s"), value)
}

// UsageText is that row's value: the sum (a number formatted for the
// locale), with TriageUsageAtLeast when it is a lower bound.
func UsageText(total string, lowerBound bool, tr Translator) string {
	if lowerBound {
		return TriageUsageAtLeast(total, tr)
	}
	return total
}

// TriageUsageToolTip is that row's tooltip: whose runs count.
func TriageUsageToolTip(tr Translator) string {
	return tr.T("Counts only the triage runs Malachi Mail started, not those of other assistants")
}

// RelativeTime is "just now", "5 minutes ago", "2 hours ago" (under a
// day), else by calendar days in now's zone: "yesterday", "3 days ago".
func RelativeTime(date, now time.Time, tr Translator) string {
	s := max(0, int(now.Sub(date)/time.Second))
	switch {
	case s < 60:
		return tr.T("just now")
	case s < 3600:
		return fmt.Sprintf(tr.N("%d minute ago", "%d minutes ago", s/60), s/60)
	case s < 86400:
		return fmt.Sprintf(tr.N("%d hour ago", "%d hours ago", s/3600), s/3600)
	}
	days := max(1, dayDifference(date, now, now.Location()))
	if days == 1 {
		// TRANSLATORS: when the assistant last refined the board.
		return tr.T("yesterday")
	}
	return fmt.Sprintf(tr.N("%d day ago", "%d days ago", days), days)
}

// RelativeFuture is "now", "in 5 minutes", "in 2 hours" (under a day),
// else by calendar days in now's zone: "tomorrow", "in 3 days".
func RelativeFuture(date, now time.Time, tr Translator) string {
	s := max(0, int(date.Sub(now)/time.Second))
	switch {
	case s < 60:
		// TRANSLATORS: when automatic triage tries again: at once.
		return tr.T("now")
	case s < 3600:
		return fmt.Sprintf(tr.N("in %d minute", "in %d minutes", s/60), s/60)
	case s < 86400:
		return fmt.Sprintf(tr.N("in %d hour", "in %d hours", s/3600), s/3600)
	}
	days := max(1, dayDifference(now, date, now.Location()))
	if days == 1 {
		// TRANSLATORS: when automatic triage tries again.
		return tr.T("tomorrow")
	}
	return fmt.Sprintf(tr.N("in %d day", "in %d days", days), days)
}

// ProviderSwap is what a msgid of this package says about Claude Code that
// a client with another assistant provider (ChatGPT through Codex) says
// in that provider's words (the GTK window's providerBoardTranslator).
type ProviderSwap int

// The swaps.
const (
	// SwapNotFound: Claude Code was not found.
	SwapNotFound ProviderSwap = iota
	// SwapNotSignedIn: Claude Code is not signed in.
	SwapNotSignedIn
	// SwapConsent: the consent's summary in Settings.
	SwapConsent
)

// ProviderSwappedTexts are the msgids of this package a client replaces
// for another provider, by what they say. The translator that replaces
// them matches these; TestProviderSwappedTexts keeps every one a msgid the
// package asks for, so a changed wording fails there and not silently.
func ProviderSwappedTexts() map[string]ProviderSwap {
	r := swapRecorder{}
	TriageFailureText(FailNotFound, r.as(SwapNotFound))
	TriageNeedsClaudeCode(r.as(SwapNotFound))
	TriageSettingsNeedsClaudeCode(r.as(SwapNotFound))
	TriageNeedsSignIn(r.as(SwapNotSignedIn))
	TriageSettingsNeedsSignIn(r.as(SwapNotSignedIn))
	TriageSettingsConsentSubtitle(r.as(SwapConsent))
	return r
}

// swapRecorder notes the msgids asked for under the swap of as.
type swapRecorder map[string]ProviderSwap

func (r swapRecorder) as(k ProviderSwap) Translator { return swapTranslator{r, k} }

type swapTranslator struct {
	r swapRecorder
	k ProviderSwap
}

func (t swapTranslator) T(msgid string) string { t.r[msgid] = t.k; return msgid }
func (t swapTranslator) N(msgid, plural string, n int) string {
	t.r[msgid] = t.k
	if n == 1 {
		return msgid
	}
	return plural
}
func (t swapTranslator) C(_, msgid string) string { t.r[msgid] = t.k; return msgid }
