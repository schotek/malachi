// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppState.swift (AppState.Hooks);
// GTK: the app.* actions of ui/main.go and the entry points window.New
// threads through. Entry points that other parts of the app register once
// they exist: the preferences window and the wizard (wave 2), the compose
// manager's window factory, the main window's search box. While one is
// null its command is disabled (AppDelegate.validateUserInterfaceItem);
// Changed tells every window to re-validate.

using System;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Microsoft.UI.Xaml;

namespace Malachi.App.Shell;

/// <summary>The application's entry points, registered by the parts that provide them.</summary>
public sealed class AppHooks
{
    private Action? openPreferences;
    private Action<Window?>? addAccount;
    private Action<Window?>? addJiraAccount;
    private Func<bool>? canComposeNew;
    private Action? composeNew;
    private Action? checkForNewMail;
    private Action? focusSearch;
    private Action<ComposeParams>? openCompose;

    /// <summary>A hook was set or cleared: the commands re-validate.</summary>
    public event EventHandler? Changed;

    /// <summary>Opens the Preferences window (app.preferences, Ctrl+,). Wave 2 (E6) sets it.</summary>
    public Action? OpenPreferences
    {
        get => openPreferences;
        set => Set(ref openPreferences, value);
    }

    /// <summary>Opens the account wizard over a window (app.add-account). Wave 2 (E6) sets it.</summary>
    public Action<Window?>? AddAccount
    {
        get => addAccount;
        set => Set(ref addAccount, value);
    }

    /// <summary>Opens the Jira account assistant over a window (app.add-jira-account).</summary>
    public Action<Window?>? AddJiraAccount
    {
        get => addJiraAccount;
        set => Set(ref addJiraAccount, value);
    }

    /// <summary>
    /// Whether New Message is offered (capabilities.CanComposeNew over the
    /// accounts): some account writes mail, or none is known yet. Null offers
    /// it; <see cref="NotifyChanged"/> tells the commands it may have changed.
    /// </summary>
    public Func<bool>? CanComposeNew
    {
        get => canComposeNew;
        set => Set(ref canComposeNew, value);
    }

    /// <summary>
    /// Opens the account wizard on an existing account over a window (the
    /// main window when null): the certificate banner's and the sign-in
    /// banner's Edit Account…, an account's row in the status popover
    /// (sync.go editAccount, signInAgain). With a reason
    /// (<c>authRequired</c>, <c>authFailed</c>) a password account opens on
    /// the identity page asking for its password (RequestPassword). Wave 2
    /// (E6) sets it.
    /// </summary>
    public Action<Window?, AccountId, ErrorCode?>? EditAccount { get; set; }

    /// <summary>Opens an empty compose window (app.compose, Ctrl+N).</summary>
    public Action? ComposeNew
    {
        get => composeNew;
        set => Set(ref composeNew, value);
    }

    /// <summary>
    /// Opens a compose window prepared by the actions (reply, forward, a
    /// mailto: link inside a message, a mailto: activation).
    /// </summary>
    public Action<ComposeParams>? OpenCompose
    {
        get => openCompose;
        set => Set(ref openCompose, value);
    }

    /// <summary>Asks the daemon to synchronise now (win.refresh, F5).</summary>
    public Action? CheckForNewMail
    {
        get => checkForNewMail;
        set => Set(ref checkForNewMail, value);
    }

    /// <summary>Puts the keyboard in the title bar's search box (win.search, Ctrl+F, Ctrl+E).</summary>
    public Action? FocusSearch
    {
        get => focusSearch;
        set => Set(ref focusSearch, value);
    }

    /// <summary>The list's filter (window.go message_filter): set it.</summary>
    public Action<MessageFilter>? SetMessageFilter { get; set; }

    /// <summary>The list's filter: read it.</summary>
    public Func<MessageFilter>? MessageFilter { get; set; }

    /// <summary>Whether the list shows search results (the filter is off then).</summary>
    public Func<bool>? SearchActive { get; set; }

    /// <summary>
    /// Brings the compose window editing a draft to the front; false when
    /// none edits it (compose/manager.go FindDraft).
    /// </summary>
    public Func<Draft, bool>? RaiseDraft { get; set; }

    /// <summary>What a hook answers changed (the accounts): the commands re-validate.</summary>
    public void NotifyChanged() => Changed?.Invoke(this, EventArgs.Empty);

    private void Set<T>(ref T? field, T? value)
        where T : class
    {
        field = value;
        Changed?.Invoke(this, EventArgs.Empty);
    }
}
