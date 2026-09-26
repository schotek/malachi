// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The search bar over the message list (window.blp search_bar): while it
// is open the list shows search.query results for what is typed, in the
// chosen scope, instead of the selected folder. The daemon searches its
// local store; the UI only sends the text and shows the answer. The
// results are a snapshot: they are asked for again when the text, the
// scope or (for the Folder and Account scopes) the selected folder
// changes, not when mail arrives, so nothing moves under the pointer.

// setupSearch binds the search bar's widgets and handlers.
func (w *Window) setupSearch(b *gtk.Builder) {
	w.searchBar = b.GetObject("search_bar").Cast().(*gtk.SearchBar)
	w.searchEntry = b.GetObject("search_entry").Cast().(*gtk.SearchEntry)
	w.searchScope = b.GetObject("search_scope").Cast().(*adw.ToggleGroup)
	w.searchNote = b.GetObject("search_note").Cast().(*gtk.Label)

	// Escape in the entry closes the bar.
	w.searchBar.ConnectEntry(w.searchEntry)
	w.searchBar.NotifyProperty("search-mode-enabled", w.onSearchModeChanged)
	// search-changed comes after the entry's search-delay: the daemon is
	// asked once typing pauses, not for every key.
	w.searchEntry.ConnectSearchChanged(w.onSearchChanged)
	w.searchEntry.ConnectActivate(w.onSearchActivate)
	w.searchScope.NotifyProperty("active-name", w.onSearchScopeChanged)

	// The single-key shortcuts (a, j, s, u, Delete) are application
	// accelerators, which GTK runs before the focused widget sees the key:
	// while the entry has the keyboard they are lifted, so the letters
	// reach it.
	focus := gtk.NewEventControllerFocus()
	focus.ConnectEnter(func() { w.setTypingAccels(false) })
	focus.ConnectLeave(func() { w.setTypingAccels(true) })
	w.searchEntry.AddController(focus)
}

// setTypingAccels installs (on) or lifts the shortcuts without a modifier.
func (w *Window) setTypingAccels(on bool) {
	for action, accel := range MessageAccels {
		if strings.HasPrefix(accel, "<") {
			continue
		}
		if on {
			w.app.SetAccelsForAction(action, []string{accel})
		} else {
			w.app.SetAccelsForAction(action, nil)
		}
	}
}

// startSearch is win.search (Ctrl+F): the bar opens, or its text is
// selected for typing over when it is open already. A collapsed window
// moves to the list.
func (w *Window) startSearch() {
	w.searchBar.SetSearchMode(true)
	w.outerSplit.SetShowContent(true)
	w.innerSplit.SetShowContent(false)
	w.searchEntry.GrabFocus()
	w.searchEntry.SelectRegion(0, -1)
}

// onSearchModeChanged switches the list between the folder and search:
// opening empties it and shows the prompt (or searches for text still in
// the entry), closing clears the entry and loads the folder again.
func (w *Window) onSearchModeChanged() {
	on := w.searchBar.SearchMode()
	st := &w.model.search
	if on == st.active {
		return
	}
	st.active = on
	st.shown, st.focusFirst = false, false
	st.params = api.SearchQueryParams{}
	w.model.bumpList()
	w.model.loading, w.model.loadingMore, w.model.listErr = false, false, nil
	w.model.clearSearchResults()
	if on {
		// No folder is listed: new mail and outbox changes leave the
		// results alone (onNewMessage, refreshOutboxViews).
		w.model.listFolder = folderKey{}
		w.model.grouped = false
		st.scope = w.settings.SearchScope()
		w.searchScope.SetActiveName(string(st.scope))
		w.messageFilter.SetVisible(false)
		w.loadOfflineDays()
		w.rebuildMessageRows()
		w.searchEntry.GrabFocus()
		st.text = strings.TrimSpace(w.searchEntry.Text())
		w.runSearch(false)
	} else {
		st.text = ""
		st.hits = nil
		w.searchEntry.SetText("") // its search-changed finds search inactive
		w.messageFilter.SetVisible(true)
		w.setTypingAccels(true)
		w.rebuildMessageRows()
		w.loadMessages()
		w.messageList.GrabFocus()
	}
	w.applyListAppearance()
	w.refreshListTitle()
	w.showLoadMore()
}

