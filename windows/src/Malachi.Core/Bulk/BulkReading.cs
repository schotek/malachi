// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Bulk/BulkMail.swift (Bulk.message,
// Bulk.wantsOffer); GTK: ui/internal/window/bulk.go (bulkIcons, bulkMessage,
// wantsOffer, bulkStripFor) and conversation_controller.go (needsBody's
// wantsOffer).
//
// What the reading views take of the bulk-mail logic: the message the strip
// is decided from, whether a conversation's card needs message.get for the
// unsubscribe offer, the strip of a message as its cache entry holds it, and
// the icon of each kind of strip. The views, which differ per platform,
// draw it.

using System;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Text;

namespace Malachi.Core.Bulk;

/// <summary>The reading side of bulk mail (the non-widget half of window/bulk.go).</summary>
public static class BulkReading
{
    /// <summary>
    /// bulkIcons: the symbolic icon (a GTK name, <c>Icons.Glyph</c> draws it)
    /// of each kind of strip; "" for no strip.
    /// </summary>
    public static string IconName(BulkStripKind kind) => kind switch
    {
        BulkStripKind.Newsletter => "mail-send-symbolic",
        BulkStripKind.List => "system-users-symbolic",
        BulkStripKind.Automated => "applications-system-symbolic",
        BulkStripKind.Junk => "dialog-warning-symbolic",
        BulkStripKind.Unsubscribed => "object-select-symbolic",
        _ => "",
    };

    /// <summary>
    /// bulkMessage: the message the strip is decided from: the cached full
    /// message when <c>message.get</c> answered, else the summary alone (the
    /// strip shows at once, the button follows with the offer). A cached
    /// message that has no classification takes the summary's.
    /// </summary>
    public static Message MessageFor(MessageSummary s, LoadedMessage? lm)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (lm?.Msg is not { } m)
        {
            return new Message { Summary = s };
        }
        if (m.Summary.Bulk is null && s.Bulk is not null)
        {
            return m with { Summary = m.Summary with { Bulk = s.Bulk } };
        }
        return m;
    }

    /// <summary>
    /// wantsOffer: a summary whose card needs <c>message.get</c> for the
    /// unsubscribe offer (a newsletter or a mailing list).
    /// </summary>
    public static bool WantsOffer(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return s.Bulk is { Kind.Value: BulkKind.Newsletter or BulkKind.List };
    }

    /// <summary>
    /// bulkStripFor: the strip of message <paramref name="s"/> as
    /// <paramref name="lm"/> holds it, from the folder of the given
    /// <paramref name="role"/>, the date in the client's full-date format.
    /// </summary>
    public static BulkStrip StripFor(MessageSummary s, LoadedMessage? lm, FolderRole role) =>
        BulkMail.StripFor(MessageFor(s, lm), role, date => Format.FormatDateTime(date));
}
