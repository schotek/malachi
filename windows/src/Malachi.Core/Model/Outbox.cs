// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Outbox.swift (outboxBannerText,
// trashTooltip); GTK: ui/internal/window/outbox.go (the same names).
// OutboxTracker, the rest of that Swift file, has a file of its own.

using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Model;

/// <summary>
/// The outbox, the pure parts: messages queued for sending live in the
/// account's outbox folder, carry their delivery state in
/// <see cref="MessageSummary.Outbox"/> and are counted by
/// <see cref="SyncState.PendingOutbox"/> (queued or sending) and
/// <see cref="SyncState.FailedOutbox"/>. The daemon owns the queue: nothing
/// here decides when or whether a message goes out.
/// </summary>
public static class Outbox
{
    /// <summary>
    /// The banner for a delivery state (outbox.go <c>outboxBannerText</c>):
    /// the title, the button label ("" for no button) and whether the banner
    /// shows at all. A message that is not in the outbox, or already
    /// delivered, has none.
    /// </summary>
    public static (string Title, string Button, bool Shown) OutboxBannerText(OutboxInfo? o)
    {
        if (o is null)
        {
            return ("", "", false);
        }
        return o.State.Value switch
        {
            OutboxState.Queued => (L10n.T("Queued for sending"), "", true),
            OutboxState.Sending => (L10n.T("Sending…"), "", true),
            OutboxState.Failed => (RpcErrorText.Text(L10n.T("Sending the message"), o.Error), L10n.T("Retry"), true),
            _ => ("", "", false),
        };
    }

    /// <summary>
    /// The trash button's tooltip: for an outbox message the button cancels
    /// the send instead (outbox.go <c>trashTooltip</c>).
    /// </summary>
    public static string TrashTooltip(bool outbox) => outbox ? L10n.T("Cancel Sending") : L10n.T("Move to Trash");
}
