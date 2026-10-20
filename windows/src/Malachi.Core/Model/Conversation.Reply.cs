// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/conversation/reply.go (QuickReplyTarget,
// QuickReplyModes, QuickReplyHTML, QuickReplyBlank, QuickReplyBody,
// QuickReplyPlaceholder, QuickReplyTargetTooltip, QuickReplyOpenWindowLabel,
// QuickReplyForwardLabel, SingleModel); macOS:
// MalachiCore/Model/ConversationReply.swift.
// Its type is QuickReplyMode (with QuickReplyModeExtensions); the bar's
// state and buttons are Controllers/QuickReplyController.cs. Go's (Item,
// bool) is a nullable item here.
//
// The chat layout of a conversation in Mail ends with a quick-reply bar, as
// a messaging app does: a fixed row under the bubbles with a button for the
// mode (Reply, Reply All, and Forward…, which opens the compose window
// straight away), a field of one to five lines for plain text, a button that
// opens the compose window with what was typed, and Send. The timeline, the
// board's chat and the message window have no bar. The same bar stands under
// the reading pane's page of a single message (docs/plans/chat-view.md §12),
// which comes to it as a model of one member (SingleModel).
//
// The reply goes to the newest message of someone else in the conversation
// (the newest message when the user wrote all of them; QuickReplyTarget), in
// a mode the account offers for it, as the card's buttons do
// (QuickReplyModes); an issue's conversation offers only a public comment.
// Send takes the daemon's usual way, as the compose window does:
// draft.create on the target, one draft.save whose body is the typed text
// above the original draft.create quoted (QuickReplyBody: what the window
// saves with "Include" checked), message.send. The text is the user's own,
// never mail: it becomes HTML here, paragraphs and line breaks with
// everything escaped (QuickReplyHtml), no Markdown. Nothing is cut: a text
// over API.Limits.MaxDraftBodyBytes is refused by the daemon's draft.save,
// and the client keeps it in the bar. The client holds the text typed per
// conversation in memory until it is sent, moved into a window or cleared
// (QuickReplyController); it is no daemon draft, so that the chat does not
// show it twice (as a draft bubble next to the bar).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

public static partial class Conversation
{
    /// <summary>
    /// conversation.QuickReplyTarget: the message a quick reply answers: the
    /// newest message card (<see cref="ConversationModel.Items"/> are oldest
    /// first) that is neither the user's own nor a sent card; when every
    /// message is the user's, the newest message card. Events and the row of
    /// older messages never are. Null when <paramref name="m"/> has no
    /// message card.
    /// </summary>
    public static ConversationItem? QuickReplyTarget(ConversationModel m)
    {
        ArgumentNullException.ThrowIfNull(m);
        ConversationItem? newest = null;
        for (var i = m.Items.Count - 1; i >= 0; i--)
        {
            var it = m.Items[i];
            if (it.Kind != ConversationItemKind.Message)
            {
                continue;
            }
            if (!it.Mine && !it.Sent)
            {
                return it;
            }
            newest ??= it;
        }
        return newest;
    }

    /// <summary>
    /// conversation.QuickReplyModes: the modes the bar offers for
    /// <paramref name="a"/>, the target's card actions
    /// (<see cref="CardActions"/>), in the menu's order: Reply, Reply All,
    /// Forward, each when <paramref name="a"/> has it. An account that
    /// comments (<see cref="CapabilityActions.Comment"/>) offers Reply only:
    /// the comment; Reply All and Forward are the window's. Empty when
    /// <paramref name="a"/> offers none of them (the bar is hidden then).
    /// </summary>
    public static IReadOnlyList<QuickReplyMode> QuickReplyModes(CapabilityActions a)
    {
        if (a.Comment)
        {
            return [QuickReplyMode.Reply];
        }
        var output = new List<QuickReplyMode>(3);
        if (a.Reply)
        {
            output.Add(QuickReplyMode.Reply);
        }
        if (a.ReplyAll)
        {
            output.Add(QuickReplyMode.ReplyAll);
        }
        if (a.Forward)
        {
            output.Add(QuickReplyMode.Forward);
        }
        return output;
    }

