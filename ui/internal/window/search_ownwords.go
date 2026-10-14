// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The search in the user's own words (ui/internal/assistant search.go, the
// In App target; macOS MainToolbar and MainWindowController): while the
// one-shot requests can run (Assistant.CanRunInApp), the button beside the
// search entry and Alt+Enter in it send the typed words to the user's
// Claude Code, which answers with a query in the search syntax; the query
// replaces the words and is searched for as if typed and Enter pressed.
// While the words are converted the entry shows "Converting the search…"
// and takes no typing; a failure is a toast and the words stay. The first
// request ever asks for consent on the main window. Only the typed words
// go to Claude, no mail.

// ownWords is the search in the user's own words of the main window.
type ownWords struct {
	button *gtk.Button
	// searcher is made on first use.
	searcher *assistantpanel.Searcher
	// converting: the words are with Claude Code; typed is what the entry
	// held, for when the conversion fails.
	converting bool
	typed      string
}

// setupOwnWords binds the button and Alt+Enter; b is the main window's
// builder.
func (w *Window) setupOwnWords(b *gtk.Builder) {
	o := &w.ownWords
	o.button = b.GetObject("search_own_words").Cast().(*gtk.Button)
	o.button.SetIconName(assistantIcon)
	o.button.SetTooltipText(assistant.SearchTexts(tr).OwnWords)
	o.button.ConnectClicked(w.searchInOwnWords)
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		if keyval != gdk.KEY_Return && keyval != gdk.KEY_KP_Enter || state&gdk.AltMask == 0 || !w.assist.CanRunInApp() {
			return false
		}
		w.searchInOwnWords()
		return true
	})
	w.searchEntry.AddController(keys)
	w.searchEntry.ConnectChanged(w.syncOwnWords)
	// The main window lives as long as the application: no unbinding.
	generation := w.assist.RuntimeGeneration()
	w.assist.OnChange(func() {
		if now := w.assist.RuntimeGeneration(); now != generation {
			generation = now
			w.cancelOwnWords()
		}
		w.syncOwnWords()
	})
	w.syncOwnWords()
}

// syncOwnWords shows the button while the one-shot requests can run,
// sensitive while there are words; a conversion under way ends when they
// can no longer run.
func (w *Window) syncOwnWords() {
	o := &w.ownWords
	ok := w.assist.CanRunInApp()
	o.button.SetVisible(ok)
	o.button.SetSensitive(ok && !o.converting && strings.TrimSpace(w.searchEntry.Text()) != "")
	if !ok && o.converting {
		w.cancelOwnWords()
	}
}

// searchInOwnWords converts the entry's words and searches for the query.
func (w *Window) searchInOwnWords() {
	o := &w.ownWords
	if !w.assist.CanRunInApp() || o.converting {
		return
	}
	words := w.searchEntry.Text()
	s := w.searcher()
	w.beginConverting()
	started := s.Convert(words, func(out assistantpanel.SearchOutcome) {
		switch out.Kind {
		case assistantpanel.SearchQuery:
			w.endConverting(out.Text)
			w.onSearchActivate()
		case assistantpanel.SearchFailed:
			w.endConverting("")
			w.Toast(out.Text)
		default:
			w.endConverting("")
		}
	})
	if !started {
		w.endConverting("")
	}
}

// cancelOwnWords ends a conversion under way (the search bar closed, the
// target changed); the typed words come back.
func (w *Window) cancelOwnWords() {
	o := &w.ownWords
	if !o.converting {
		return
	}
	o.searcher.Cancel()
	w.endConverting("")
}

// searcher is the conversion, made on first use; its consent question is
// on the main window.
func (w *Window) searcher() *assistantpanel.Searcher {
	o := &w.ownWords
	if o.searcher == nil {
		req := w.assist.NewRequest()
		req.Consent = func(done func(bool)) { w.assist.AskAssistantConsent(w, done) }
		o.searcher = assistantpanel.NewSearcher(tr, req)
	}
	return o.searcher
}

// beginConverting: the entry shows "Converting the search…" and takes no
// typing; what was typed is kept.
func (w *Window) beginConverting() {
	o := &w.ownWords
	o.converting = true
	o.typed = w.searchEntry.Text()
	w.searchEntry.SetEditable(false)
	w.searchEntry.SetPlaceholderText(assistant.SearchTexts(tr).Converting)
	w.searchEntry.SetText("")
	w.syncOwnWords()
}

// endConverting: the entry takes typing again and shows query, or the
// words typed before (query "": a failure).
func (w *Window) endConverting(query string) {
	o := &w.ownWords
	if !o.converting {
		return
	}
	o.converting = false
	w.searchEntry.SetEditable(true)
	w.searchEntry.SetPlaceholderText(i18n.T("Search Mail"))
	if query != "" {
		w.searchEntry.SetText(query)
	} else {
		w.searchEntry.SetText(o.typed)
	}
	o.typed = ""
	w.syncOwnWords()
}
