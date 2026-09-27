// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the fetch the macOS PartSchemeHandler makes (cache.fetchPart);
// GTK: ui/internal/htmlview/scheme.go (PartFetcher). The app passes
// MessageCache.FetchPartAsync (MessageWebView.UseCache); the canary passes a
// stand-in.

using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Html;

namespace Malachi.App.WebViews;

/// <summary>
/// htmlview.PartFetcher: the claimed type and the bytes of one message part
/// (<c>message.part</c>). Whether the type may be shown is the viewer's check.
/// </summary>
public delegate Task<(string ContentType, byte[] Data)> PartFetcher(PartReference part, CancellationToken cancellationToken);
