// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/chatgpt"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

func (d *PreferencesDialog) bindChatGPT(s *settings.Store) func() {
	a := d.assist
	t := assistant.ChatGPTText(tr)
	d.assistantProvider.SetTitle(t.Provider)
	d.assistantProvider.SetModel(gtk.NewStringList([]string{assistant.TargetName(tr, assistant.Code), t.Name}))
	d.chatGPTGroup.SetTitle(t.Name)
	d.chatGPTGroup.SetDescription(t.Description)
	d.codexRow.SetTitle(t.Codex)
	d.codexChoose.SetLabel(assistant.PanelTexts(tr).Choose)
	d.codexInstall.SetLabel(t.Install)
	d.chatGPTConnection.SetTitle(i18n.T("Account"))
	d.chatGPTSignIn.SetLabel(t.SignIn)
	d.chatGPTDisconnect.SetLabel(t.Disconnect)
	d.chatGPTModel.SetTitle(assistant.PanelTexts(tr).Model)
	d.chatGPTUsage.SetTitle(t.Usage)
	d.chatGPTManageUsage.SetLabel(t.Usage)
	var syncing, busy bool
	var ids []string
	var generation int
	var cancel context.CancelFunc
	var version, versionPath string
	var lastCatalog assistantModelCatalogKey
	catalogKnown := false
	var catalog []chatgpt.Model
	fillModels := func(models []chatgpt.Model) {
		syncing = true
		defer func() { syncing = false }()
		ids = []string{""}
		names := []string{t.DefaultModel}
		for _, m := range models {
			if m.ID != "" && !slices.Contains(ids, m.ID) {
				ids = append(ids, m.ID)
				names = append(names, m.Name)
			}
		}
		selected := s.AssistantChatGPTModel()
		if !slices.Contains(ids, selected) {
			ids = append(ids, selected)
			names = append(names, selected)
		}
		d.chatGPTModel.SetModel(gtk.NewStringList(names))
		d.chatGPTModel.SetSelected(uint(slices.Index(ids, selected)))
	}
	update := func() {
		if d.closed {
			return
		}
		syncing = true
		if a.usesChatGPT() {
			d.assistantProvider.SetSelected(1)
		} else {
			d.assistantProvider.SetSelected(0)
		}
		syncing = false
		shown := a.usesChatGPT() && a.target() == assistant.App
		d.chatGPTGroup.SetVisible(shown)
		if !shown {
			return
		}
		ready := a.chatGPT != nil && os.Getenv("FLATPAK_ID") == ""
		if os.Getenv("FLATPAK_ID") != "" {
			d.chatGPTGroup.SetDescription(t.FlatpakUnavailable)
		} else if !ready {
			d.chatGPTGroup.SetDescription(t.ConnectionFailed)
		} else {
			d.chatGPTGroup.SetDescription(t.Description)
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
		d.chatGPTConnection.SetSubtitle(state)
		d.chatGPTSignIn.SetVisible(!connected)
		d.chatGPTSignIn.SetSensitive(ready && !busy && connection.Status != chatgpt.SigningIn)
		d.chatGPTDisconnect.SetVisible(connected || connection.Status == chatgpt.SigningIn || connection.Status == chatgpt.ReconnectRequired)
		d.chatGPTDisconnect.SetSensitive(ready)
		d.chatGPTManageUsage.SetSensitive(connected)
		d.chatGPTModel.SetSensitive(connected)
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
		key := assistantModelCatalogKey{s.AssistantProvider(), a.target(), path, connection.ConnectionID, connection.Status}
		if catalogKnown && lastCatalog == key {
			// Model selection changes the request runtime, not the model catalog.
			syncing = true
			if i := slices.Index(ids, s.AssistantChatGPTModel()); i >= 0 {
				d.chatGPTModel.SetSelected(uint(i))
			} else {
				fillModels(catalog)
			}
			syncing = false
			return
		}
		lastCatalog, catalogKnown, catalog = key, true, nil
		generation++
		my := generation
		if cancel != nil {
			cancel()
		}
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		fillModels(nil)
		if path != "" {
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
		if !connected || path == "" || a.codex == nil {
			return
		}
		runtime := a.codex
		go func() {
			models, err := runtime.GetModels(ctx)
			glib.IdleAdd(func() {
				if d.closed || my != generation || ctx.Err() != nil {
					return
				}
				if err != nil {
					d.chatGPTModel.SetSubtitle(t.ModelsUnavailable)
				} else {
					d.chatGPTModel.SetSubtitle("")
					catalog = models
					fillModels(catalog)
				}
			})
		}()
	}
	providerHandle := d.assistantProvider.NotifyProperty("selected", func() {
		if syncing {
			return
		}
		if d.assistantProvider.Selected() == 1 {
			s.SetAssistantProvider("chatgpt")
		} else {
			s.SetAssistantProvider("claude")
		}
	})
	modelHandle := d.chatGPTModel.NotifyProperty("selected", func() {
		if i := d.chatGPTModel.Selected(); !syncing && i < uint(len(ids)) {
			s.SetAssistantChatGPTModel(ids[i])
		}
	})
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
	installHandle := d.codexInstall.ConnectClicked(func() { widget.LaunchURI(nil, "https://developers.openai.com/codex/cli/", nil) })
	usageHandle := d.chatGPTManageUsage.ConnectClicked(func() { widget.LaunchURI(nil, "https://chatgpt.com/settings/usage", nil) })
	var refresh func()
	refresh = update
	signInHandle := d.chatGPTSignIn.ConnectClicked(func() {
		if a.chatGPT == nil || busy {
			return
		}
		busy = true
		refresh()
		go func() {
			err := a.chatGPT.SignIn(context.Background())
			glib.IdleAdd(func() {
				busy = false
				if !d.closed {
					if err != nil {
						d.AddToast(widget.PlainToast(t.ConnectionFailed))
					}
					refresh()
				}
				if err == nil {
					// The board's triage may have waited for this.
					a.recheckBoardSignIn()
				}
			})
		}()
	})
	disconnectHandle := d.chatGPTDisconnect.ConnectClicked(func() {
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
					refresh()
				}
			})
		}()
	})
	scheduleUpdate := d.deferChoiceUpdate(update)
	remove := a.OnChange(scheduleUpdate)
	update()
	return func() {
		generation++
		if cancel != nil {
			cancel()
		}
		remove()
		d.assistantProvider.HandlerDisconnect(providerHandle)
		d.chatGPTModel.HandlerDisconnect(modelHandle)
		d.codexChoose.HandlerDisconnect(chooseHandle)
		d.codexInstall.HandlerDisconnect(installHandle)
		d.chatGPTManageUsage.HandlerDisconnect(usageHandle)
		d.chatGPTSignIn.HandlerDisconnect(signInHandle)
		d.chatGPTDisconnect.HandlerDisconnect(disconnectHandle)
	}
}

