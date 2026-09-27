// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/SyncController.swift
// (ConnView); GTK: ui/internal/window/status.go (connView).

namespace Malachi.Core.Controllers;

/// <summary>
/// What the status line knows of the daemon connection (status.go
/// <c>connView</c>). The connection controller folds the handshake and
/// <c>system.info</c> into its state (connected with the answer, its
/// failure, a mismatching protocol), so beside it only the failure of
/// <c>sync.status</c> is kept. GTK has one more moment, connected with
/// <c>system.info</c> still on its way; here the controller reports the
/// connection only once <c>system.info</c> answered.
/// </summary>
/// <param name="State">The connection controller's state.</param>
/// <param name="SyncFailed">
/// <c>sync.status</c> failed; the next state from the daemon clears it, and
/// a change of the connection forgets it.
/// </param>
public readonly record struct ConnView(ConnectionState State, bool SyncFailed = false);
