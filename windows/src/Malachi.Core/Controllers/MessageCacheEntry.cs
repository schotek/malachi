// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the arguments of macos/Sources/MalachiCore/Controllers/
// MessageCache.swift's onLoaded and onRemoteBar callbacks (MessageID,
// LoadedMessage). Windows-only type: a C# event carries one argument.

using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>A message and its cache entry as it is now.</summary>
/// <param name="Id">The message.</param>
/// <param name="Loaded">Its entry, shared by every view showing it.</param>
public readonly record struct MessageCacheEntry(MessageId Id, LoadedMessage Loaded);
