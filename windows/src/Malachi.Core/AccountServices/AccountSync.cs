// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/AccountServices/AccountServices.swift
// (Service, offered, offeredForNew, isOn, apply, off, dismissKey,
// showPrompt, prompts, dismissing, pruned and the texts); GTK:
// ui/internal/accountservices (ported at the same time).
//
// The calendar and the contacts of a mail account (docs/plans/
// account-services.md): which of them an account can sync, the wizard's
// offer to switch them on before the first sign-in, and the fallback offer
// in the Calendar and the Contacts window for an account that can sync one
// and has it off, until the user says Not Now. Pure: no RPC, no settings;
// the callers pass the account and the dismissals of the key
// account-services-dismissed.
//
// Only a graph account offers anything today: its capabilities say what it
// can have (calendar, contacts always), its configuration whether it is on
// (calendar.sync, graph.contacts.sync; null is off). A CalDAV account
// decides both in its own wizard; an IMAP account (Gmail included) has
// neither yet.
//
// Windows: the class is AccountSync, not AccountServices, so that the
// namespace Malachi.Core.AccountServices is not hidden (as RuleBook for
// Rules); Swift's [Service: Bool] is a read-only dictionary, its
// Service.rawValue Name.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.I18n;

namespace Malachi.Core.AccountServices;

/// <summary>Swift <c>AccountServices</c>: what a mail account can sync besides its mail.</summary>
public static class AccountSync
{
    /// <summary>What an account can sync besides its mail.</summary>
    public enum Service
    {
        /// <summary>Its calendars.</summary>
        Calendar,

        /// <summary>Its contacts.</summary>
        Contacts,
    }

    /// <summary>Every service, in their order (Swift <c>CaseIterable</c>).</summary>
    public static IReadOnlyList<Service> All { get; } = [Service.Calendar, Service.Contacts];

    /// <summary>The service's raw value: "calendar" or "contacts" (the dismiss key's second half).</summary>
    public static string Name(Service s) => s == Service.Calendar ? "calendar" : "contacts";

    /// <summary>The capability that says the account can have it.</summary>
    public static Capability CapabilityOf(Service s) => s == Service.Calendar ? Capability.Calendar : Capability.Contacts;

