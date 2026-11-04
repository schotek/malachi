// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationLayout.swift
// (Marker); GTK: ui/internal/window/conversation_layout.go (convMarker).

namespace Malachi.Core.Model;

/// <summary>convMarker: what marks an item on the conversation's timeline.</summary>
public enum RailMarker
{
    /// <summary>markAvatar: the sender's avatar, its top at the top of the card.</summary>
    Avatar,

    /// <summary>markDot: a small dot beside the first line of the text.</summary>
    Dot,
}
