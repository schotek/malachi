// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the quick-reply bar's Comment label on an issue
// (ConversationQuickReplyBar; GTK quick_reply_bar.go's comment box, macOS
// ConversationQuickReplyBar.swift's commentBox, an accessibility element of
// role static text). A panel has no automation peer of its own, so the
// label (an icon and, unless the pane is narrow, the word) names itself as
// one text element with this peer, whose name the bar sets
// (AutomationProperties.Name: Comment).

using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Reader.Conversation;

/// <summary>A horizontal panel that is one text element for UI Automation.</summary>
public sealed partial class QuickReplyCommentLabel : StackPanel
{
    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() =>
        new ConversationGroupPeer(this, nameof(QuickReplyCommentLabel), AutomationControlType.Text);
}
