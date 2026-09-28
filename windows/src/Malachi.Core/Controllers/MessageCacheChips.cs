// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the arguments of macos/Sources/MalachiCore/Controllers/
// MessageCache.swift's onChips callback (MessageID, LoadedMessage?); GTK:
// ui/internal/window/download.go (refreshChips). Windows-only type: a C#
// event carries one argument.

using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>A message whose attachment chips have to be drawn again, and its cache entry.</summary>
/// <param name="Id">The message.</param>
/// <param name="Loaded">
/// Its entry as it is now; null when the cache no longer holds the message
/// (it can drop out while a download runs), and a view draws from what it
/// last rendered.
/// </param>
public readonly record struct MessageCacheChips(MessageId Id, LoadedMessage? Loaded);
