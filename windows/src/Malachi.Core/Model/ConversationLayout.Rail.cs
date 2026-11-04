// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationLayout.swift (Rail);
// GTK: ui/internal/window/conversation_layout.go (convRail).

namespace Malachi.Core.Model;

public static partial class ConversationLayout
{
    /// <summary>
    /// convRail: an item's piece of the timeline: its marker,
    /// <paramref name="Accent"/> (the avatar tinted with the accent colour:
    /// the user's own message, never a dot), and whether the line runs from
    /// the item above down to the marker (<paramref name="Above"/>) and from
    /// the marker down to the item below (<paramref name="Below"/>).
    /// </summary>
    public readonly record struct Rail(RailMarker Marker, bool Accent = false, bool Above = false, bool Below = false);
}
