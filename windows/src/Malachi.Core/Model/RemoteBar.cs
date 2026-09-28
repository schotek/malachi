// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/RemoteBar.swift (remoteBarState,
// loadableImages, picturesBarState, remotePictures, picturesPolicy,
// recheckPictures, reloadAfterDownload, linkTextFor); GTK:
// ui/internal/window/remote.go (remoteBarStateFor, loadableImages,
// picturesBarStateFor, remotePictures, picturesPolicy, recheckPictures,
// reloadAfterDownload, linkTextFor). The two bar states, ActivatedLink and
// LinkDecision, the rest of that Swift file, have files of their own.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// The remote-image bar, the pictures bar and the link lookup, the pure
/// parts. The daemon decides everything about the content; this only says
/// what the bars show.
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
    /// Derives the pictures bar from what is known about the message
    /// (remote.go <c>picturesBarStateFor</c>). The bar shows the wait from
    /// the click until the body was asked for again, whatever the body says
    /// meanwhile.
    /// </summary>
    public static PicturesBarState PicturesBarStateFor(LoadedMessage? lm)
    {
        if (lm is null)
        {
            return default;
        }
        if (lm.LoadingPictures)
        {
            return new PicturesBarState(Visible: true, Loading: true);
        }
        var n = RemotePictures(lm.Body);
        return new PicturesBarState(Visible: n > 0, Remote: n);
    }

    /// <summary>
    /// How many pictures the HTML body shows are kept on the mail server only
    /// and not on this device now (remote.go <c>remotePictures</c>): the
    /// daemon's <see cref="MessageBodyResult.RemotePictures"/>, 0 unless the
    /// body goes into the HTML view (the plain text has no pictures).
    /// </summary>
    public static int RemotePictures(MessageBodyResult? b) =>
        LoadedMessageText.ShowsHtml(b) ? b!.RemotePictureCount : 0;

    /// <summary>
    /// The policy <c>message.body</c> is asked with again after the pictures
    /// were downloaded (remote.go <c>picturesPolicy</c>): allow when the
    /// remote images are loaded or on their way, so the new body does not
    /// take them away again; otherwise no override (the stored preference).
    /// </summary>
    public static RemoteContentPolicy? PicturesPolicy(LoadedMessage lm)
    {
        ArgumentNullException.ThrowIfNull(lm);
        if (lm.LoadingImages || lm.Body?.RemoteContent == RemoteContentPolicy.Allow)
        {
            return RemoteContentPolicy.Allow;
        }
        return null;
    }

    /// <summary>
    /// Whether a partNotDownloaded for picture <paramref name="partId"/> says
    /// that the cached body of <paramref name="lm"/> is out of date (remote.go
    /// <c>recheckPictures</c>): the body lists the part among the pictures it
    /// shows (<see cref="MessageBodyResult.InlineParts"/>) yet counts none on
    /// the server, and nothing that brings a newer body is on its way (the
    /// body itself, the remote images, Download Pictures). Once until the next
    /// download of the message (<see cref="LoadedMessage.PicturesRechecked"/>):
    /// a body that still counts none while the daemon will not serve the
    /// picture is not asked for in a loop.
    /// </summary>
    public static bool RecheckPictures(LoadedMessage? lm, string partId)
    {
        ArgumentNullException.ThrowIfNull(partId);
        if (lm is null || lm.PicturesRechecked || lm.Fetching || lm.LoadingImages || lm.LoadingPictures)
        {
            return false;
        }
        if (lm.Body is not { } b || !LoadedMessageText.ShowsHtml(b) || b.RemotePictureCount != 0)
        {
            return false;
        }
        return b.InlineParts is { } parts && parts.Values.Any(p => string.Equals(p, partId, StringComparison.Ordinal));
    }

    /// <summary>
    /// Whether a download of the message <paramref name="lm"/> holds asks for
    /// its body again (remote.go <c>reloadAfterDownload</c>): the body on
    /// display counts pictures on the mail server, which the daemon now
    /// holds, and neither Download Pictures (which asks for the body itself)
    /// nor a body is on its way.
    /// </summary>
    public static bool ReloadAfterDownload(LoadedMessage? lm) =>
        lm is { LoadingPictures: false, Fetching: false } && RemotePictures(lm.Body) > 0;

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
