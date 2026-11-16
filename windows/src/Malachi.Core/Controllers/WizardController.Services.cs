// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift
// (offeredServices, chosenServices, updateServices, setService,
// signInWithMailOnly, signInDidNotSucceed, servicesView, serviceChoice,
// failedWithServices); GTK: ui/internal/accountwizard oauth.go over
// ui/internal/accountservices.
//
// The calendar and the contacts of a new Microsoft 365 account
// (docs/plans/account-services.md §3.2): the browser sign-in's prompt
// offers to sync them too, both checked (K1); SignInWithProvider passes the
// checked ones in the configuration of account.oauthStart, so that the
// daemon asks for their scopes in the same consent. A sign-in that asked
// for one and did not succeed (failed, refused, or cancelled on the waiting
// page: an administrator's approval page can only be left so) offers "Try
// Again with Mail Only", which unchecks them and signs in again. Editing an
// account offers nothing (Preferences keep its switches).

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.AccountServices;

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    private readonly Dictionary<AccountSync.Service, bool> serviceChoice = [];

    // The last sign-in asked for a service and did not succeed: the group
    // offers "Try Again with Mail Only".
    private bool failedWithServices;

    /// <summary>The prompt's group of services, as last delivered through <see cref="ServicesChanged"/> (Swift <c>servicesView</c>).</summary>
    public ServicesView? CurrentServicesView { get; private set; }

    /// <summary>
    /// The user's choice of the services to sync too; a service not in it is
    /// checked (K1). It stays while the wizard is open.
    /// </summary>
    public IReadOnlyDictionary<AccountSync.Service, bool> ServiceChoice => serviceChoice;

    // The services the new account of the sign-in can sync; none when
    // editing (Preferences keep the switches) or without a new account.
    private IReadOnlyList<AccountSync.Service> OfferedServices =>
        Editing is null && OAuth?.Config is { } cfg ? AccountSync.OfferedForNew(cfg) : [];

    // The choice of the offered services: checked unless unchecked.
    private Dictionary<AccountSync.Service, bool> ChosenServices =>
        OfferedServices.ToDictionary(s => s, s => serviceChoice.GetValueOrDefault(s, true));

    /// <summary>A switch of the group.</summary>
    public void SetService(AccountSync.Service s, bool on)
    {
        scope.VerifyAccess();
        if (!OfferedServices.Contains(s))
        {
            return;
        }
        serviceChoice[s] = on;
        UpdateServices();
    }

    /// <summary>"Try Again with Mail Only": unchecks every service and signs in again.</summary>
    public void SignInWithMailOnly()
    {
        scope.VerifyAccess();
        if (CurrentServicesView?.MailOnly is null)
        {
            return;
        }
        foreach (var s in OfferedServices)
        {
            serviceChoice[s] = false;
        }
        failedWithServices = false;
        UpdateServices();
        SignInWithProvider();
    }

    // Delivers the group of the prompt (null off the prompt).
    private void UpdateServices()
    {
        ServicesView? v = null;
        var offered = OfferedServices;
        if (CurrentOAuthView is OAuthView.Prompt && offered.Count > 0)
        {
            var chosen = ChosenServices;
            v = new ServicesView
            {
                Title = AccountSync.GroupTitle,
                Rows = [.. offered.Select(s => new ServiceRow(s, AccountSync.SwitchTitle(s), chosen.GetValueOrDefault(s, true)))],
                Note = AccountSync.LaterNote,
                MailOnly = failedWithServices && chosen.Values.Any(on => on) ? AccountSync.MailOnly : null,
            };
        }
        if (Equals(v, CurrentServicesView))
        {
            return;
        }
        CurrentServicesView = v;
        ServicesChanged?.Invoke(this, v);
    }

    // A sign-in ended without success: when it asked for a service, the
    // group offers the mail-only retry.
    private void SignInDidNotSucceed() => failedWithServices = ChosenServices.Values.Any(on => on);
}
