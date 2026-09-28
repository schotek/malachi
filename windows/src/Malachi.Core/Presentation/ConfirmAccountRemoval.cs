// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/PrefsContracts.swift
// (PrefsConfirmRemoval); GTK: ui/internal/widget/rpc.go
// (ConfirmDestructiveExtra). The Accounts page never makes a dialog itself
// (docs/windows-port.md §7.5): the window hands it the shell's alert.

using System.Threading.Tasks;

namespace Malachi.Core.Presentation;

/// <summary>
/// Asks "Remove this account?" (<paramref name="prompt"/>) and answers
/// whether the user confirmed and whether the check box ("Also delete
/// drafts and downloaded data") was on.
/// </summary>
public delegate Task<(bool Confirmed, bool DeleteLocalData)> ConfirmAccountRemoval(AccountRemovalPrompt prompt);
