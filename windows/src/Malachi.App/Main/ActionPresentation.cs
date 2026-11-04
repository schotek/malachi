// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/ActionPresentation.swift;
// GTK: ui/internal/window/actions.go (setMessageActionsSensitive's
// visibility, presentReply, commentIcon) and message_window.go's buttons.
// How the per-message actions show for the account's capabilities
// (ActionFlags.Unsupported and Comment, from Capabilities.Supported): an
// action the account does not offer at all leaves the header bar, the
// command row of a message window and the context menu (GTK hides the
// buttons; Windows' menus drop the items, as a WinUI menu has no
// hidden-when); Reply is labelled "Comment" with its own icon
// (Jira.ReplyLabel) on an account that comments on issues instead. Whether
// an action is enabled stays its command's.

using Malachi.App.Resources;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Microsoft.UI.Xaml;

namespace Malachi.App.Main;

/// <summary>The labels, icons and visibility of the message actions for their account.</summary>
public static class ActionPresentation
{
    /// <summary>actions.go <c>commentIcon</c>: Reply when it writes a comment on an issue.</summary>
    public const string CommentIcon = "chat-message-new-symbolic";

    /// <summary>Reply's label: "Comment" on an account that comments, "Reply" otherwise.</summary>
    public static string ReplyLabel(ActionFlags f) => Jira.ReplyLabel(f.Comment);

    /// <summary>Reply's glyph (presentReply).</summary>
    public static string ReplyGlyph(ActionFlags f) => Icons.Glyph(f.Comment ? CommentIcon : "mail-reply-sender-symbolic");

    /// <summary>Whether the account offers <paramref name="kind"/> at all.</summary>
    public static bool Offers(ActionFlags f, MessageActionKind kind) => (f.Unsupported & kind) == 0;

    /// <summary>A button of <paramref name="kind"/>: shown while the account offers it.</summary>
    public static Visibility Shown(ActionFlags f, MessageActionKind kind) => Offers(f, kind) ? Visibility.Visible : Visibility.Collapsed;
}
