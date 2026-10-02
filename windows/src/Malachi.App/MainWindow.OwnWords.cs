// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/search_ownwords.go (setupOwnWords,
// syncOwnWords, searchInOwnWords, cancelOwnWords, searcher,
// beginConverting, endConverting); macOS: MainToolbar.swift and
// MainWindowController.swift (Search in Your Own Words). The search in the
// user's own words (ui/internal/assistant search.go, the In App target):
// while the one-shot requests can run (AssistantController.CanRunInApp),
// the ✦ button beside the search box and Alt+Enter in it send the typed
// words to the user's Claude Code, which answers with a query in the search
// syntax; the query replaces the words and is searched for as if typed and
// Enter pressed (a list folded by a narrow window unfolds). While the words
// are converted the box shows "Converting the search…" and takes no typing;
// a failure is a toast and the words stay. The first request ever asks for
// consent on the main window. Only the typed words go to Claude, no mail.
//
// Windows differences: the box's text field (the AutoSuggestBox's
// TextBox) is made read-only while the words are converted, as GTK makes
// its entry not editable, so that it keeps the keyboard.

using System;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Windows.System;
using Windows.UI.Core;

namespace Malachi.App;

/// <summary>The main window's search in the user's own words.</summary>
public sealed partial class MainWindow
{
    private SearchConversion? searcher;

    // The words are with Claude Code; typed is what the box held, for when
    // the conversion fails.
    private bool converting;
    private string typed = "";

    // The button and Alt+Enter; called once the Assistant state exists.
    private void SetupOwnWords()
    {
        var text = Assistant.SearchTexts().OwnWords;
        ToolTipService.SetToolTip(OwnWordsButton, text);
        AutomationProperties.SetName(OwnWordsButton, text);
        OwnWordsButton.Click += (_, _) => SearchInOwnWords();
        SearchBox.AddHandler(UIElement.PreviewKeyDownEvent, new KeyEventHandler(OnOwnWordsKey), handledEventsToo: true);
        SearchBox.TextChanged += (_, _) => SyncOwnWords();
        state.Assistant.Changed += (_, _) => SyncOwnWords();
        state.InAppProviderChanged += OnSearchProviderChanged;
        Closed += (_, _) =>
        {
            state.InAppProviderChanged -= OnSearchProviderChanged;
            searcher?.Dispose();
        };
        SyncOwnWords();
    }

    private void OnSearchProviderChanged(object? sender, EventArgs e)
    {
        CancelOwnWords();
        if (searcher is { } active) active.Request.Provider = state.InAppProvider;
        SyncOwnWords();
    }

    // Alt+Enter in the box, while the one-shot requests can run.
    private void OnOwnWordsKey(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter || !state.Assistant.CanRunInApp
            || !InputKeyboardSource.GetKeyStateForCurrentThread(VirtualKey.Menu).HasFlag(CoreVirtualKeyStates.Down))
        {
            return;
        }
        e.Handled = true;
        SearchInOwnWords();
    }

    // The button while the one-shot requests can run, enabled while there
    // are words; a conversion under way ends when they can no longer run.
    private void SyncOwnWords()
    {
        var ok = state.Assistant.CanRunInApp;
        OwnWordsButton.Visibility = ok ? Visibility.Visible : Visibility.Collapsed;
        OwnWordsButton.IsEnabled = ok && !converting && SearchBox.Text.Trim().Length > 0;
        if (!ok && converting)
        {
            CancelOwnWords();
        }
    }

    // Converts the box's words and searches for the query.
    private void SearchInOwnWords()
    {
        if (!state.Assistant.CanRunInApp || converting)
        {
            return;
        }
        var words = SearchBox.Text;
        var s = Searcher();
        BeginConverting();
        var started = s.Convert(words, outcome =>
        {
            switch (outcome)
            {
                case SearchConversion.Outcome.Query q:
                    EndConverting(q.Text);
                    SearchForTyped(q.Text);
                    break;
                case SearchConversion.Outcome.Failed f:
                    EndConverting("");
                    Toasts.Show(f.Text);
                    break;
                default:
                    EndConverting("");
                    break;
            }
        });
        if (!started)
        {
            EndConverting("");
        }
    }

    // A conversion under way ends (the target changed); the typed words come
    // back.
    private void CancelOwnWords()
    {
        if (!converting)
        {
            return;
        }
        searcher?.Cancel();
        EndConverting("");
    }

    // The conversion, made on first use; its consent question is on the
    // main window.
    private SearchConversion Searcher()
    {
        if (searcher is { } existing)
        {
            return existing;
        }
        var request = state.NewAssistantRequest();
        request.Consent = () => state.AskAssistantConsentAsync(this);
        searcher = new SearchConversion(request);
        return searcher;
    }

    // The box shows "Converting the search…" and takes no typing; what was
    // typed is kept.
    private void BeginConverting()
    {
        converting = true;
        typed = SearchBox.Text;
        SearchBox.PlaceholderText = Assistant.SearchTexts().Converting;
        SearchBox.Text = "";
        SetSearchEditable(false);
        SyncOwnWords();
    }

    // The box takes typing again and shows query, or the words typed before
    // (query "": a failure).
    private void EndConverting(string query)
    {
        if (!converting)
        {
            return;
        }
        converting = false;
        SetSearchEditable(true);
        SearchBox.PlaceholderText = L10n.T("Search Mail");
        SearchBox.Text = query.Length > 0 ? query : typed;
        typed = "";
        SyncOwnWords();
    }

    // The box's text field takes typing, or not (GtkEditable.SetEditable).
    private void SetSearchEditable(bool editable)
    {
        if (FindDescendant<TextBox>(SearchBox) is { } box)
        {
            box.IsReadOnly = !editable;
        }
    }

    // The query as if typed and Enter pressed: the list's search follows the
    // box, and its first result is selected (a folded list unfolds first).
    private void SearchForTyped(string query)
    {
        if (list is null)
        {
            return;
        }
        Navigate(layout.ShowList);
        list.SearchFieldChanged(query);
        list.SearchFieldReturn(query);
    }
}
