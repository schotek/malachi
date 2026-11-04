// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ContactsPreferencesController.swift
// (ContactsPreferencesController.Section); GTK: ui/internal/contactsctl
// preferences.go (PrefsAccount).

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Contacts;

namespace Malachi.Core.Controllers;

/// <summary>contactsctl.PrefsAccount: an account's section of Preferences → Contacts.</summary>
/// <param name="Account">The account.</param>
/// <param name="Title">The heading (<see cref="ContactBook.AccountName"/>).</param>
/// <param name="Badge">The kind capsule.</param>
/// <param name="SyncOn">The switch Sync Contacts.</param>
/// <param name="Consent">Contacts are on but the grant lacks the scope.</param>
/// <param name="SignIn">Allow… can fix it (else <see cref="ContactsText.ConsentOnline"/>).</param>
/// <param name="Books">The account's books with their checkboxes (<see cref="AddressBook.Visible"/>).</param>
/// <param name="CardDAV"><see cref="ContactsText.CardDAVNote"/> follows the books.</param>
public sealed record ContactsPreferencesSection(
    Account Account, string Title, string Badge, bool SyncOn, bool Consent, bool SignIn, IReadOnlyList<AddressBook> Books, bool CardDAV);