    /// <summary>
    /// conversation.QuickReplyHTML: the HTML of the user's own text typed in
    /// the bar: line ends normalised (CRLF and CR to LF), paragraphs split on
    /// one or more blank lines (a line of white space only is blank), each
    /// paragraph a <c>&lt;p&gt;</c> with its lines joined by
    /// <c>&lt;br&gt;</c>, every line escaped as html.EscapeString does
    /// (<see cref="Prefill.EscapeText"/>, which a line without a line end
    /// matches); blank lines before the first and after the last paragraph
    /// are dropped. "" for text with no line that is not blank. It never
    /// cuts: the daemon refuses a body over
    /// <see cref="API.Limits.MaxDraftBodyBytes"/> in <c>draft.save</c>, and
    /// the client keeps the text.
    /// </summary>
    public static string QuickReplyHtml(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var normalised = text.Replace("\r\n", "\n", StringComparison.Ordinal).Replace('\r', '\n');
        var output = new StringBuilder(normalised.Length + 16);
        var para = new List<string>();
        void Flush()
        {
            if (para.Count == 0)
            {
                return;
            }
            output.Append("<p>");
            for (var i = 0; i < para.Count; i++)
            {
                if (i > 0)
                {
                    output.Append("<br>");
                }
                output.Append(Prefill.EscapeText(para[i]));
            }
            output.Append("</p>");
            para.Clear();
        }
        foreach (var line in normalised.Split('\n'))
        {
            // Go's strings.TrimSpace: white space by the Unicode property,
            // as char.IsWhiteSpace has it (every such character is one
            // UTF-16 unit).
            if (string.IsNullOrWhiteSpace(line))
            {
                Flush();
                continue;
            }
            para.Add(line);
        }
        Flush();
        return output.ToString();
    }

    /// <summary>
    /// conversation.QuickReplyBlank: a text with nothing to send: white
    /// space, control and format (Cf) characters only. Send is disabled then.
    /// The rule of <see cref="Jira.SendProblem"/>, used as is (Go mirrors it
    /// exactly).
    /// </summary>
    public static bool QuickReplyBlank(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        return Jira.SendProblem(text).Length > 0;
    }

    /// <summary>
    /// conversation.QuickReplyBody: the body <c>draft.save</c> gets for a
    /// quick reply in <paramref name="mode"/> with the user's own text
    /// <paramref name="own"/> on <paramref name="draft"/>
    /// (<c>draft.create</c>'s result): byte for byte what the compose window
    /// saves with "Include" checked, the own HTML
    /// (<see cref="QuickReplyHtml"/>), a line break and the original
    /// <c>draft.create</c> quoted (<see cref="ComposeBodyLayout.DraftBody"/>);
    /// a comment draft (and a draft without a body) is the own HTML alone.
    /// </summary>
    public static string QuickReplyBody(ComposeMode mode, string own, Draft draft)
    {
        ArgumentNullException.ThrowIfNull(own);
        ArgumentNullException.ThrowIfNull(draft);
        return new ComposeBodyLayout(mode, draft.Comment is not null, ComposeBodyLayout.DraftBody(draft))
            .Html(QuickReplyHtml(own), include: true);
    }

    /// <summary>
    /// conversation.QuickReplyPlaceholder: the placeholder and accessible
    /// name of the bar's field: <paramref name="comment"/> says the reply is
    /// a comment on an issue.
    /// </summary>
    public static string QuickReplyPlaceholder(bool comment)
    {
        if (comment)
        {
            // TRANSLATORS: the placeholder of the quick-reply field under an issue's conversation shown as chat; what is typed becomes a comment.
            return L10n.T("Write a comment…");
        }
        // TRANSLATORS: the placeholder of the quick-reply field under a conversation shown as chat.
        return L10n.T("Write a reply…");
    }

