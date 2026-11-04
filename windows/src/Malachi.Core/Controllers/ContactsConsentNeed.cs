// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ContactsOptions.swift
// (ContactsConsentNeed); GTK: ui/internal/contactsctl window.go
// (ConsentNeed).

using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>
/// contactsctl.ConsentNeed: an account whose contacts wait for Microsoft's
/// consent: the banner <c>ContactsText.ConsentBanner(Account)</c> with
/// Allow… (<see cref="SignIn"/>) or the Online Accounts advice.
/// </summary>
/// <param name="AccountId">The account.</param>
/// <param name="Account">Its name for the banner (<c>ContactBook.AccountName</c>).</param>
/// <param name="SignIn">Allow… can fix it (account.oauthStart with contacts).</param>
public sealed record ContactsConsentNeed(AccountId AccountId, string Account, bool SignIn);
