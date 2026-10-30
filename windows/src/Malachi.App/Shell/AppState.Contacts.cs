// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppState.swift (contacts) and
// AppDelegate.swift (Window ▸ Contacts); GTK: ui/main.go (app.contacts).
// The application's address books (Contacts/AppContacts.cs), made after
// the calendar, and the hook of the "…" menus' Contacts.

using Malachi.App.Contacts;

namespace Malachi.App.Shell;

/// <summary>The address books' application-wide state.</summary>
public sealed partial class AppState
{
    /// <summary>The address books (the chips' lookup, the Contacts window, Add to Contacts…).</summary>
    public AppContacts Contacts { get; private set; } = null!;

    // After the calendar: the controllers' options, the lookup, the routing
    // of notify.contactsChanged, and app.contacts.
    private void InitializeContacts()
    {
        Contacts = new AppContacts(this);
        Hooks.OpenContacts = () => Contacts.ShowWindow();
    }

    // Quit's Release: the routing goes.
    private void CloseContacts() => Contacts?.Dispose();
}