// Board model selection is separate from the conversational provider's model.
func (d *PreferencesDialog) bindBoardProviderModel(s *settings.Store) func() {
	var syncing bool
	var ids []string
	var generation int
	var cancel context.CancelFunc
	var lastCatalog assistantModelCatalogKey
	var catalogKnown bool
	var catalog []chatgpt.Model
	fillModels := func() {
		ids = []string{""}
		names := []string{assistant.ChatGPTText(tr).DefaultModel}
		for _, m := range catalog {
			if m.ID != "" && !slices.Contains(ids, m.ID) {
				ids = append(ids, m.ID)
				names = append(names, m.Name)
			}
		}
		selected := s.BoardChatGPTModel()
		if !slices.Contains(ids, selected) {
			ids = append(ids, selected)
			names = append(names, selected)
		}
		d.boardTriageModel.SetModel(gtk.NewStringList(names))
		d.boardTriageModel.SetSelected(uint(slices.Index(ids, selected)))
	}
	update := func() {
		if d.closed {
			return
		}
		syncing = true
		defer func() { syncing = false }()
		a := d.assist
		if !a.usesChatGPT() {
			if catalogKnown {
				generation++
				if cancel != nil {
					cancel()
				}
				catalogKnown = false
			}
			d.boardTriageModel.SetModel(gtk.NewStringList(modelNames()))
			d.boardTriageModel.SetSelected(uint(slices.Index(assistant.Models, s.BoardTriageModel())))
			return
		}
		connection := chatgpt.Connection{}
		if a.chatGPT != nil {
			connection = a.chatGPT.Connection()
		}
		key := assistantModelCatalogKey{s.AssistantProvider(), a.target(), chatgpt.Locate(s.AssistantCodexPath()), connection.ConnectionID, connection.Status}
		if catalogKnown && key == lastCatalog {
			if i := slices.Index(ids, s.BoardChatGPTModel()); i >= 0 {
				d.boardTriageModel.SetSelected(uint(i))
			} else {
				fillModels()
			}
			return
		}
		lastCatalog, catalogKnown, catalog = key, true, nil
		generation++
		my := generation
		if cancel != nil {
			cancel()
		}
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		fillModels()
		if a.codex == nil || !a.chatGPTReady() {
			return
		}
		runtime := a.codex
		go func() {
			models, err := runtime.GetModels(ctx)
			glib.IdleAdd(func() {
				if d.closed || generation != my || ctx.Err() != nil || !a.usesChatGPT() || err != nil {
					return
				}
				syncing = true
				defer func() { syncing = false }()
				catalog = models
				fillModels()
			})
		}()
	}
	handle := d.boardTriageModel.NotifyProperty("selected", func() {
		if syncing {
			return
		}
		i := d.boardTriageModel.Selected()
		if d.assist.usesChatGPT() {
			if i < uint(len(ids)) {
				s.SetBoardChatGPTModel(ids[i])
			}
		} else if i < uint(len(assistant.Models)) {
			s.SetBoardTriageModel(assistant.Models[i])
		}
	})
	scheduleUpdate := d.deferChoiceUpdate(update)
	remove := d.assist.OnChange(scheduleUpdate)
	removeLegacy := s.OnChanged(settings.KeyBoardTriageModel, scheduleUpdate)
	removeModel := s.OnChanged(settings.KeyBoardChatGPTModel, scheduleUpdate)
	update()
	return func() {
		generation++
		if cancel != nil {
			cancel()
		}
		remove()
		removeLegacy()
		removeModel()
		d.boardTriageModel.HandlerDisconnect(handle)
	}
}

// Runtime model choices do not affect the locally discovered model catalog.
type assistantModelCatalogKey struct {
	provider                 string
	target                   assistant.Target
	executable, connectionID string
	status                   chatgpt.Status
}

// Selection notifications run inside GTK's drop-down update. Replacing its
// model from that stack invalidates GTK's current selection item. Render
// once at idle instead; runtime cancellation and settings writes stay immediate.
func (d *PreferencesDialog) deferChoiceUpdate(update func()) func() {
	queued := false
	return func() {
		if queued || d.closed {
			return
		}
		queued = true
		glib.IdleAdd(func() {
			queued = false
			if !d.closed {
				update()
			}
		})
	}
}
