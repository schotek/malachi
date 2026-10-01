// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the bulk half of ui/internal/window/message_view.go (render's
// renderBulk) and window/bulk.go (renderBulk, showBulk, bulkStripFor);
// macOS: MessageView/MessageViewController.swift (the strip). A bulk message (a
// newsletter, a mailing list, an automated message) gets the strip above its
// body: what it is, and the Unsubscribe button. The strip is decided from
// the summary at once and from the full message once <c>message.get</c> has
// brought the offer. Not in an attached message's view, which has no strip.

using System;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Bulk;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <content>The bulk-mail strip.</content>
public sealed partial class ReaderController
{
    /// <summary>
    /// The folder role of the folder a message lies in (the junk folder
    /// changes the strip), set by the application; a message of an unknown
    /// folder reads as <see cref="FolderRole.None"/>.
    /// </summary>
    public Func<MessageSummary, FolderRole>? FolderRoleOf { get; set; }

    /// <summary>The strip above the body; its default value hides it (personal mail).</summary>
    [ObservableProperty]
    public partial BulkStrip Bulk { get; private set; } = new();

    /// <summary>A request waits for its answer: the strip's button waits too.</summary>
    [ObservableProperty]
    public partial bool BulkBusy { get; private set; }

    /// <summary>
    /// bulk.go <c>refreshBulk</c>: redraws the strip when the view shows the
    /// message of <paramref name="lm"/>'s entry (a request began or ended,
    /// or its offer was applied) and leaves the rest alone.
    /// </summary>
    public void RefreshBulk(LoadedMessage? lm)
    {
        if (closed || Mode == ReaderMode.Embedded || Current is not { } s)
        {
            return;
        }
        RenderBulk(s, lm);
    }

    // renderBulk.
    private void RenderBulk(MessageSummary s, LoadedMessage? lm)
    {
        if (Mode == ReaderMode.Embedded)
        {
            return;
        }
        Bulk = BulkReading.StripFor(s, lm, FolderRoleOf?.Invoke(s) ?? FolderRole.None);
        BulkBusy = lm?.Unsubscribing == true;
    }

    // leaveForConversation's showBulk(Strip{}): the strip goes.
    private void HideBulk()
    {
        Bulk = new BulkStrip();
        BulkBusy = false;
    }
}
