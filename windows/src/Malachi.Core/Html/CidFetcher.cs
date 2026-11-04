// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/CIDRegistry.swift (CIDFetcher); GTK:
// ui/internal/editor/cid.go (Fetcher).

using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Html;

/// <summary>
/// editor.Fetcher: produces the bytes and the media type of one inline image
/// the daemon holds (<c>attachment.get</c>). The scheme handler calls it
/// under <see cref="CidRegistry.FetchTimeout"/> and checks the answer with
/// <see cref="CidRegistry.CheckInline"/>.
/// </summary>
public delegate Task<InlineImage> CidFetcher(CancellationToken cancellationToken);
