// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationQuoted.swift
// (Conversation.quotedTextLabel); GTK: ui/internal/conversation/quoted.go
// (QuotedTextLabel).
//
// The body of a message shows its new text: the daemon cuts the quoted
// history under it (message.body with trimQuoted; quotedTrimmed says it cut
// something), and a small button under the body ("•••") shows it, then
// hides it again. What the user revealed holds until another conversation or
// message is shown, like the folds (QuotedReveal). Wherever a body shows: the
// cards of the conversation, the message alone in the pane, the message
// window.

using Malachi.Core.I18n;

namespace Malachi.Core.Model;

public static partial class Conversation
{
    /// <summary>
    /// conversation.QuotedTextLabel: the tooltip and accessible name of the
    /// button under a trimmed body; <paramref name="shown"/> is whether the
    /// quoted history shows now.
    /// </summary>
    public static string QuotedTextLabel(bool shown)
    {
        if (shown)
        {
            // TRANSLATORS: the tooltip of the three-dot button under a message whose quoted earlier messages show; hides them.
            return L10n.T("Hide Quoted Text");
        }
        // TRANSLATORS: the tooltip of the three-dot button under a message whose quoted earlier messages are hidden; shows them.
        return L10n.T("Show Quoted Text");
    }
}
