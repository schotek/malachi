// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/RemoteBar.swift (remoteBarState,
// loadableImages, linkTextFor); GTK: ui/internal/window/remote.go
// (remoteBarStateFor, loadableImages, linkTextFor). ActivatedLink and
// LinkDecision, the rest of that Swift file, have files of their own.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// The remote-image bar and the link lookup, the pure parts. The daemon
/// decides everything about the content; this only says what the bar shows.
/// </summary>
public static class RemoteBar
{
    /// <summary>
    /// Derives the bar from what is known about the message (remote.go
    /// <c>remoteBarStateFor</c>). The bar shows the wait from the click until
    /// the daemon answers, whatever the body says meanwhile.
    /// </summary>
    public static RemoteBarState RemoteBarStateFor(LoadedMessage? lm)
    {
        if (lm is null)
        {
            return default;
        }
        if (lm.LoadingImages)
        {
            return new RemoteBarState(Visible: true, Loading: true);
        }
        var n = LoadableImages(lm.Body);
        return new RemoteBarState(Visible: n > 0, Blocked: n);
    }

    /// <summary>
    /// How many remote images of the body could still be shown by asking the
    /// daemon again, which is what the bar offers (remote.go
    /// <c>loadableImages</c>). Only the block policy leaves anything to load.
    /// Under allow the daemon already fetched what it could, and the images
    /// the sanitiser still counts are the ones it removes whatever the
    /// policy: CSS url(), srcset, background attributes, plain http:, a
    /// download that failed. Tracking pixels are counted separately and never
    /// loaded at all.
    /// </summary>
    public static int LoadableImages(MessageBodyResult? b)
    {
        if (b is null || string.IsNullOrEmpty(b.Html) || b.RemoteContent != RemoteContentPolicy.Block)
        {
            return 0;
        }
        return b.Blocked.RemoteImages;
    }

    /// <summary>
    /// The visible text of the first link in <paramref name="links"/> with
    /// this target, "" when the daemon listed none (remote.go
    /// <c>linkTextFor</c>). The target is compared exactly.
    /// </summary>
    public static string LinkTextFor(string uri, IReadOnlyList<Link> links)
    {
        ArgumentNullException.ThrowIfNull(links);
        foreach (var l in links)
        {
            if (string.Equals(l.Href, uri, StringComparison.Ordinal))
            {
                return l.Text;
            }
        }
        return "";
    }
}