// onSearchChanged runs once typing pauses.
func (w *Window) onSearchChanged() {
	st := &w.model.search
	if !st.active {
		return
	}
	st.text = strings.TrimSpace(w.searchEntry.Text())
	w.runSearch(false)
}

// onSearchActivate is Enter in the entry: the first result is selected,
// now when the results for the text are on show, otherwise as soon as
// they arrive.
func (w *Window) onSearchActivate() {
	st := &w.model.search
	if !st.active {
		return
	}
	st.text = strings.TrimSpace(w.searchEntry.Text())
	if params, _ := w.model.searchRequest(st.text, st.scope); st.shown && params == st.params {
		w.selectFirstResult()
		return
	}
	st.focusFirst = true
	w.runSearch(false)
}

// onSearchScopeChanged follows the scope toggles. The choice is kept for
// the next search; a write from Go (opening the bar) is a no-op here.
func (w *Window) onSearchScopeChanged() {
	sc := settings.SearchScope(w.searchScope.ActiveName())
	st := &w.model.search
	if sc == "" || sc == st.scope {
		return
	}
	st.scope = sc
	w.settings.SetSearchScope(sc)
	w.runSearch(false)
	w.refreshListTitle()
}

// runSearch asks for the first page of results for the entry's text in
// the chosen scope. With too little typed the prompt is shown instead;
// the same request as the results on show (or the one on its way) is not
// sent again unless force.
func (w *Window) runSearch(force bool) {
	st := &w.model.search
	if !st.active {
		return
	}
	if !searchReady(st.text) {
		w.model.bumpList()
		st.params, st.shown, st.focusFirst = api.SearchQueryParams{}, false, false
		w.model.loading, w.model.listErr = false, nil
		if w.model.rowCount() > 0 {
			w.model.clearSearchResults()
			w.rebuildMessageRows()
		}
		w.showListState()
		w.refreshListTitle()
		return
	}
	params, effective := w.model.searchRequest(st.text, st.scope)
	if !force && params == st.params && (st.shown || w.model.loading) {
		return
	}
	st.params, st.effective, st.shown = params, effective, false
	gen := w.model.bumpList()
	w.model.loading, w.model.loadingMore, w.model.listErr = true, false, nil
	w.showListState()
	w.refreshListTitle()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.SearchQueryResult
		err := w.client.Call(ctx, api.MethodSearchQuery, params, &res)
		glib.IdleAdd(func() {
			if gen != w.model.listGen || !st.active {
				return
			}
			w.model.loading = false
			if err != nil {
				// The query is what the user typed: never logged.
				w.log.Warn("search.query", "err", err)
				w.model.clearSearchResults()
				w.model.listErr = err
				w.rebuildMessageRows()
				w.showListState()
				w.refreshListTitle()
				return
			}
			st.shown = true
			w.model.setSearchResults(res)
			w.rebuildMessageRows()
			w.showListState()
			w.refreshListTitle()
			if st.focusFirst {
				st.focusFirst = false
				w.selectFirstResult()
			}
		})
	}()
}

// loadMoreSearch fetches the next page of results (loadMore while
// searching).
func (w *Window) loadMoreSearch(gen uint64) {
	params := w.model.search.params
	params.Page = api.Page{Cursor: w.model.nextCursor, Limit: api.DefaultPageLimit}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.SearchQueryResult
		err := w.client.Call(ctx, api.MethodSearchQuery, params, &res)
		glib.IdleAdd(func() {
			if gen != w.model.listGen || !w.model.search.active {
				return
			}
			w.model.loadingMore = false
			if err != nil {
				w.log.Warn("search.query (more)", "err", err)
				w.Toast(widget.RPCErrorText(i18n.T("Loading more results"), err))
				w.showLoadMore() // the cursor is still there; the button offers a retry
				return
			}
			added := w.model.appendSearchResults(res)
			for _, s := range w.model.messages[len(w.model.messages)-added:] {
				r := w.newMessageRow(s)
				w.rows[listKey{Message: s.ID}] = r
				w.messageList.Append(r)
			}
			w.showListState()
		})
	}()
}

