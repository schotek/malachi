// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/conversation/chat.go (Expanded: Expand, Is,
// ExpandDraft, IsDraft, Reset). The bubbles of the chat layout the user
// expanded while the preferences conversation-chat-truncate-mine and -others (Shorten My or Others' Messages
// in Chat) shortens them: forgotten when another conversation or case is
// selected (as the folds are), kept across a switch of the layout, never
// persisted; nothing collapses back.

using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>conversation.Expanded: the messages and drafts whose bubbles the user expanded.</summary>
public sealed class ConversationExpanded
{
    private readonly HashSet<MessageId> messages = [];
    private readonly HashSet<DraftId> drafts = [];

    /// <summary>Expand: the user expanded the bubble of message <paramref name="id"/>; an empty id is ignored.</summary>
    public void Expand(MessageId id)
    {
        if (!string.IsNullOrEmpty(id.Value))
        {
            messages.Add(id);
        }
    }

    /// <summary>Is: whether the user expanded the bubble of message <paramref name="id"/>.</summary>
    public bool Is(MessageId? id) => id is { } i && !string.IsNullOrEmpty(i.Value) && messages.Contains(i);

    /// <summary>ExpandDraft: the user expanded the bubble of draft <paramref name="id"/>; an empty id is ignored.</summary>
    public void ExpandDraft(DraftId id)
    {
        if (!string.IsNullOrEmpty(id.Value))
        {
            drafts.Add(id);
        }
    }

    /// <summary>IsDraft: whether the user expanded the bubble of draft <paramref name="id"/>.</summary>
    public bool IsDraft(DraftId? id) => id is { } i && !string.IsNullOrEmpty(i.Value) && drafts.Contains(i);

    /// <summary>Reset: forgets every expanded bubble (another conversation or case).</summary>
    public void Reset()
    {
        messages.Clear();
        drafts.Clear();
    }
}
