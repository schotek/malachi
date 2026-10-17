// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationChatBody.swift
// (ConversationLayout.ChatResize). Swift-first: ui/internal/conversation/
// chat.go leaves the chat's measures to the clients.

namespace Malachi.Core.Model;

public static partial class ConversationLayout
{
    /// <summary>
    /// What a change of the chat's viewport size asks for
    /// (<see cref="ChatResizeFor"/>): <paramref name="Width"/>, the width
    /// changed (the bubbles reflow, the short dates may switch);
    /// <paramref name="ToEnd"/>, the viewport ends at the end of the chat
    /// again, as it did before the change (or the end was pinned): else the
    /// row the user read keeps its place.
    /// </summary>
    public readonly record struct ChatResize(bool Width, bool ToEnd);
}
