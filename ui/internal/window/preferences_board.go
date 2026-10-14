// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/boardtriage"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

// Preferences → General → Board (the macOS General pane's Board group):
// Show the Board, Board View, Open at Launch, and how long each state
// keeps a case; and Preferences → AI → Board's Triage These Accounts. The
// texts are ui/internal/board's; Board View and Open at Launch are the
// settings keys board-default-style and board-start-mode, the rest the
// daemon's board preferences (boardtriage.Preferences, optimistic: a
// refused write is taken back with a toast).

// boardWindowsDebounce is how long the windows' rows wait after the last
// step before the daemon is told (one write for a run of clicks).
const boardWindowsDebounce = 600 // milliseconds

// boardDefaultStyleChoices are Board View's values in the order of the
// combo row's items (board.DefaultStyles: Last Used, List, Columns, Today).
func boardDefaultStyleChoices() []settings.BoardStyle {
	out := make([]settings.BoardStyle, len(board.DefaultStyles))
	for i, d := range board.DefaultStyles {
		out[i] = settings.BoardStyle(d.Nick())
	}
	return out
}

// boardStartModeChoices are Open at Launch's values in the order of the
// combo row's items (board.StartModes: Mail, Board, Last Used).
func boardStartModeChoices() []settings.BoardStartMode {
	out := make([]settings.BoardStartMode, len(board.StartModes))
	for i, m := range board.StartModes {
		out[i] = settings.BoardStartMode(m.Nick())
	}
	return out
}

// windowOf is state st's window in w, and withWindow w with it set to n.
func windowOf(w api.BoardWindows, st board.State) int {
	switch st {
	case board.StateHot:
		return w.Hot
	case board.StateYou:
		return w.You
	case board.StateThem:
		return w.Them
	}
	return w.Info
}

func withWindow(w api.BoardWindows, st board.State, n int) api.BoardWindows {
	switch st {
	case board.StateHot:
		w.Hot = n
	case board.StateYou:
		w.You = n
	case board.StateThem:
		w.Them = n
	default:
		w.Info = n
	}
	return w
}

// bindBoard fills Preferences → General → Board and keeps it with the
// settings and the daemon's board preferences. Without the application's
// triage (no preferences object) the daemon's rows hide.
func (d *PreferencesDialog) bindBoard(s *settings.Store) (unbind func()) {
	btr := i18n.Tr
	d.boardGroup.SetTitle(board.BoardName(btr))
	d.boardShow.SetTitle(board.ShowBoardSetting(btr))
	d.boardShow.SetSubtitle(board.ShowBoardSettingSubtitle(btr))
	d.boardDefaultStyle.SetTitle(board.DefaultStyleSetting(btr))
	styles := make([]string, len(board.DefaultStyles))
	for i, st := range board.DefaultStyles {
		styles[i] = board.DefaultStyleTitle(st, btr)
	}
	d.boardDefaultStyle.SetModel(gtk.NewStringList(styles))
	d.boardStartMode.SetTitle(board.StartModeSetting(btr))
	modes := make([]string, len(board.StartModes))
	for i, m := range board.StartModes {
		modes[i] = board.StartModeTitle(m, btr)
	}
	d.boardStartMode.SetModel(gtk.NewStringList(modes))
	d.boardWindowsGroup.SetTitle(board.WindowsSetting(btr))
	d.boardWindowsGroup.SetDescription(board.WindowsSettingSubtitle(btr))
	for i, st := range board.States {
		d.boardWindows[i].SetTitle(board.StateName(st, btr))
	}

	unbinds := []func(){
		bindChoice(s, settings.KeyBoardDefaultStyle, d.boardDefaultStyle, boardDefaultStyleChoices(),
			s.BoardDefaultStyle, s.SetBoardDefaultStyle),
		bindChoice(s, settings.KeyBoardStartMode, d.boardStartMode, boardStartModeChoices(),
			s.BoardStartMode, s.SetBoardStartMode),
	}
	unbindAll := func() {
		for _, f := range unbinds {
			f()
		}
	}

	var p *boardtriage.Preferences
	if d.assist != nil {
		if bt := d.assist.BoardTriage(); bt != nil {
			p = bt.Preferences()
		}
	}
	if p == nil {
		d.boardShow.SetVisible(false)
		d.boardWindowsGroup.SetVisible(false)
		return unbindAll
	}

	var (
		syncing bool
		timer   glib.SourceHandle
	)
	update := func() {
		if d.closed {
			return
		}
		prefs, known := p.Current()
		syncing = true
		defer func() { syncing = false }()
		d.boardShow.SetSensitive(known)
		d.boardShow.SetActive(!known || prefs.Enabled)
		windows := boardtriage.DefaultWindows
		if known && boardtriage.ValidWindows(prefs.Windows) {
			windows = prefs.Windows
		}
		d.boardWindowsGroup.SetSensitive(known && prefs.Enabled)
		for i, st := range board.States {
			n := windowOf(windows, st)
			row := d.boardWindows[i]
			if timer == 0 && int(row.Value()) != n {
				row.SetValue(float64(n))
			}
			row.SetSubtitle(board.Days(int(row.Value()), btr))
		}
	}
	showHandle := d.boardShow.NotifyProperty("active", func() {
		if syncing {
			return
		}
		if d.boardShow.Active() {
			p.SetEnabled(true, nil)
			return
		}
		// Turned off, nothing runs on its own either.
		p.Update(false, func(b *api.BoardPreferences) {
			b.Enabled = false
			b.AutoTriage = false
		}, nil)
	})
	// write tells the daemon what the rows show; final: the dialog is
	// closing, and the last step still goes.
	write := func(final bool) {
		timer = 0
		prefs, known := p.Current()
		if !known || (d.closed && !final) {
			return
		}
		w := prefs.Windows
		if !boardtriage.ValidWindows(w) {
			w = boardtriage.DefaultWindows
		}
		for i, st := range board.States {
			w = withWindow(w, st, int(d.boardWindows[i].Value()))
		}
		if w == prefs.Windows || !p.SetWindows(w, nil) {
			update()
		}
	}
	var handles []glib.SignalHandle
	for i := range board.States {
		row := d.boardWindows[i]
		handles = append(handles, row.NotifyProperty("value", func() {
			row.SetSubtitle(board.Days(int(row.Value()), btr))
			if syncing {
				return
			}
			if timer != 0 {
				glib.SourceRemove(timer)
			}
			timer = glib.TimeoutAdd(boardWindowsDebounce, func() bool {
				write(false)
				return false
			})
		}))
	}
	removePrefs := p.Observe(update)
	if _, known := p.Current(); !known {
		p.Load(nil)
	}
	update()
	return func() {
		if timer != 0 {
			glib.SourceRemove(timer)
			write(true) // the last step is not lost with the dialog
		}
		removePrefs()
		d.boardShow.HandlerDisconnect(showHandle)
		for i, h := range handles {
			d.boardWindows[i].HandlerDisconnect(h)
		}
		unbindAll()
	}
}

