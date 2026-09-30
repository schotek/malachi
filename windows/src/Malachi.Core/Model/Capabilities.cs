// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Capabilities.swift; GTK:
// ui/internal/capabilities/capabilities.go (Can, Supported, Available,
// ForwardAccounts, ForwardFrom, ComposeAccounts, CanComposeNew).
//
// Which message actions the client offers for an account
// (Account.Capabilities, docs/api.md): a mail account (imap, graph) can do
// everything, an issue-tracker account (jira) only what its capabilities
// list, for example comment instead of reply, and forward into a mail
// account. Seen and flagged work on every account and are not decided
// here. Which accounts write mail at all (the From list, New Message) is
// ComposeAccounts and CanComposeNew. The rules extend those of the message
// toolbar (ActionRules): archive and junk need the account's role folder,
// and a queued message in the outbox keeps reply, forward and trash (which
// cancels the send). No texts. Go's Situation and Actions are
// CapabilitySituation and CapabilityActions.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// The capabilities package: a namespace, so the Go names map 1:1
/// (<c>capabilities.Supported</c> → <c>Capabilities.Supported</c>).
/// </summary>
public static class Capabilities
{
    /// <summary>
    /// capabilities.Can: whether account <paramref name="a"/> has capability
    /// <paramref name="c"/> (<see cref="Account.Can"/>): null capabilities
    /// (a daemon from before capabilities) mean the mail set, an empty list
    /// none.
    /// </summary>
    public static bool Can(Account a, Capability c)
    {
        ArgumentNullException.ThrowIfNull(a);
        return a.Can(c);
    }

    // Can for the account of a situation; no account (no folder listed, Go's
    // zero Account) has the mail default.
    private static bool Has(Account? a, Capability c) => a?.Can(c) ?? API.MailCapabilities.Contains(c);

    /// <summary>
    /// capabilities.Supported: the actions the account offers at all,
    /// whatever is selected: a client hides the others (or disables them
    /// where it cannot hide), and labels Reply by Comment. Reply needs reply
    /// or comment, Forward needs forward and an account to send from (the
    /// account itself, or another enabled one that composes), Trash needs
    /// delete except in the outbox (cancelling a queued message), Move,
    /// Archive and Junk need move.
    /// </summary>
    public static CapabilityActions Supported(CapabilitySituation s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var a = s.Account;
        var move = Has(a, Capability.Move);
        return new CapabilityActions
        {
            Reply = Has(a, Capability.Reply) || Has(a, Capability.Comment),
            ReplyAll = Has(a, Capability.ReplyAll),
            Forward = Has(a, Capability.Forward) && (Has(a, Capability.Compose) || s.ComposeAccount),
            Move = move,
            Trash = Has(a, Capability.Delete) || s.Outbox,
            Archive = move,
            Junk = move,
            Comment = Has(a, Capability.Comment),
        };
    }

    /// <summary>
    /// capabilities.Available: the actions enabled now: the supported ones
    /// while a row is selected, archive and junk only with the role folder,
    /// and for a queued message only reply, reply all, forward and trash (the
    /// daemon refuses moves in the outbox). Comment is Supported's, selected
    /// or not.
    /// </summary>
    public static CapabilityActions Available(CapabilitySituation s)
    {
        var sup = Supported(s);
        var on = s.Selected;
        return new CapabilityActions
        {
            Reply = on && sup.Reply,
            ReplyAll = on && sup.ReplyAll,
            Forward = on && sup.Forward,
            Move = on && sup.Move && !s.Outbox,
            Trash = on && sup.Trash,
            Archive = on && sup.Archive && !s.Outbox && s.Archive,
            Junk = on && sup.Junk && !s.Outbox && s.Junk,
            Comment = sup.Comment,
        };
    }

    /// <summary>
    /// capabilities.ForwardAccounts: the accounts a message can be forwarded
    /// from: the enabled ones that compose, in the given order.
    /// </summary>
    public static IReadOnlyList<Account> ForwardAccounts(IEnumerable<Account> accounts) =>
        [.. accounts.Where(a => a.Enabled && Can(a, Capability.Compose))];

    /// <summary>
    /// capabilities.ForwardFrom: the account a message of account
    /// <paramref name="from"/> is forwarded from: that account itself when it
    /// is enabled and composes, else the first of ForwardAccounts; null when
    /// there is none. When the result is not <paramref name="from"/>,
    /// draft.create names it as <see cref="DraftCreateParams.MessageAccountId"/>.
    /// </summary>
    public static Account? ForwardFrom(IEnumerable<Account> accounts, AccountId from)
    {
        var list = ForwardAccounts(accounts);
        return list.FirstOrDefault(a => a.Id == from) ?? (list.Count > 0 ? list[0] : null);
    }

    /// <summary>
    /// capabilities.ComposeAccounts: the accounts a message is written from
    /// (the compose window's From list): those that compose, in the given
    /// order, paused ones included as before. An account that only comments
    /// (an issue tracker) writes in the comment mode of the window instead,
    /// pinned to itself.
    /// </summary>
    public static IReadOnlyList<Account> ComposeAccounts(IEnumerable<Account> accounts) =>
        [.. accounts.Where(a => Can(a, Capability.Compose))];

    /// <summary>
    /// capabilities.CanComposeNew: whether New Message is offered: while no
    /// account is known (the compose window writes from a placeholder
    /// identity until the list arrives) or when some account composes; never
    /// for issue-tracker accounts alone.
    /// </summary>
    public static bool CanComposeNew(IReadOnlyCollection<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        return accounts.Count == 0 || ComposeAccounts(accounts).Count > 0;
    }
}
