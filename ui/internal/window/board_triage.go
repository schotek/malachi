// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"
	"log/slog"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/boardtriage"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The board's triage (ui/internal/boardtriage; docs/mcp.md "The board's
// triage run in the app") for the whole application: one Preferences (the
// board's preferences in the daemon), one Controller (the run) and one
// Scheduler (automatic triage), made once the RPC client exists
// (Assistant.AttachBoardTriage, from main.go's startup) and stopped before
// it closes (Assistant.StopBoardTriage, from shutdown). The macOS client
// leads (AppState.wireBoardTriage); this is its port.
//
// What the controller needs is fed here: the consent keys
// (assistant-consent, board-triage-consent) as SettingsChanged; the
// Assistant's changes (its status, the handlers, assistant-menu,
// assistant-target) and assistant-claude-path as AvailabilityChanged; the
// connection's state as Preferences.ConnectionChanged. Triage is available
// with the Assistant shown and the In App target (triageAvailable). A
// refused write of the preferences is a toast: in the Preferences dialog
// while it is open (showToastsIn), else on the main window, else logged. A
// manual run without consent asks with "Let the Assistant Triage the
// Board?" on the main window (widget.AskTriageConsent).
//
// What the board page feeds and uses (board.go): BoardChanged with
// every snapshot of its source, SetOnRefresh with the source's list again
// (after a run, and whenever Preferences → AI comes up), Controller for
// the Triage control and the status strip (View, Observe, Start, Cancel,
// WantsBoardData) and OnManualRunEnded for the toast of a manual run.
//
// Nothing a run's assistant writes reaches this file: only the
// controller's counts and classes.

// boardConnectionPollSeconds is how often the connection's state is looked
// at for the board's preferences (ConnectionChanged): the window owns the
// client's only state callback, so the application follows the state by
// asking for it. The window may feed ConnectionChanged itself as well; a
// state already known changes nothing.
const boardConnectionPollSeconds = 1

// tokenDigitSeparator groups the digits of token counts in Preferences →
// AI → Board ("1 234 567" with a narrow no-break space): the UI has no
// locale-aware number formatting, and this grouping reads in every
// language without a msgid of its own.
const tokenDigitSeparator = "\u202f"

// formatTokens is a count of tokens with its digits grouped
// (boardtriage.Config.Number).
func formatTokens(n int64) string { return boardtriage.GroupDigits(n, tokenDigitSeparator) }

// BoardTriage is the application's board triage (see the comment above).
// Main loop only.
type BoardTriage struct {
	prefs     *boardtriage.Preferences
	ctl       *boardtriage.Controller
	scheduler *boardtriage.Scheduler

	rpc *client.Client
	log *slog.Logger
	// mainWindow is the main window, nil while there is none.
	mainWindow func() *Window
	// toastHere shows the toasts while the Preferences dialog is open.
	toastHere func(string)
	// onRefresh is the board page's list again (SetOnRefresh).
	onRefresh func()

	// connected is the connection's state as last fed.
	connected bool
	poll      glib.SourceHandle
	removes   []func()
	stopped   bool
}

