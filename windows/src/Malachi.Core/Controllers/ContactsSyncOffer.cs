// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ContactsListController.swift
// (ContactsListController.SyncOffer); GTK: ui/internal/window
// contacts_window.go over ui/internal/accountservices. A type of its own,
// as ContactsConsentNeed is.

using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>
/// An offer of the Contacts window to sync the contacts of an account that
/// has them off (docs/plans/account-services.md §3.3), with Turn On
/// (<see cref="ContactsListController.TurnOnSync"/>) and Not Now
/// (<see cref="ContactsListController.DismissSyncOffer"/>).
/// </summary>
/// <param name="AccountId">The account.</param>
/// <param name="Text">"Contacts sync is off for %s".</param>
public sealed record ContactsSyncOffer(AccountId AccountId, string Text);
