// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.6): what the previewer is asked to
// show for a click on an attachment chip, the counterpart of the file
// macos AttachmentActions.preview hands Quick Look and GTK's
// previewAttachment hands Sushi, kept in memory: the bytes message.part
// returned, or none for a program, whose panel shows its name, size and
// type only.

using System;
using Malachi.Core.Api;

namespace Malachi.Core.Presentation;

/// <summary>An attachment for the previewer.</summary>
/// <param name="Attachment">What the chip stands for.</param>
/// <param name="Message">The message it belongs to (Open and Save As… of the previewer act on it).</param>
/// <param name="ContentType">The type <c>message.part</c> served, null without the bytes.</param>
/// <param name="Data">The bytes; null shows the panel only (a program: metadata only).</param>
/// <param name="CanOpen">Whether the previewer offers Open: never for a program or script.</param>
public sealed record PreviewRequest(Attachment Attachment, MessageSummary Message, string? ContentType, ReadOnlyMemory<byte>? Data, bool CanOpen)
{
    /// <summary>The size the panel shows: the bytes when fetched, the listed size otherwise.</summary>
    public long? Size => Data?.Length;
}