    /// <summary>
    /// The services the account can sync and the wizard and the fallback
    /// offer: an account of kind graph with the capability.
    /// <paramref name="capabilities"/> null (a daemon from before them) is
    /// <see cref="API.MailCapabilities"/>, which has neither.
    /// </summary>
    public static IReadOnlyList<Service> Offered(AccountConfig cfg, IReadOnlyList<Capability>? capabilities)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        if (cfg.ProtocolKind.Value != AccountKind.Graph)
        {
            return [];
        }
        var caps = capabilities ?? API.MailCapabilities;
        return [.. All.Where(s => caps.Contains(CapabilityOf(s)))];
    }

    /// <summary>
    /// <see cref="Offered"/> for an account not added yet (the wizard has no
    /// capabilities): the daemon gives every graph account both
    /// (capabilitiesFor).
    /// </summary>
    public static IReadOnlyList<Service> OfferedForNew(AccountConfig cfg) => Offered(cfg, [Capability.Calendar, Capability.Contacts]);

    /// <summary>
    /// The service's switch in the configuration: <c>calendar.sync</c>,
    /// <c>graph.contacts.sync</c> (or a CalDAV account's <c>caldav.contacts</c>,
    /// <see cref="ContactBook.SyncOn"/>).
    /// </summary>
    public static bool IsOn(AccountConfig cfg, Service s)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        return s == Service.Calendar ? cfg.Calendar?.Sync ?? false : ContactBook.SyncOn(cfg);
    }

    /// <summary>
    /// <paramref name="cfg"/> with the services of <paramref name="on"/>
    /// switched on (true) or off (false); a service not in it stays as it
    /// is. Only a graph account changes. Switching on writes the same as
    /// Preferences (<c>CalendarAccountController.SetSync</c>,
    /// <see cref="ContactBook.WithSync"/>): the calendar keeps its other
    /// settings. Switching off leaves an absent part absent (null is off
    /// already).
    /// </summary>
    public static AccountConfig Apply(AccountConfig cfg, IReadOnlyDictionary<Service, bool> on)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        ArgumentNullException.ThrowIfNull(on);
        if (cfg.ProtocolKind.Value != AccountKind.Graph)
        {
            return cfg;
        }
        foreach (var s in All)
        {
            if (!on.TryGetValue(s, out var want))
            {
                continue;
            }
            switch (s)
            {
                case Service.Calendar:
                    if (want || cfg.Calendar is not null)
                    {
                        cfg = cfg with { Calendar = (cfg.Calendar ?? new CalendarConfig { Sync = want }) with { Sync = want } };
                    }
                    break;
                case Service.Contacts:
                    if (want || cfg.Graph?.Contacts is not null)
                    {
                        cfg = ContactBook.WithSync(cfg, want);
                    }
                    break;
            }
        }
        return cfg;
    }

    /// <summary>The services the account can sync and has off (the fallback offer).</summary>
    public static IReadOnlyList<Service> Off(AccountConfig cfg, IReadOnlyList<Capability>? capabilities) =>
        [.. Offered(cfg, capabilities).Where(s => !IsOn(cfg, s))];

    /// <summary>The entry of account-services-dismissed for Not Now of <paramref name="s"/> on the account: "&lt;account id&gt;:&lt;service&gt;".</summary>
    public static string DismissKey(AccountId id, Service s) => id.Value + ":" + Name(s);

    /// <summary>
    /// Whether the Calendar (<see cref="Service.Calendar"/>) or the Contacts
    /// window (<see cref="Service.Contacts"/>) offers to switch the service
    /// on for the account: it is enabled, can sync the service, has it off,
    /// and Not Now was not said.
    /// </summary>
    public static bool ShowPrompt(Account account, Service s, IReadOnlyList<string> dismissed)
    {
        ArgumentNullException.ThrowIfNull(account);
        ArgumentNullException.ThrowIfNull(dismissed);
        return account.Enabled && Off(account.Config, account.Capabilities).Contains(s)
            && !dismissed.Contains(DismissKey(account.Id, s), StringComparer.Ordinal);
    }

    /// <summary>The accounts the Calendar or the Contacts window offers the service for, in their order.</summary>
    public static IReadOnlyList<Account> Prompts(IReadOnlyList<Account> accounts, Service s, IReadOnlyList<string> dismissed)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        return [.. accounts.Where(a => ShowPrompt(a, s, dismissed))];
    }

    /// <summary><paramref name="dismissed"/> with Not Now of <paramref name="s"/> on the account added (once).</summary>
    public static IReadOnlyList<string> Dismissing(IReadOnlyList<string> dismissed, AccountId id, Service s)
    {
        ArgumentNullException.ThrowIfNull(dismissed);
        var key = DismissKey(id, s);
        return dismissed.Contains(key, StringComparer.Ordinal) ? dismissed : [.. dismissed, key];
    }

    /// <summary>
    /// <paramref name="dismissed"/> without the entries of accounts that no
    /// longer exist (an entry that is not "&lt;id&gt;:&lt;service&gt;" goes
    /// too).
    /// </summary>
    public static IReadOnlyList<string> Pruned(IReadOnlyList<string> dismissed, IEnumerable<AccountId> accounts)
    {
        ArgumentNullException.ThrowIfNull(dismissed);
        ArgumentNullException.ThrowIfNull(accounts);
        var ids = accounts.Select(a => a.Value ?? "").ToHashSet(StringComparer.Ordinal);
        return [.. dismissed.Where(e =>
        {
            var i = e.LastIndexOf(':');
            if (i < 0)
            {
                return false;
            }
            var service = e[(i + 1)..];
            return ids.Contains(e[..i]) && All.Any(s => Name(s) == service);
        })];
    }

    // Texts

    /// <summary>The wizard's group over the switches.</summary>
    public static string GroupTitle => L10n.T("Also Sync from This Account");

    /// <summary>The wizard's switch of a service (Preferences' wording).</summary>
    public static string SwitchTitle(Service s) =>
        s == Service.Calendar ? L10n.T("Sync This Account’s Calendars") : L10n.T("Sync Contacts");

    /// <summary>The note under the wizard's group.</summary>
    public static string LaterNote => L10n.T("You can change this later in Preferences.");

    /// <summary>The fallback offer's sentence for the account's name.</summary>
    public static string OffLine(Service s, string account) =>
        s == Service.Calendar
            // TRANSLATORS: %s is the name of a mail account
            ? L10n.T("Calendar sync is off for %s", account)
            // TRANSLATORS: %s is the name of a mail account
            : L10n.T("Contacts sync is off for %s", account);

    /// <summary>The fallback offer's Turn On.</summary>
    public static string TurnOn => L10n.T("Turn On");

    /// <summary>The fallback offer's Not Now.</summary>
    public static string NotNow => L10n.T("Not Now");

    /// <summary>The wizard's offer after a sign-in that asked for a service failed.</summary>
    public static string MailOnly => L10n.T("Try Again with Mail Only");
}
