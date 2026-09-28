// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ActionsController.swift
// (ActionsController.Confirm); GTK: ui/internal/widget/rpc.go
// (ConfirmDestructive).

using System.Threading.Tasks;

namespace Malachi.Core.Controllers;

/// <summary>
/// Asks the user before a destructive action (widget.ConfirmDestructive):
/// <paramref name="parent"/> is the window the dialog goes on, opaque to
/// Core (the WinUI layer passes its window; null is the main window),
/// <paramref name="body"/> is hostile input shown as plain text and
/// <paramref name="confirmLabel"/> carries its GTK mnemonic. True when
/// confirmed.
/// </summary>
public delegate Task<bool> ConfirmDestructive(object? parent, string heading, string body, string confirmLabel);
