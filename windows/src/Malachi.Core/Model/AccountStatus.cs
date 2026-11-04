// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/SyncStatus.swift (AccountStatus);
// GTK: ui/internal/window/status.go (accountStatus).

using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>One account's row in the status popover (status.go <c>accountStatus</c>).</summary>
public sealed record AccountStatus
{
    /// <summary>The account.</summary>
    public required AccountId Account { get; init; }

    /// <summary><see cref="AccountsPage.AccountRowTitle"/>; plain text.</summary>
    public required string Title { get; init; }

    /// <summary>One whole sentence, never pieced together.</summary>
    public required string Detail { get; init; }

    /// <summary>What the row's button does.</summary>
    public StatusAction Action { get; init; } = StatusAction.NoAction;

    /// <summary>Where the account signs in: the label and route of <see cref="StatusAction.SignIn"/>.</summary>
    public SignInKind SignIn { get; init; } = SignInKind.Password;

    /// <summary>
    /// Why <see cref="StatusAction.SignIn"/> is needed: the code of the
    /// error of the account's authRequired state, 0 when it has none. With a
    /// password account, authRequired or authFailed lead to the edit wizard
    /// asking for the password (<see cref="SyncStatusTexts.EditsPassword"/>),
    /// as the sign-in banner does.
    /// </summary>
    public ErrorCode Reason { get; init; }

    /// <summary><see cref="SyncState.FailedOutbox"/>; above zero the popover links to the outbox.</summary>
    public int Failed { get; init; }
}
