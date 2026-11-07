// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/chatgpt"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The settings of ChatGPT (Codex) (Preferences → Integrations → ChatGPT,
// its Settings and the catalogue's Add…; data/ui/chatgpt_integration.blp):
// where Codex is (Choose…, Get Codex…), the ChatGPT connection (Continue
// with ChatGPT, Disconnect), the panel's model (bindProviderModel) and
// Manage usage. They were the ChatGPT group of Preferences → AI until the
// AI providers through their API arrived (docs/plans/ai-api.md §7.2);
// their logic moved here unchanged, but for the In-app provider combo,
// which Assistant Uses on the AI page replaced.
type chatGPTIntegration struct {
	*adw.Dialog

	assist *Assistant
	log    *slog.Logger
	toasts *adw.ToastOverlay
	closed bool

	windowTitle                                                *adw.WindowTitle
	group                                                      *adw.PreferencesGroup
	codexRow, connectionRow, usageRow                          *adw.ActionRow
	codexChoose, codexInstall, signIn, disconnect, manageUsage *gtk.Button
	modelRow                                                   *adw.ComboRow
}

// newChatGPTIntegration builds the dialog over the application's
// Assistant state and the settings store; Present it with
// Present(parent).
func newChatGPTIntegration(s *settings.Store, as *Assistant, log *slog.Logger) *chatGPTIntegration {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b := data.Builder("chatgpt_integration.ui")
	d := &chatGPTIntegration{
		Dialog:        b.GetObject("chatgpt_integration_dialog").Cast().(*adw.Dialog),
		assist:        as,
		log:           log.With("component", "chatgpt-integration"),
		toasts:        b.GetObject("toast_overlay").Cast().(*adw.ToastOverlay),
		windowTitle:   b.GetObject("window_title").Cast().(*adw.WindowTitle),
		group:         b.GetObject("chatgpt_group").Cast().(*adw.PreferencesGroup),
		codexRow:      b.GetObject("codex_executable").Cast().(*adw.ActionRow),
		codexChoose:   b.GetObject("codex_choose").Cast().(*gtk.Button),
		codexInstall:  b.GetObject("codex_install").Cast().(*gtk.Button),
		connectionRow: b.GetObject("chatgpt_connection").Cast().(*adw.ActionRow),
		signIn:        b.GetObject("chatgpt_sign_in").Cast().(*gtk.Button),
		disconnect:    b.GetObject("chatgpt_disconnect").Cast().(*gtk.Button),
		modelRow:      b.GetObject("chatgpt_model").Cast().(*adw.ComboRow),
		usageRow:      b.GetObject("chatgpt_usage").Cast().(*adw.ActionRow),
		manageUsage:   b.GetObject("chatgpt_manage_usage").Cast().(*gtk.Button),
	}
	t := assistant.ChatGPTText(tr)
	d.SetTitle(t.Name)
	d.windowTitle.SetTitle(t.Name)
	cleanup := []func(){d.bind(s), bindProviderModel(d, s, d.modelRow, modelPanel)}
	d.ConnectClosed(func() {
		d.closed = true
		for _, f := range cleanup {
			f()
		}
	})
	return d
}

func (d *chatGPTIntegration) AddToast(t *adw.Toast)      { d.toasts.AddToast(t) }
func (d *chatGPTIntegration) isClosed() bool             { return d.closed }
func (d *chatGPTIntegration) assistantState() *Assistant { return d.assist }
func (d *chatGPTIntegration) daemon() *client.Client     { return d.assist.rpc.Load() }
func (d *chatGPTIntegration) toast(text string)          { d.AddToast(widget.PlainToast(text)) }

// editChatGPTIntegration opens the ChatGPT integration's settings over
// the preferences.
func (d *PreferencesDialog) editChatGPTIntegration() {
	if d.assist == nil || d.settings == nil {
		return
	}
	newChatGPTIntegration(d.settings, d.assist, d.log).Present(d)
}

