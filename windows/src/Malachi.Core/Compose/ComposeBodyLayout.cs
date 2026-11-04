// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/ComposeBodyLayout.swift; GTK:
// ui/internal/composebody/layout.go (EmptyBody, Layout, New, HasQuote, HTML,
// Text, WithOwn, DraftBody) and ui/internal/compose/quoted.go (bodyLayout
// with LayoutWindow). Swift's init(_ params:) is the constructor over
// ComposeParams, Go's New the one over a mode, the comment flag and the
// body; a caller that must keep a body whole (the board's inline reply)
// passes ComposeMode.New, as Go's doc says. Go's EscapeText is
// Prefill.EscapeText.

using System;
using Malachi.Core.Api;

namespace Malachi.Core.Compose;

/// <summary>
/// The body of a compose window in two parts: a new reply or forward starts
/// with the backend's complete quoted original (<c>draft.create</c>), which
/// the window shows apart from the editor, in a read-only panel under it;
/// the editor starts empty (<see cref="EmptyBody"/>). The panel's checkbox
/// decides whether the saved draft and the message include the original.
/// Anything else, a draft opened from the Drafts folder above all (its body
/// may hold the user's edits), a comment and the board's inline reply, opens
/// whole in the editor: nothing here looks for a quotation inside a body or
/// splits one.
/// </summary>
public sealed record ComposeBodyLayout
{
    /// <summary>composebody.EmptyBody: the editor's body above an original shown apart.</summary>
    public const string EmptyBody = "<p><br></p>";

    /// <summary>
    /// composebody.New: lays <paramref name="bodyHtml"/> out for a window of
    /// <paramref name="mode"/>: a reply, a reply to all or a forward that is
    /// not a <paramref name="comment"/>, with a body, has its body as the
    /// original and an empty editor; everything else has its body in the
    /// editor.
    /// </summary>
    public ComposeBodyLayout(ComposeMode mode, bool comment, string bodyHtml)
    {
        ArgumentNullException.ThrowIfNull(bodyHtml);
        (EditorHtml, QuotedHtml) = Split(mode, comment, bodyHtml);
    }

    /// <summary>
    /// Swift's <c>init(_ params:)</c> (compose/quoted.go <c>bodyLayout</c>
    /// with <c>LayoutWindow</c>): the layout of a window opened with
    /// <paramref name="p"/> (its kind's <c>draft.create</c> mode, so a draft
    /// opened from Drafts stays whole; a comment stays whole), started with
    /// the user's own text <see cref="ComposeParams.OwnHtml"/>
    /// (<see cref="WithOwn"/>). The inline layout builds params of its own
    /// (<see cref="ComposeKind.Edit"/>, no own text).
    /// </summary>
    public ComposeBodyLayout(ComposeParams p)
    {
        ArgumentNullException.ThrowIfNull(p);
        (EditorHtml, QuotedHtml) = Split(p.Kind.Mode, p.Comment is not null, p.BodyHtml);
        if (p.OwnHtml.Length > 0)
        {
            EditorHtml = p.OwnHtml;
        }
    }

    // WithOwn's copy: the parts as they are.
    private ComposeBodyLayout(string editorHtml, string quotedHtml)
    {
        EditorHtml = editorHtml;
        QuotedHtml = quotedHtml;
    }

    /// <summary>What the editor shows.</summary>
    public string EditorHtml { get; }

    /// <summary>The original the panel shows ("" without one).</summary>
    public string QuotedHtml { get; }

    /// <summary>composebody.HasQuote: whether the window shows an original apart.</summary>
    public bool HasQuote => QuotedHtml.Length > 0;

    /// <summary>
    /// composebody.Layout.WithOwn: the layout with <paramref name="own"/>,
    /// the user's own text written before the window opened (the chat's
    /// quick-reply bar; already safe HTML, <see cref="Model.Conversation.QuickReplyHtml"/>),
    /// in the editor in place of what it would start with; the original
    /// apart stays as it is. "" keeps the layout unchanged (this one).
    /// </summary>
    public ComposeBodyLayout WithOwn(string own)
    {
        ArgumentNullException.ThrowIfNull(own);
        return own.Length == 0 ? this : new ComposeBodyLayout(own, QuotedHtml);
    }

    /// <summary>
    /// composebody.DraftBody: the body of a draft (<c>draft.create</c>,
    /// <c>draft.open</c>) as HTML for a window or a quick reply: its
    /// <see cref="Draft.HtmlBody"/>, safe since the backend's sanitiser
    /// produced it; a draft without one (the backend could not quote
    /// formatted, or it is plain text) has its <see cref="Draft.TextBody"/>
    /// escaped (<see cref="Prefill.EscapeText"/>, composebody.EscapeText).
    /// </summary>
    public static string DraftBody(Draft draft)
    {
        ArgumentNullException.ThrowIfNull(draft);
        return draft.HtmlBody is { Length: > 0 } html ? html : Prefill.EscapeText(draft.TextBody);
    }

    /// <summary>
    /// composebody.HTML: the body to save and send: the editor's own HTML
    /// <paramref name="own"/>, and below it, when included, the original.
    /// </summary>
    public string Html(string own, bool include)
    {
        ArgumentNullException.ThrowIfNull(own);
        return include && HasQuote ? own + "\n" + QuotedHtml : own;
    }

    /// <summary>
    /// composebody.Text: the plain text of the body to save and send: the
    /// editor's own text <paramref name="own"/> and, when included, a blank
    /// line and the panel's text of the original, <paramref name="quote"/>.
    /// </summary>
    public string Text(string own, string quote, bool include)
    {
        ArgumentNullException.ThrowIfNull(own);
        ArgumentNullException.ThrowIfNull(quote);
        return include && HasQuote ? own + "\n\n" + quote : own;
    }

    // composebody.New's choice: the editor's part and the original.
    private static (string Editor, string Quoted) Split(ComposeMode mode, bool comment, string bodyHtml)
    {
        var quotes = mode.Value is ComposeMode.Reply or ComposeMode.ReplyAll or ComposeMode.Forward;
        return quotes && !comment && bodyHtml.Length > 0 ? (EmptyBody, bodyHtml) : (bodyHtml, "");
    }
}
