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
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/chatgpt"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The Model rows of Preferences → AI (the panel's, and the board's in the
// Board group) and of the ChatGPT integration follow the provider in
// effect (docs/plans/ai-api.md §7.3): Claude Code offers the fixed
// models of ui/internal/assistant (assistant-model, board-triage-model),
// ChatGPT the catalogue Codex lists, with the provider's default first
// (assistant-chatgpt-model, board-triage-chatgpt-model), and an AI
// provider account the models the daemon lists for it
// (assistant.providerModels), with the account's own stored model: a
// choice is written to the account (account.update with its
// configuration and the changed model, no credentials; the board's
// first entry, empty, uses the account's model).

// modelPurpose is which model a row chooses: the panel's (and the
// one-shot requests'), or the board's triage.
type modelPurpose int

const (
	modelPanel modelPurpose = iota
	modelBoard
)

// modelHost is what a Model row needs of the dialog it is in.
type modelHost interface {
	isClosed() bool
	assistantState() *Assistant
	// daemon is the RPC client, nil without one (a test).
	daemon() *client.Client
	toast(text string)
}

func (d *PreferencesDialog) isClosed() bool             { return d.closed }
func (d *PreferencesDialog) assistantState() *Assistant { return d.assist }
func (d *PreferencesDialog) daemon() *client.Client     { return d.rpc }
func (d *PreferencesDialog) toast(text string)          { d.AddToast(widget.PlainToast(text)) }