    /// <summary>
    /// conversation.QuickReplyTargetTooltip: the tooltip of the bar's mode
    /// button, which names the target's sender
    /// (<see cref="ConversationItem.Sender"/>, already cleaned for one line).
    /// "" for <see cref="QuickReplyMode.Forward"/> and when the target has no
    /// sender: the button keeps only its own label then (Reply, Reply All).
    /// </summary>
    public static string QuickReplyTargetTooltip(QuickReplyMode mode, string sender)
    {
        if (string.IsNullOrEmpty(sender))
        {
            return "";
        }
        switch (mode)
        {
            case QuickReplyMode.Reply:
                // TRANSLATORS: the tooltip of the quick reply's mode button; %s is the sender of the message the reply goes to, as it shows in the conversation (no inflection).
                return L10n.T("Reply to: %s", sender);
            case QuickReplyMode.ReplyAll:
                // TRANSLATORS: the tooltip of the quick reply's mode button set to reply to all; %s is the sender of the message the reply goes to, as it shows in the conversation (no inflection).
                return L10n.T("Reply all to: %s", sender);
            default:
                return "";
        }
    }

    /// <summary>
    /// conversation.QuickReplyOpenWindowLabel: the tooltip and accessible
    /// name of the bar's button that opens the compose window with what was
    /// typed.
    /// </summary>
    public static string QuickReplyOpenWindowLabel() =>
        // TRANSLATORS: the tooltip of the quick-reply bar's button that moves the typed reply into the compose window.
        L10n.T("Open in a Window");

    /// <summary>
    /// conversation.QuickReplyForwardLabel: the item of the mode button's
    /// menu that opens the compose window with a forward of the target (and
    /// the typed text above it).
    /// </summary>
    public static string QuickReplyForwardLabel() =>
        // TRANSLATORS: an item of the quick-reply bar's mode menu; opens the compose window with the message forwarded.
        L10n.T("Forward…");

    /// <summary>
    /// conversation.SingleModel: the model of one message for the quick-reply
    /// bar under the reading pane's page of a single message
    /// (docs/plans/chat-view.md §12, S2, S4): the bar answers that message,
    /// whoever wrote it (in Sent a reply to the user's own message), so
    /// <see cref="Controllers.QuickReplyController.Update"/> takes the same
    /// model shape as for a conversation and <see cref="QuickReplyTarget"/>
    /// needs no rule of its own. The model's thread is the message's (the bar
    /// keeps the typed text per account and thread, shared with the
    /// message's conversation; "" without one); its one item is a
    /// <see cref="ConversationItemKind.Message"/> with the cleaned sender
    /// (<see cref="ConversationItem.Sender"/>, which names the target in the
    /// mode button's tooltip) and <paramref name="mine"/> as the caller says
    /// (the rule of <see cref="ConversationItem.Mine"/>:
    /// <see cref="IsMine"/>). An event of an issue (a row without a body) is
    /// an <see cref="ConversationItemKind.Event"/> without its sentences, so
    /// that the bar finds no target and hides; a message without an id makes
    /// an empty model. The model is for the bar only: no issue card, nothing
    /// to mark read, never rendered.
    /// </summary>
    public static ConversationModel SingleModel(MessageSummary s, bool mine)
    {
        ArgumentNullException.ThrowIfNull(s);
        var m = new ConversationModel { Thread = s.ThreadId ?? new ThreadId("") };
        if (string.IsNullOrEmpty(s.Id.Value))
        {
            return m;
        }
        var it = new ConversationItem
        {
            Kind = ConversationItemKind.Message,
            Message = s,
            Sender = Sender(s.From),
            Mine = mine,
            Unread = !s.Flags.Contains(Flag.Seen),
        };
        if (Jira.IsEvent(s.Issue))
        {
            it = it with { Kind = ConversationItemKind.Event, Unread = false };
        }
        return m with { Items = [it], ScrollTo = 0 };
    }
}
