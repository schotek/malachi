// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/SyncController.swift
// (SyncController.FooterState); GTK: ui/internal/window/sync.go (the label
// and spinner refreshSyncLabel and startSync set).

namespace Malachi.Core.Controllers;

/// <summary>
/// The sync half of the status line (<c>syncStatusText</c>, or "Checking for
/// new mail…").
/// </summary>
/// <param name="Text">The line, a whole sentence; "" without any account.</param>
/// <param name="Spinning">Whether the spinner turns.</param>
public readonly record struct FooterState(string Text, bool Spinning);
