// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationReply.swift
// (Conversation.QuickReplyMode.composeMode, composeKind); GTK:
// ui/internal/conversation/reply.go (QuickReplyMode.ComposeMode) and
// ui/internal/window/quick_reply.go (quickReplyKind).

namespace Malachi.Core.Model;

/// <summary>Swift's computed properties of <see cref="QuickReplyMode"/>.</summary>
public static class QuickReplyModeExtensions
{
    extension(QuickReplyMode mode)
    {
        /// <summary>QuickReplyMode.ComposeMode: the <c>draft.create</c> mode of the mode.</summary>
        public Malachi.Core.Api.ComposeMode ComposeMode => mode switch
        {
            QuickReplyMode.ReplyAll => Malachi.Core.Api.ComposeMode.ReplyAll,
            QuickReplyMode.Forward => Malachi.Core.Api.ComposeMode.Forward,
            _ => Malachi.Core.Api.ComposeMode.Reply,
        };

        /// <summary>
        /// The compose window's kind for the mode (the GTK window's
        /// quick_reply.go <c>quickReplyKind</c>), for the host that opens it.
        /// </summary>
        public Malachi.Core.Compose.ComposeKind ComposeKind => mode switch
        {
            QuickReplyMode.ReplyAll => Malachi.Core.Compose.ComposeKind.ReplyAll,
            QuickReplyMode.Forward => Malachi.Core.Compose.ComposeKind.Forward,
            _ => Malachi.Core.Compose.ComposeKind.Reply,
        };
    }
}
