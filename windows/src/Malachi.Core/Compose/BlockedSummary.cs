// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/BlockedSummary.swift; GTK:
// ui/internal/compose/draft.go (blockedSummary, skippedSummary). Swift's
// free functions blockedSummary(_:) and skippedSummary(_:) are
// BlockedSummary.Text and BlockedSummary.Skipped.

using System;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Compose;

/// <summary>
/// The lines the compose window shows once about what the sanitiser removed
/// from a quoted original and what of the original could not be attached.
/// </summary>
public static class BlockedSummary
{
    /// <summary>
    /// compose.blockedSummary: describes what the daemon's sanitiser removed;
    /// empty when nothing was.
    /// </summary>
    public static string Text(BlockedContent b)
    {
        ArgumentNullException.ThrowIfNull(b);
        long n = (long)b.RemoteImages + b.RemoteStyles + b.RemoteFonts + b.Scripts + b.Forms
            + b.EventHandlers + b.DangerousUrls + b.EmbeddedFrames + b.TrackingPixels;
        if (n == 0)
        {
            return "";
        }
        return L10n.N("%d unsafe element was removed from the message",
            "%d unsafe elements were removed from the message", n);
    }

    /// <summary>
    /// compose.skippedSummary: how many parts of the original
    /// <c>draft.create</c> did not import (<see cref="ComposeParams.Skipped"/>:
    /// over a cap, unreadable, or kept on the mail server); empty when none.
    /// </summary>
    public static string Skipped(int n)
    {
        if (n <= 0)
        {
            return "";
        }
        // TRANSLATORS: %d is the number of files of the forwarded (or quoted) message that the new one lacks.
        return L10n.N("%d attachment of the original could not be attached",
            "%d attachments of the original could not be attached", n);
    }
}
