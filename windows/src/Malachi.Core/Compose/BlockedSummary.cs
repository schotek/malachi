// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/BlockedSummary.swift; GTK:
// ui/internal/compose/draft.go (blockedSummary). Swift's free function
// blockedSummary(_:) is BlockedSummary.Text.

using System;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Compose;

/// <summary>The one line the compose window shows about what the sanitiser removed from a quoted original.</summary>
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
}
