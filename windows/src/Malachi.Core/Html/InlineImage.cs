// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the (data, contentType) tuple of
// macos/Sources/MalachiCore/HTML/CIDRegistry.swift (CIDFetcher); GTK:
// ui/internal/editor/cid.go (Fetcher's results).

using System;

namespace Malachi.Core.Html;

/// <summary>The bytes and the media type of one inline picture.</summary>
/// <param name="Data">The picture.</param>
/// <param name="ContentType">Its media type, as the daemon or the file picker named it.</param>
public readonly record struct InlineImage(ReadOnlyMemory<byte> Data, string ContentType);