// AttachBoardTriage makes the application's board triage over rpc, once:
// the preferences, the controller (its sign-in asked once) and the
// automatic schedule, started; mainWindow (may be nil, and may return nil)
// is the main window for the consent sheet and the toasts. Call it once
// the client exists, from the application's startup; a second call
// returns the first triage.
func (a *Assistant) AttachBoardTriage(rpc *client.Client, mainWindow func() *Window) *BoardTriage {
	if a.board != nil {
		return a.board
	}
	b := &BoardTriage{rpc: rpc, log: a.log.With("part", "board-triage"), mainWindow: mainWindow}
	b.prefs = boardtriage.NewPreferences(boardtriage.PreferencesConfig{
		Caller: rpc, Loop: glibLoop{}, Log: b.log, Translator: i18n.Tr,
		Disconnected: func(err error) bool { return errors.Is(err, client.ErrDisconnected) },
	})
	b.prefs.OnError = b.toast
	b.ctl = boardtriage.New(boardtriage.Config{
		Caller: rpc, Settings: a.settings, Locator: a.locator, Preferences: b.prefs,
		// A request of its own, not the rewrite's or the search's.
		Request: a.NewRequest(), Loop: glibLoop{}, Log: b.log, Translator: i18n.Tr,
		Bridge: a.bridge, Socket: rpc.Socket, Available: a.triageAvailable, Number: formatTokens,
	})
	b.ctl.Language = uiLanguage
	b.ctl.Consent = b.askConsent
	b.ctl.OnRefresh = b.refresh
	s := a.settings
	b.removes = append(b.removes,
		s.OnChanged(settings.KeyAssistantConsent, b.ctl.SettingsChanged),
		s.OnChanged(settings.KeyBoardTriageConsent, b.ctl.SettingsChanged),
		// Another claude: where Claude Code is, and its sign-in, may differ.
		s.OnChanged(settings.KeyAssistantClaudePath, b.ctl.AvailabilityChanged),
		// The status, the handlers, assistant-menu and assistant-target.
		a.OnChange(b.ctl.AvailabilityChanged),
	)
	b.ctl.CheckSignIn()
	b.scheduler = boardtriage.NewScheduler(b.ctl, glibLoop{}, b.log)
	b.scheduler.Start()
	b.ConnectionChanged(rpc.State() == client.Connected)
	b.poll = glib.TimeoutSecondsAdd(boardConnectionPollSeconds, func() bool {
		if b.stopped {
			b.poll = 0
			return false
		}
		b.ConnectionChanged(b.rpc.State() == client.Connected)
		return true
	})
	a.board = b
	return b
}

// BoardTriage is the application's board triage; nil before
// AttachBoardTriage.
func (a *Assistant) BoardTriage() *BoardTriage { return a.board }

// triageAvailable says whether the board's triage is available: the
// Assistant shown with the In App target (boardtriage.TriageAvailable).
func (a *Assistant) triageAvailable() bool {
	return boardtriage.TriageAvailable(a.shown(), a.target())
}

// StopBoardTriage ends the board's triage as the application quits, before
// the client closes: the schedule stops, a run under way ends as cancelled
// and board.runEnd is waited for, at most boardtriage.EndWait (it blocks
// the main loop that long at most), then nothing is followed any more.
func (a *Assistant) StopBoardTriage() {
	b := a.board
	if b == nil || b.stopped {
		return
	}
	b.stopped = true
	if b.poll != 0 {
		glib.SourceRemove(b.poll)
		b.poll = 0
	}
	b.scheduler.Stop()
	b.ctl.CancelAndEnd(boardtriage.EndWait)
	b.ctl.Close()
	for _, r := range b.removes {
		r()
	}
	b.removes = nil
}

// Controller is the triage run: its View (the Triage control, the status
// strip), Observe, Start(boardtriage.Manual, 0), Cancel, WantsBoardData.
func (b *BoardTriage) Controller() *boardtriage.Controller { return b.ctl }

// Preferences are the board's preferences in the daemon, for the whole
// application.
func (b *BoardTriage) Preferences() *boardtriage.Preferences { return b.prefs }

// SetOnRefresh sets what lists the board again (the board page's source):
// the controller asks after every run's board.runEnd, and RelistBoard
// whenever Preferences → AI comes up (the tokens of the last 24 hours age
// out without a notification). nil: nothing to list.
func (b *BoardTriage) SetOnRefresh(f func()) { b.onRefresh = f }

func (b *BoardTriage) refresh() {
	if b.onRefresh != nil {
		b.onRefresh()
	}
}

// BoardChanged takes every snapshot of the board page's source (its
// DaemonSource.OnSnapshot): the triage's queue and counts, the board's
// assistant preference and the daemon's last run, which the schedule and
// the status strip need. Snapshots of the sample board
// (MALACHI_BOARD_SAMPLES) are not the daemon's and never come here.
func (b *BoardTriage) BoardChanged(s board.Snapshot) {
	b.ctl.BoardChanged(triageSnapshot(s))
}

