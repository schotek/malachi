// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AttachmentChips.swift (PartState);
// GTK: ui/internal/window/attachments.go (partAvailability, partWaiting,
// partLocal, partRemote, partUnavailable).

namespace Malachi.Core.Model;

/// <summary>What a chip can do with its part (attachments.go <c>partState</c>).</summary>
public enum PartState
{
    /// <summary>Nothing is known yet: the body is still on its way.</summary>
    Waiting,

    /// <summary>Stored on this device: <c>message.part</c> delivers it.</summary>
    Local,

    /// <summary>
    /// On the mail server only (<see cref="Api.Attachment.Remote"/>), or the
    /// body is not downloaded yet: <c>message.download</c> fetches it first.
    /// </summary>
    Remote,

    /// <summary>
    /// Out of reach: the message is too large or unreadable, or the part is
    /// over <see cref="Api.API.Limits.MaxAttachmentDataBytes"/>.
    /// </summary>
    Unavailable,
}