// bind fills the rows and keeps them with the Assistant's ChatGPT state:
// the connection, Codex (its path and version), the description's
// reasons. The returned function stops following.
func (d *chatGPTIntegration) bind(s *settings.Store) func() {
	a := d.assist
	t := assistant.ChatGPTText(tr)
	d.group.SetDescription(t.Description)
	d.codexRow.SetTitle(t.Codex)
	d.codexChoose.SetLabel(assistant.PanelTexts(tr).Choose)
	d.codexInstall.SetLabel(t.Install)
	d.connectionRow.SetTitle(i18n.T("Account"))
	d.signIn.SetLabel(t.SignIn)
	d.disconnect.SetLabel(t.Disconnect)
	d.modelRow.SetTitle(assistant.PanelTexts(tr).Model)
	d.usageRow.SetTitle(t.Usage)
	d.manageUsage.SetLabel(t.Usage)
	var busy bool
	var generation int
	var cancel context.CancelFunc
	var version, versionPath string
	update := func() {
		if d.closed {
			return
		}
		ready := a.chatGPT != nil && os.Getenv("FLATPAK_ID") == ""
		if os.Getenv("FLATPAK_ID") != "" {
			d.group.SetDescription(t.FlatpakUnavailable)
		} else if !ready {
			d.group.SetDescription(t.ConnectionFailed)
		} else {
			d.group.SetDescription(t.Description)
		}
		connection := chatgpt.Connection{}
		if ready {
			connection = a.chatGPT.Connection()
		}
		connected := connection.Status == chatgpt.Connected
		state := t.Disconnected
		switch connection.Status {
		case chatgpt.Connected:
			state = fmt.Sprintf(t.Connected, connection.Email)
		case chatgpt.SigningIn:
			state = t.Connecting
		case chatgpt.ReconnectRequired:
			state = t.Reconnect
		}
		d.connectionRow.SetSubtitle(state)
		d.signIn.SetVisible(!connected)
		d.signIn.SetSensitive(ready && !busy && connection.Status != chatgpt.SigningIn)
		d.disconnect.SetVisible(connected || connection.Status == chatgpt.SigningIn || connection.Status == chatgpt.ReconnectRequired)
		d.disconnect.SetSensitive(ready)
		d.manageUsage.SetSensitive(connected)
		d.modelRow.SetSensitive(connected)
		path := chatgpt.Locate(s.AssistantCodexPath())
		if path == "" {
			d.codexRow.SetSubtitle(t.NativeMissingCodex)
		} else {
			shownPath := path
			if versionPath == path && version != "" {
				shownPath += " · " + version
			}
			d.codexRow.SetSubtitle(shownPath)
		}
		d.codexInstall.SetVisible(path == "")
		d.codexChoose.SetSensitive(os.Getenv("FLATPAK_ID") == "")
		if path == "" || (versionPath == path && version != "") {
			return
		}
		generation++
		my := generation
		if cancel != nil {
			cancel()
		}
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		go func() {
			v, err := chatgpt.Version(ctx, path)
			glib.IdleAdd(func() {
				if !d.closed && my == generation && ctx.Err() == nil && err == nil {
					versionPath, version = path, v
					d.codexRow.SetSubtitle(path + " · " + v)
				}
			})
		}()
	}
	chooseHandle := d.codexChoose.ConnectClicked(func() {
		dlg := gtk.NewFileDialog()
		dlg.SetTitle(t.Codex)
		dlg.Open(context.Background(), nil, func(r gio.AsyncResulter) {
			f, err := dlg.OpenFinish(r)
			if err != nil || d.closed {
				return
			}
			if path := f.Path(); path != "" && chatgpt.Locate(path) != "" {
				s.SetAssistantCodexPath(path)
			}
		})
	})
	installHandle := d.codexInstall.ConnectClicked(func() { widget.LaunchURI(nil, chatgpt.InstallURL, nil) })
	usageHandle := d.manageUsage.ConnectClicked(func() { widget.LaunchURI(nil, "https://chatgpt.com/settings/usage", nil) })
	signInHandle := d.signIn.ConnectClicked(func() {
		if a.chatGPT == nil || busy {
			return
		}
		busy = true
		update()
		go func() {
			err := a.chatGPT.SignIn(context.Background())
			glib.IdleAdd(func() {
				busy = false
				if !d.closed {
					if err != nil {
						d.AddToast(widget.PlainToast(t.ConnectionFailed))
					}
					update()
				}
				if err == nil {
					// The board's triage may have waited for this.
					a.recheckBoardSignIn()
				}
			})
		}()
	})
	disconnectHandle := d.disconnect.ConnectClicked(func() {
		if a.chatGPT == nil {
			return
		}
		a.chatGPT.CancelSignIn()
		go func() {
			confirmed, err := a.chatGPT.Disconnect(context.Background())
			glib.IdleAdd(func() {
				if !d.closed {
					if err != nil || !confirmed {
						d.AddToast(widget.PlainToast(t.RevocationUnconfirmed))
					}
					update()
				}
			})
		}()
	})
	scheduleUpdate := deferUpdate(d.isClosed, update)
	remove := a.OnChange(scheduleUpdate)
	removePath := s.OnChanged(settings.KeyAssistantCodexPath, scheduleUpdate)
	update()
	return func() {
		generation++
		if cancel != nil {
			cancel()
		}
		remove()
		removePath()
		d.codexChoose.HandlerDisconnect(chooseHandle)
		d.codexInstall.HandlerDisconnect(installHandle)
		d.manageUsage.HandlerDisconnect(usageHandle)
		d.signIn.HandlerDisconnect(signInHandle)
		d.disconnect.HandlerDisconnect(disconnectHandle)
	}
}