// ConnectionChanged says the connection to the daemon came or went: the
// board's preferences are asked for again, or the replies on their way
// dropped. A state already known changes nothing, so the window and the
// poll may both feed it.
func (b *BoardTriage) ConnectionChanged(connected bool) {
	if b.stopped || connected == b.connected {
		return
	}
	b.connected = connected
	b.prefs.ConnectionChanged(connected)
}

// OnManualRunEnded calls f with the result of every manual run that ended
// (the view's Result: what it did, or why it failed), for a toast; not
// for a declined consent (the user just said no), never for an automatic
// run (the status strip says enough). Until the returned function is
// called.
func (b *BoardTriage) OnManualRunEnded(f func(text string)) (remove func()) {
	return b.ctl.ObserveEnded(func() {
		e, ok := b.ctl.LastEnded()
		if !ok || !manualRunToasts(e) {
			return
		}
		// The state still says the run is active here; the result is the
		// view's once the state changed, right after.
		glib.IdleAdd(func() {
			if text := b.ctl.View().Result; text != "" {
				f(text)
			}
		})
	})
}

// manualRunToasts says whether the end of run e is a toast: a manual run's,
// unless the user declined the consent.
func manualRunToasts(e boardtriage.Ended) bool {
	return e.Trigger == boardtriage.Manual && !(e.Failed && e.Failure == board.FailDeclined)
}

// showToastsIn shows the toasts with f (the Preferences dialog's) until
// the returned function is called.
func (b *BoardTriage) showToastsIn(f func(string)) (remove func()) {
	b.toastHere = f
	return func() { b.toastHere = nil }
}

// toast shows text where the user looks: the Preferences dialog while it
// is open, else the main window; with neither, it is logged (a sentence of
// the catalog with a reason, never mail).
func (b *BoardTriage) toast(text string) {
	if b.toastHere != nil {
		b.toastHere(text)
		return
	}
	if w := b.window(); w != nil {
		w.Toast(text)
		return
	}
	b.log.Info("board triage", "message", text)
}

// window is the main window, nil while there is none.
func (b *BoardTriage) window() *Window {
	if b.mainWindow == nil {
		return nil
	}
	return b.mainWindow()
}

// askConsent is the controller's Consent: the sheet on the main window (a
// manual run starts there), or a window of its own without one.
func (b *BoardTriage) askConsent(done func(bool)) {
	var parent gtk.Widgetter
	if w := b.window(); w != nil {
		parent = w
	}
	widget.AskTriageConsent(parent, done)
}

// triageSnapshot is what a snapshot of the board says about the triage, as
// the controller takes it: the board.list's phase, assistant preference
// and triage part, with its last run as the daemon reported it (the
// source's convertRun turned around).
func triageSnapshot(s board.Snapshot) boardtriage.Snapshot {
	return boardtriage.Snapshot{
		Phase:     s.Phase,
		Assistant: s.Annotated,
		Triage: api.BoardTriage{
			LastRun:            apiRun(s.Run),
			AnnotatedTodayAuto: s.Triage.AnnotatedToday,
			Queue:              s.Triage.Queue,
			Usage24h:           s.Triage.Usage24h,
		},
	}
}

// apiRun is the daemon's run behind the board's r: started at r.Started,
// ended at r.Date unless it runs; nil for none.
func apiRun(r *board.Run) *api.BoardRun {
	if r == nil {
		return nil
	}
	out := &api.BoardRun{
		At: r.Started, Trigger: api.BoardTrigger(r.Trigger), Source: r.Model, Annotated: r.Annotated,
		Error: api.BoardRunError(r.Error),
	}
	if !r.Running {
		ended := r.Date
		out.EndedAt = &ended
	}
	return out
}
