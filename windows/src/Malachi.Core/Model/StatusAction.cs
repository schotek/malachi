// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/SyncStatus.swift (StatusAction);
// GTK: ui/internal/window/status.go (statusAction).

namespace Malachi.Core.Model;

/// <summary>
/// What the button of an account's row in the status popover does
/// (status.go <c>statusAction</c>).
/// </summary>
public enum StatusAction
{
    /// <summary><c>statusActionNone</c>: nothing to offer (a paused account).</summary>
    NoAction,

    /// <summary><c>sync.trigger</c> for the account (an icon).</summary>
    Check,

    /// <summary><c>sync.trigger</c> for the account ("Try Again").</summary>
    Retry,

    /// <summary>
    /// Signing in again, labelled by <see cref="SyncStatusTexts.AuthBannerButton"/>
    /// (the edit wizard asking for the password,
    /// <see cref="SyncStatusTexts.EditsPassword"/>).
    /// </summary>
    SignIn,

    /// <summary>The account assistant ("Edit Account…").</summary>
    Edit,
}
