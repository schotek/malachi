// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationChat.swift
// (Conversation.ChatRow); GTK: ui/internal/conversation/chat.go (ChatRow).
// Go's zero values of a row's item and draft are null here, as in Swift.

namespace Malachi.Core.Model;

/// <summary>
/// conversation.ChatRow: one row of the chat
/// (<see cref="Conversation.ChatRows"/>): what it is
/// (<paramref name="Kind"/>), where it stands (<paramref name="Side"/>), the
/// model's item of a <see cref="ChatRowKind.Bubble"/>,
/// <see cref="ChatRowKind.Event"/> or <see cref="ChatRowKind.Truncated"/> row
/// (<paramref name="Item"/>, whose <see cref="ConversationItem.Text"/> is the
/// truncated row's sentence; null for a draft), and the draft card of a
/// <see cref="ChatRowKind.Draft"/> row (<paramref name="Draft"/>; null
/// otherwise).
/// </summary>
public sealed record ChatRow(ChatRowKind Kind, ChatSide Side, ConversationItem? Item = null, ConversationDraftCard? Draft = null);