// bindTriageAccounts fills Triage These Accounts (an expander row of the
// AI page's Board group): a switch per enabled account, checked as the
// daemon decides (boardtriage.TriageAccountChecked; none listed is every
// enabled mail account, which the subtitle says), changed through
// ToggleTriageAccount (the last checked account cannot be turned off).
// The accounts are asked with account.list now and by reload (whenever the
// AI page comes up).
func (d *PreferencesDialog) bindTriageAccounts(c *client.Client, p *boardtriage.Preferences) (reload func(), unbind func()) {
	btr := i18n.Tr
	exp := d.boardTriageAccounts
	exp.SetTitle(board.TriageSettingsAccounts(btr))
	var (
		accounts []api.Account
		rows     []*adw.SwitchRow
		syncing  bool
		gen      int
	)
	enabledAccounts := func() []api.Account {
		var out []api.Account
		for _, a := range accounts {
			if a.Enabled {
				out = append(out, a)
			}
		}
		return out
	}
	update := func() {
		if d.closed {
			return
		}
		prefs, known := p.Current()
		exp.SetSensitive(known && len(rows) > 0)
		exp.SetSubtitle("")
		if known {
			switch boardtriage.TriageAccountsSubtitle(prefs.TriageAccounts, accounts) {
			case boardtriage.TriageAccountsAll:
				exp.SetSubtitle(board.TriageSettingsAccountsAll(btr))
			case boardtriage.TriageAccountsNone:
				// Only once the accounts are in: before that every list
				// would look like one of gone accounts.
				if accounts != nil {
					exp.SetSubtitle(board.TriageSettingsAccountsNone(btr))
				}
			}
		}
		syncing = true
		for i, a := range enabledAccounts() {
			if i < len(rows) {
				rows[i].SetActive(boardtriage.TriageAccountChecked(prefs.TriageAccounts, a))
			}
		}
		syncing = false
	}
	rebuild := func(list []api.Account) {
		for _, r := range rows {
			exp.Remove(r)
		}
		rows = rows[:0]
		accounts = slices.Clone(list)
		for _, a := range enabledAccounts() {
			id := a.ID
			row := adw.NewSwitchRow()
			row.SetUseMarkup(false)
			row.SetTitle(accountRowTitle(a))
			row.NotifyProperty("active", func() {
				if syncing || d.closed {
					return
				}
				prefs, known := p.Current()
				if !known {
					update()
					return
				}
				next, ok := boardtriage.ToggleTriageAccount(prefs.TriageAccounts, accounts, id, row.Active())
				if !ok {
					update() // the last checked account stays checked
					return
				}
				p.SetTriageAccounts(next, nil)
			})
			rows = append(rows, row)
			exp.AddRow(row)
		}
		update()
	}
	reload = func() {
		gen++
		my := gen
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			var res api.AccountListResult
			err := c.Call(ctx, api.MethodAccountList, api.AccountListParams{}, &res)
			glib.IdleAdd(func() {
				if d.closed || my != gen || err != nil {
					return
				}
				rebuild(res.Accounts)
			})
		}()
	}
	remove := p.Observe(update)
	update()
	reload()
	return reload, remove
}
