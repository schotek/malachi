// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/LoadedMessage.swift (the pane text:
// subjectText, bodyText, showsHTML, quotedTextOffer); GTK: ui/internal/window/message_view.go
// (subjectText, bodyText) and attachments.go (showsHTML).

using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Model;

/// <summary>The pure text helpers of the message pane.</summary>
public static class LoadedMessageText
{
    /// <summary>
    /// The subject to display; an empty one gets a placeholder
    /// (message_view.go <c>subjectText</c>). Windows-only: cleaned first
    /// (<see cref="DisplayText.Clean"/>, docs/security.md §4), so no control
    /// or explicit bidi character reaches the list, the reader, a window's
    /// caption or a question, and a subject of nothing else, or of nothing
    /// but characters that draw nothing, is "(No subject)"
    /// (<see cref="DisplayText.CleanTrimmed"/>).
    /// </summary>
    public static string SubjectText(string? subject)
    {
        var s = DisplayText.CleanTrimmed(subject);
        return s.Length > 0 ? s : L10n.T("(No subject)");
    }

    /// <summary>
    /// What the body label shows for a <c>message.body</c> result
    /// (message_view.go <c>bodyText</c>): a sentence for a body that is not
    /// there, otherwise the text without its trailing white space (Go's
    /// <c>strings.TrimRight(text, " \t\r\n")</c>, which cuts a CR LF as two
    /// characters).
    /// </summary>
    public static string BodyText(MessageBodyResult? b)
    {
        if (b is null)
        {
            return L10n.T("(Empty message)");
        }
        switch (b.BodyState.Value)
        {
            case BodyState.Pending:
                return L10n.T("Downloading…");
            case BodyState.TooBig:
                return L10n.T("This message is too large to download.");
            case BodyState.Failed:
                return L10n.T("This message could not be read.");
        }
        var text = b.Text.TrimEnd(' ', '\t', '\r', '\n');
        if (text.Trim().Length == 0)
        {
            return L10n.T("(Empty message)");
        }
        return text;
    }

    /// <summary>
    /// Whether the body goes into the HTML view (attachments.go
    /// <c>showsHTML</c>): fetched, with HTML.
    /// </summary>
    public static bool ShowsHtml(MessageBodyResult? b) =>
        b is not null && b.BodyState == BodyState.Fetched && !string.IsNullOrEmpty(b.Html);

    /// <summary>
    /// The button for what <paramref name="lm"/> shows (null: none; Swift
    /// <c>quotedTextOffer</c>): Hide while the whole body shows (or is on its
    /// way, or failed: the way back to the trimmed one), Show when the daemon
    /// cut the quoted history from the body on display.
    /// </summary>
    public static QuotedTextOffer? QuotedTextOfferFor(LoadedMessage? lm)
    {
        if (lm is null)
        {
            return null;
        }
        if (lm.QuotedShown)
        {
            return QuotedTextOffer.Hide;
        }
        return lm.Err is null && lm.Body is { IsQuotedTrimmed: true } ? QuotedTextOffer.Show : null;
    }
}
