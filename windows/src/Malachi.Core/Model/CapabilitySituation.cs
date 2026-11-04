// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Capabilities.swift
// (Capabilities.Situation); GTK: ui/internal/capabilities/capabilities.go
// (Situation).

using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>capabilities.Situation: what the rules of <see cref="Capabilities"/> look at.</summary>
public sealed record CapabilitySituation
{
    /// <summary>
    /// The selected row's account; with nothing selected, the account of the
    /// listed folder. Null (no folder listed) has the mail default.
    /// </summary>
    public Account? Account { get; init; }

    /// <summary>A row is selected and the actions are on at all.</summary>
    public bool Selected { get; init; }

    /// <summary>
    /// The row is a queued message in the account's outbox (with nothing
    /// selected: the listed folder is the outbox).
    /// </summary>
    public bool Outbox { get; init; }

    /// <summary>The account has an archive folder and the row is not in it already (CanMoveToRole).</summary>
    public bool Archive { get; init; }

    /// <summary>The account has a junk folder and the row is not in it already.</summary>
    public bool Junk { get; init; }

    /// <summary>
    /// Some enabled account can compose (ForwardAccounts is not empty): the
    /// forward of an issue goes out from a mail account.
    /// </summary>
    public bool ComposeAccount { get; init; }
}
