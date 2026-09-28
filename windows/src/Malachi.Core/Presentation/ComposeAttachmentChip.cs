// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the chip of macos/Sources/MalachiMail/Compose/AttachmentChipsView.swift
// (ChipView: the icon, the tail-elided name with the whole name as its
// tooltip, the size); GTK: ui/internal/compose/compose.go (addChip). The
// name is the backend's, sanitised, and still shown as plain text.

using System;
using Malachi.Core.Api;
using Malachi.Core.Text;

namespace Malachi.Core.Presentation;

/// <summary>One attachment chip of a compose window: icon, name, size and Remove.</summary>
/// <param name="Id">The attachment's id (what Remove removes).</param>
/// <param name="Name">The name, cut to <see cref="ComposeAttachmentsController.ChipNameChars"/> characters.</param>
/// <param name="FullName">The whole name (the tooltip).</param>
/// <param name="Size">The size (<see cref="Format.FormatSize"/>).</param>
/// <param name="Icon">The GTK icon name: a picture for an inline image, a paperclip otherwise.</param>
public sealed record ComposeAttachmentChip(string Id, string Name, string FullName, string Size, string Icon)
{
    /// <summary>addChip: the chip of <paramref name="a"/>.</summary>
    public static ComposeAttachmentChip For(DraftAttachment a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return new AttachmentChip(
            a.Id,
            ComposeAttachmentsController.ChipName(a.Filename),
            a.Filename,
            Format.FormatSize(a.Size),
            a.Inline ? "image-x-generic-symbolic" : "mail-attachment-symbolic");
    }
}
