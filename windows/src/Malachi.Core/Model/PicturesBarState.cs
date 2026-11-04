// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/RemoteBar.swift
// (PicturesBarState); GTK: ui/internal/window/remote.go (picturesBarState).

namespace Malachi.Core.Model;

/// <summary>
/// What the pictures bar shows (remote.go <c>picturesBarState</c>): nothing,
/// how many pictures of the HTML body are kept on the mail server only with
/// the button that downloads them, or the notice that they are on their way.
/// It sits below the remote-image bar; both may show.
/// </summary>
/// <param name="Visible">The bar is shown.</param>
/// <param name="Loading">The pictures are on their way: the notice instead of the button.</param>
/// <param name="Remote">How many pictures the button would download.</param>
public readonly record struct PicturesBarState(bool Visible = false, bool Loading = false, int Remote = 0);
