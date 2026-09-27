// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/RemoteBar.swift (RemoteBarState);
// GTK: ui/internal/window/remote.go (remoteBarState).

namespace Malachi.Core.Model;

/// <summary>
/// What the remote-image bar shows (remote.go <c>remoteBarState</c>):
/// nothing, how many remote images were blocked with the buttons that load
/// them, or the notice that they are on their way.
/// </summary>
/// <param name="Visible">The bar is shown.</param>
/// <param name="Loading">The images are on their way: the notice instead of the buttons.</param>
/// <param name="Blocked">How many remote images the buttons would load.</param>
public readonly record struct RemoteBarState(bool Visible = false, bool Loading = false, int Blocked = 0);
