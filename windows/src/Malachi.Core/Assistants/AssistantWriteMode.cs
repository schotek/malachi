// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/assistant/compose_write.go (WriteMode, WriteNew,
// WriteReply, WriteReplyAll, WriteForward, WriteEdit); macOS:
// macos/Sources/MalachiCore/Compose/ComposeAssistantPrompt.swift, which
// names the mode `String(describing: ComposeKind)`. Go keeps the mode as a
// string so that an unknown one exists; a C# enum is open the same way (any
// other value is unknown: Assistant.WriteMessage refuses it), and
// Assistant.WriteModeNick gives Go's string, as AssistantRewrite and
// RewriteNick do for the rewrite.

namespace Malachi.Core.Assistants;

/// <summary>
/// The kind of compose window the assistant's bar writes for, as the request
/// names it to the model (<see cref="Assistant.WriteModeNick"/>).
/// </summary>
public enum AssistantWriteMode
{
    /// <summary><c>new</c>: a new message.</summary>
    New,

    /// <summary><c>reply</c>: a reply to the sender.</summary>
    Reply,

    /// <summary><c>replyAll</c>: a reply to the sender and the other recipients.</summary>
    ReplyAll,

    /// <summary><c>forward</c>: a forward (the bar writes its introduction).</summary>
    Forward,

    /// <summary><c>edit</c>: a draft opened from the Drafts folder.</summary>
    Edit,
}