// selectFirstResult selects the first row and gives it the keyboard.
func (w *Window) selectFirstResult() {
	if row := w.messageList.RowAtIndex(0); row != nil {
		w.messageList.SelectRow(row)
		row.GrabFocus()
	}
}

// loadOfflineDays learns the retention window the retention note names.
func (w *Window) loadOfflineDays() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.ConfigGetResult
		err := w.client.Call(ctx, api.MethodConfigGet, api.ConfigGetParams{}, &res)
		glib.IdleAdd(func() {
			if err != nil {
				w.log.Debug("config.get for the search note", "err", err)
				return
			}
			st := &w.model.search
			st.offlineDays, st.offlineKnown = res.Preferences.OfflineDays, true
			if st.active {
				w.showListState()
			}
		})
	}()
}

// showSearchState is showListState while searching and no result is
// listed: the prompt, the search under way, its failure, or no results.
func (w *Window) showSearchState() {
	m := &w.model
	st := &m.search
	retry := false
	switch {
	case !searchReady(st.text):
		setStatusPage(w.listStatusPage, "edit-find-symbolic", i18n.T("Search Mail"),
			searchRetentionText(st.offlineDays, st.offlineKnown))
	case m.listErr != nil:
		retry = true
		setStatusPage(w.listStatusPage, "dialog-warning-symbolic", i18n.T("Search Failed"),
			widget.RPCErrorText(i18n.T("Searching"), m.listErr))
	case m.loading || !st.shown:
		setStatusPage(w.listStatusPage, "", i18n.T("Searching…"), "")
	default:
		setStatusPage(w.listStatusPage, "edit-find-symbolic", i18n.T("No Results"), searchEmptyText(st.effective))
	}
	w.listRetryButton.SetVisible(retry)
	w.listStack.SetVisibleChildName("status")
}

// refreshSearchScope keeps the scope toggles in step with the selection:
// Folder and Account need a selected folder, and the tooltips name what
// they search. The tooltips are Pango markup, so the names, which the
// server or the user chose, are escaped.
func (w *Window) refreshSearchScope() {
	k := w.model.selected
	f, hasFolder := w.model.folder(k)
	folderToggle := w.searchScope.ToggleByName(string(settings.SearchFolder))
	accountToggle := w.searchScope.ToggleByName(string(settings.SearchAccount))
	allToggle := w.searchScope.ToggleByName(string(settings.SearchAll))
	if folderToggle == nil || accountToggle == nil || allToggle == nil {
		return
	}
	folderToggle.SetEnabled(hasFolder)
	accountToggle.SetEnabled(hasFolder)
	if hasFolder {
		// TRANSLATORS: tooltip of the Folder search scope; %s is a folder.
		folderToggle.SetTooltip(glib.MarkupEscapeText(fmt.Sprintf(i18n.T("Search in %s"), folderTitle(f))))
	} else {
		folderToggle.SetTooltip(glib.MarkupEscapeText(i18n.T("Select a folder to search in it")))
	}
	if a, ok := w.model.account(k.Account); ok {
		// TRANSLATORS: tooltip of the Account search scope; %s is an account.
		accountToggle.SetTooltip(glib.MarkupEscapeText(fmt.Sprintf(i18n.T("Search every folder of %s except Trash and Junk"), accountLabel(a))))
	} else {
		accountToggle.SetTooltip("")
	}
	allToggle.SetTooltip(glib.MarkupEscapeText(i18n.T("Search every account except Trash and Junk")))
}
