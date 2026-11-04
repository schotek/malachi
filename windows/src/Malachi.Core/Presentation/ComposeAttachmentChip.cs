// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the tile of macos/Sources/MalachiMail/Compose/AttachmentChipsView.swift
// (ComposeTileView: the type icon by the file's name or type, the name,
// whose middle the tile cuts to its width with the whole name as its
// tooltip, the size and Remove); GTK: ui/internal/compose/compose.go
// (addChip). The name is the backend's, sanitised, and still shown as plain
// text.

using System;
using Malachi.Core.Api;
using Malachi.Core.Text;

namespace Malachi.Core.Presentation;

/// <summary>One attachment tile of a compose window: icon, name, size and Remove.</summary>
/// <param name="Id">The attachment's id (what Remove removes, what a click previews).</param>
/// <param name="Name">The whole name (the tile cuts its middle to its width; the tooltip).</param>
/// <param name="Size">The size (<see cref="Format.FormatSize"/>).</param>
/// <param name="ContentType">The claimed type, for the icon of a name without a known extension.</param>
public sealed record ComposeAttachmentChip(string Id, string Name, string Size, string ContentType)
{
    /// <summary>addChip: the tile of <paramref name="a"/>.</summary>
    public static ComposeAttachmentChip For(DraftAttachment a)
    {
        ArgumentNullException.ThrowIfNull(a);
        // The daemon sanitised the name; cleaned for display all the same, as
        // every name from a message (a forward's files come from one).
        return new ComposeAttachmentChip(a.Id, DisplayText.Clean(a.Filename), Format.FormatSize(a.Size), a.ContentType);
    }
}
