// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/LoadedMessage.swift (LoadedMessage);
// GTK: ui/internal/window/message_view.go (loadedMessage, complete,
// bodySettled, size). LoadedCache and the pane's text helpers
// (LoadedMessageText) are the other types of that Swift file.

using System;
using System.Text;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// What fetching produced for one message (message_view.go
/// <c>loadedMessage</c>): the full header view, the body, or the error that
/// prevented the body. A failed <c>message.get</c> is only logged (the
/// summary headers stay) and retried the next time the message is shown; a
/// failed body is retried the same way. A class, because the pane, a
/// message window and the compose prefill share one entry and see each
/// other's halves arrive. UI-thread-affine, as every controller's state
/// (docs/windows-port.md §7.1): no locks.
/// </summary>
public sealed class LoadedMessage
{
    /// <summary>The full header view (<c>message.get</c>).</summary>
    public Message? Msg { get; set; }

    /// <summary>The body (<c>message.body</c>).</summary>
    public MessageBodyResult? Body { get; set; }

    /// <summary>The <c>message.body</c> failure.</summary>
    public Exception? Err { get; set; }

    /// <summary>Insertion order in <see cref="LoadedCache"/>.</summary>
    public ulong Seq { get; set; }

    /// <summary>
    /// The header half is in flight; a second fetch for the same id while one
    /// runs joins instead of asking the daemon twice.
    /// </summary>
    public bool Getting { get; set; }

    /// <summary>The body half is in flight.</summary>
    public bool Fetching { get; set; }

    /// <summary>
    /// Set from the moment the user asks for the remote images (Load Images,
    /// Always From This Sender) until the daemon has answered; the bar shows
    /// it instead of the buttons (<see cref="RemoteBar.RemoteBarStateFor"/>).
    /// </summary>
    public bool LoadingImages { get; set; }

    /// <summary>Nothing is left to fetch.</summary>
    public bool Complete => Msg is not null && Body is not null;

    /// <summary>The body half has an answer (content or error).</summary>
    public bool BodySettled => Body is not null || Err is not null;

    /// <summary>
    /// What the entry costs the cache, in UTF-8 bytes: its body (an HTML
    /// body carries its inlined pictures).
    /// </summary>
    public int Size
    {
        get
        {
            if (Body is not { } body)
            {
                return 0;
            }
            return Encoding.UTF8.GetByteCount(body.Html ?? "") + Encoding.UTF8.GetByteCount(body.Text);
        }
    }
}