// bindProviderModel keeps row with the model of purpose for the provider
// in effect, and writes the user's choice. Its model is replaced only
// outside GTK's selection notification (deferUpdate). The returned
// function stops following.
func bindProviderModel(h modelHost, s *settings.Store, row *adw.ComboRow, purpose modelPurpose) func() {
	a := h.assistantState()
	var (
		syncing bool
		// ids are the values of the rows shown; "" is the provider's
		// default (ChatGPT) or the account's model (the board over an
		// account).
		ids        []string
		generation int
		cancel     context.CancelFunc
		// The catalogue of the provider, asked once per key.
		lastCatalog  assistantModelCatalogKey
		catalogKnown bool
		catalog      []string
		names        map[string]string
		// pending is a choice written to the daemon and not listed back
		// yet (an account's model).
		pending string
	)
	x := assistant.APIText(tr)
	chatGPTModel := func() string {
		if purpose == modelBoard {
			return s.BoardChatGPTModel()
		}
		return s.AssistantChatGPTModel()
	}
	accountModel := func(acc api.Account) string {
		if acc.Config.AI == nil {
			return ""
		}
		if purpose == modelBoard {
			return acc.Config.AI.BoardModel
		}
		return acc.Config.AI.Model
	}
	// fill shows the catalogue with the current value, which it adds when
	// the catalogue lacks it; withDefault puts the empty value first.
	fill := func(current string, withDefault bool, defaultName string) {
		syncing = true
		defer func() { syncing = false }()
		ids = ids[:0]
		var labels []string
		if withDefault {
			ids = append(ids, "")
			labels = append(labels, defaultName)
		}
		for _, id := range catalog {
			if id != "" && !slices.Contains(ids, id) {
				ids = append(ids, id)
				if n := names[id]; n != "" {
					labels = append(labels, n)
				} else {
					labels = append(labels, id)
				}
			}
		}
		if !slices.Contains(ids, current) {
			ids = append(ids, current)
			labels = append(labels, current)
		}
		row.SetModel(gtk.NewStringList(labels))
		row.SetSelected(uint(slices.Index(ids, current)))
	}
	// reselect shows current, or fills again when it is not listed.
	reselect := func(current string, withDefault bool, defaultName string) {
		if i := slices.Index(ids, current); i >= 0 {
			syncing = true
			row.SetSelected(uint(i))
			syncing = false
			return
		}
		fill(current, withDefault, defaultName)
	}
	forget := func() {
		if catalogKnown {
			generation++
			if cancel != nil {
				cancel()
			}
			catalogKnown = false
		}
	}
	update := func() {
		if h.isClosed() {
			return
		}
		switch a.source() {
		case assistant.SourceClaude:
			forget()
			row.SetSubtitle("")
			catalog, names = nil, nil
			for _, m := range assistant.Models {
				catalog = append(catalog, string(m))
			}
			names = map[string]string{}
			for i, m := range assistant.Models {
				names[string(m)] = modelNames()[i]
			}
			current := string(s.AssistantModel())
			if purpose == modelBoard {
				current = string(s.BoardTriageModel())
			}
			fill(current, false, "")
		case assistant.SourceChatGPT:
			connection := chatgpt.Connection{}
			if a.chatGPT != nil {
				connection = a.chatGPT.Connection()
			}
			key := assistantModelCatalogKey{provider: s.AssistantProvider(), target: a.target(),
				executable: chatgpt.Locate(s.AssistantCodexPath()), connectionID: connection.ConnectionID, status: connection.Status}
			defaultName := assistant.ChatGPTText(tr).DefaultModel
			if catalogKnown && key == lastCatalog {
				reselect(chatGPTModel(), true, defaultName)
				return
			}
			lastCatalog, catalogKnown, catalog, names = key, true, nil, nil
			row.SetSubtitle("")
			generation++
			my := generation
			if cancel != nil {
				cancel()
			}
			ctx, stop := context.WithCancel(context.Background())
			cancel = stop
			fill(chatGPTModel(), true, defaultName)
			if a.codex == nil || !a.chatGPTReady() {
				return
			}
			runtime := a.codex
			go func() {
				models, err := runtime.GetModels(ctx)
				glib.IdleAdd(func() {
					if h.isClosed() || generation != my || ctx.Err() != nil || a.source() != assistant.SourceChatGPT {
						return
					}
					if err != nil {
						row.SetSubtitle(assistant.ChatGPTText(tr).ModelsUnavailable)
						return
					}
					names = map[string]string{}
					catalog = nil
					for _, m := range models {
						catalog = append(catalog, m.ID)
						names[m.ID] = m.Name
					}
					fill(chatGPTModel(), true, defaultName)
				})
			}()
		case assistant.SourceAccount:
			acc, _ := a.apiAccount()
			current := accountModel(acc)
			if pending != "" {
				if pending == current {
					pending = ""
				} else {
					current = pending
				}
			}
			subtitle, withDefault, defaultName := x.ModelSubtitle, false, ""
			if purpose == modelBoard {
				subtitle, withDefault, defaultName = x.BoardModelSubtitle, true, x.BoardModelDefault
			}
			row.SetSubtitle(subtitle)
			key := assistantModelCatalogKey{provider: s.AssistantProvider(), account: acc.ID, enabled: acc.Enabled}
			if catalogKnown && key == lastCatalog {
				reselect(current, withDefault, defaultName)
				return
			}
			lastCatalog, catalogKnown, catalog, names = key, true, nil, nil
			generation++
			my := generation
			if cancel != nil {
				cancel()
			}
			ctx, stop := context.WithCancel(context.Background())
			cancel = stop
			fill(current, withDefault, defaultName)
			rpc := h.daemon()
			if rpc == nil || acc.ID == "" {
				return
			}
			go func() {
				var res api.AssistantProviderModelsResult
				err := rpc.Call(ctx, api.MethodAssistantProviderModels, api.AssistantProviderModelsParams{AccountID: acc.ID}, &res)
				glib.IdleAdd(func() {
					if h.isClosed() || generation != my || ctx.Err() != nil || err != nil || a.source() != assistant.SourceAccount {
						return
					}
					names = map[string]string{}
					catalog = nil
					for _, m := range res.Models {
						catalog = append(catalog, m.ID)
						names[m.ID] = m.DisplayName
					}
					acc, _ := a.apiAccount()
					current := accountModel(acc)
					if pending != "" {
						current = pending
					}
					fill(current, withDefault, defaultName)
				})
			}()
		}
	}
	// chooseAccountModel writes model to the chosen account.
	chooseAccountModel := func(model string) {
		acc, ok := a.apiAccount()
		rpc := h.daemon()
		if !ok || rpc == nil || acc.Config.AI == nil || accountModel(acc) == model {
			return
		}
		cfg := acc.Config
		ai := *cfg.AI
		if purpose == modelBoard {
			ai.BoardModel = model
		} else {
			ai.Model = model
		}
		cfg.AI = &ai
		pending = model
		go func() {
			ctx, stop := context.WithTimeout(context.Background(), rpcTimeout)
			defer stop()
			err := rpc.Call(ctx, api.MethodAccountUpdate, api.AccountUpdateParams{AccountID: acc.ID, Config: cfg}, &api.AccountUpdateResult{})
			glib.IdleAdd(func() {
				if h.isClosed() {
					return
				}
				if err != nil {
					pending = ""
					h.toast(widget.RPCErrorText(assistant.APIText(tr).ModelRow, err))
					update()
				}
			})
		}()
	}
	handle := row.NotifyProperty("selected", func() {
		if syncing {
			return
		}
		i := row.Selected()
		if i >= uint(len(ids)) {
			return
		}
		chosen := ids[i]
		switch a.source() {
		case assistant.SourceClaude:
			if purpose == modelBoard {
				s.SetBoardTriageModel(assistant.Model(chosen))
			} else {
				s.SetAssistantModel(assistant.Model(chosen))
			}
		case assistant.SourceChatGPT:
			if purpose == modelBoard {
				s.SetBoardChatGPTModel(chosen)
			} else {
				s.SetAssistantChatGPTModel(chosen)
			}
		case assistant.SourceAccount:
			chooseAccountModel(chosen)
		}
	})
	scheduleUpdate := deferUpdate(h.isClosed, update)
	removes := []func(){
		a.OnChange(scheduleUpdate),
		s.OnChanged(settings.KeyAssistantModel, scheduleUpdate),
		s.OnChanged(settings.KeyBoardTriageModel, scheduleUpdate),
		s.OnChanged(settings.KeyAssistantChatGPTModel, scheduleUpdate),
		s.OnChanged(settings.KeyBoardChatGPTModel, scheduleUpdate),
	}
	update()
	return func() {
		generation++
		if cancel != nil {
			cancel()
		}
		for _, r := range removes {
			r()
		}
		row.HandlerDisconnect(handle)
	}
}

// assistantModelCatalogKey is what a catalogue was listed for: a change
// lists it again; a model chosen does not.
type assistantModelCatalogKey struct {
	provider                 string
	target                   assistant.Target
	executable, connectionID string
	status                   chatgpt.Status
	account                  api.AccountID
	enabled                  bool
}

// Selection notifications run inside GTK's drop-down update. Replacing its
// model from that stack invalidates GTK's current selection item. Render
// once at idle instead; runtime cancellation and settings writes stay
// immediate. closed says whether the dialog went away meanwhile.
func deferUpdate(closed func() bool, update func()) func() {
	queued := false
	return func() {
		if queued || closed() {
			return
		}
		queued = true
		glib.IdleAdd(func() {
			queued = false
			if !closed() {
				update()
			}
		})
	}
}

// deferChoiceUpdate is deferUpdate for the preferences dialog.
func (d *PreferencesDialog) deferChoiceUpdate(update func()) func() {
	return deferUpdate(d.isClosed, update)
}
