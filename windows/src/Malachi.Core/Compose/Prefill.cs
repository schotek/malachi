// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/Prefill.swift; GTK:
// ui/internal/compose/prefill.go (FromDraft, Prefill, Attribution,
// ReplySubject, ForwardSubject, escapeText, dedupeAddresses, replyTargets,
// quoteHTML, forwardHTML).
//
// Reply, reply-all and forward parameters built by the client itself (the
// fallback when draft.create cannot be asked), the attribution line handed
// to draft.create, and the window parameters made from a draft.create
// result. Swift's prefill(kind:source:self:) is Prefill.Create (C# allows no
// member named like its type).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using System.Text.RegularExpressions;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Compose;

/// <summary>The parameters of a compose window for a reply, a forward or a draft.</summary>
public static partial class Prefill
{
    private const string Ellipsis = "…";

    /// <summary>
    /// compose.FromDraft: turns a <c>draft.create</c> or <c>draft.open</c>
    /// result into window parameters. A draft without HTML (the daemon could
    /// not quote formatted, or it is plain text) shows its text.
    /// </summary>
    public static ComposeParams FromDraft(ComposeKind kind, Draft draft, BlockedContent blocked)
    {
        ArgumentNullException.ThrowIfNull(draft);
        ArgumentNullException.ThrowIfNull(blocked);
        var body = draft.HtmlBody ?? "";
        if (body.Length == 0)
        {
            body = EscapeText(draft.TextBody);
        }
        return new ComposeParams
        {
            Kind = kind,
            AccountId = string.IsNullOrEmpty(draft.AccountId.Value) ? null : (AccountId?)draft.AccountId,
            To = draft.To,
            Cc = draft.Cc ?? [],
            Bcc = draft.Bcc ?? [],
            Subject = draft.Subject,
            BodyHtml = body,
            InReplyTo = draft.InReplyTo,
            Forwarding = draft.Forwarding,
            Attachments = draft.Attachments ?? [],
            Blocked = blocked,
            DraftId = draft.Id,
            Version = draft.Version,
            Replaces = draft.Replaces,
        };
    }

    /// <summary>
    /// compose.Prefill: builds the reply / reply-all / forward parameters from
    /// <paramref name="source"/> on the client's own: the fallback when
    /// <c>draft.create</c> cannot be asked (no daemon), quoting the plain text
    /// only. The normal path is <c>draft.create</c>, which quotes the original
    /// formatted, with its pictures.
    /// </summary>
    public static ComposeParams Create(ComposeKind kind, ComposeSource source, Address self)
    {
        ArgumentNullException.ThrowIfNull(source);
        ArgumentNullException.ThrowIfNull(self);
        switch (kind)
        {
            case ComposeKind.Reply:
                return new ComposeParams
                {
                    Kind = kind,
                    To = DedupeAddresses(ReplyTargets(source), []),
                    Subject = ReplySubject(source.Subject),
                    BodyHtml = QuoteHtml(kind, source),
                    InReplyTo = NonEmptyId(source.Id),
                };
            case ComposeKind.ReplyAll:
                var to = DedupeAddresses(ReplyTargets(source), []);
                return new ComposeParams
                {
                    Kind = kind,
                    To = to,
                    Cc = DedupeAddresses([.. source.To, .. source.Cc], [self, .. to]),
                    Subject = ReplySubject(source.Subject),
                    BodyHtml = QuoteHtml(kind, source),
                    InReplyTo = NonEmptyId(source.Id),
                };
            case ComposeKind.Forward:
                return new ComposeParams
                {
                    Kind = kind,
                    Subject = ForwardSubject(source.Subject),
                    BodyHtml = ForwardHtml(kind, source),
                    Forwarding = NonEmptyId(source.Id),
                };
            default:
                return new ComposeParams { Kind = kind };
        }
    }

    /// <summary>
    /// compose.Attribution: the line above a quote in the user's language:
    /// "On &lt;date&gt;, &lt;sender&gt; wrote:" for a reply, the header block
    /// of a forwarded message. Plain text, lines separated by "\n", nothing
    /// escaped: it is what <c>draft.create</c> is handed (the daemon escapes
    /// it) and what the fallback quote escapes itself. Empty for a new
    /// message. Cut to the daemon's cap
    /// (<see cref="API.Limits.MaxDraftAttributionBytes"/>) with a trailing
    /// ellipsis on a UTF-8 boundary.
    /// </summary>
    public static string Attribution(ComposeKind kind, ComposeSource source)
    {
        ArgumentNullException.ThrowIfNull(source);
        string s;
        var date = SourceDate(source);
        switch (kind)
        {
            case ComposeKind.Reply or ComposeKind.ReplyAll:
                var names = DisplayNames(source.From);
                s = date is { } d
                    // TRANSLATORS: quote header; %s are the date and the sender.
                    ? L10n.T("On %s, %s wrote:", FormatDateTime(d), names)
                    // TRANSLATORS: quote header without a date; %s is the sender.
                    : L10n.T("%s wrote:", names);
                break;
            case ComposeKind.Forward:
                var lines = new List<string>
                {
                    L10n.T("---------- Forwarded message ----------"),
                    L10n.T("From: %s", FormatAll(source.From)),
                };
                if (date is { } when)
                {
                    lines.Add(L10n.T("Date: %s", FormatDateTime(when)));
                }
                lines.Add(L10n.T("Subject: %s", source.Subject));
                if (source.To.Count > 0)
                {
                    lines.Add(L10n.T("To: %s", FormatAll(source.To)));
                }
                s = string.Join("\n", lines);
                break;
            default:
                return "";
        }
        // Go's strings.ToValidUTF8: a lone surrogate (a name the daemon could
        // not decode) is U+FFFD, then the daemon's cap; a message to hundreds
        // of people has a To: line that would break it.
        var bytes = Encoding.UTF8.GetBytes(s);
        const int Cap = API.Limits.MaxDraftAttributionBytes;
        if (bytes.Length <= Cap)
        {
            return Encoding.UTF8.GetString(bytes);
        }
        var n = Cap - Encoding.UTF8.GetByteCount(Ellipsis);
        // Back off to a UTF-8 boundary: never cut inside a scalar.
        while (n > 0 && (bytes[n] & 0xC0) == 0x80)
        {
            n--;
        }
        return Encoding.UTF8.GetString(bytes, 0, n) + Ellipsis;
    }

    /// <summary>
    /// compose.ReplySubject: "Re: " + subject without existing prefixes
    /// (idempotent). The prefixes are deliberately not translated: other
    /// clients only recognise the English forms when threading and
    /// de-duplicating them.
    /// </summary>
    public static string ReplySubject(string s) => "Re: " + StripPrefixes(s);

    /// <summary>compose.ForwardSubject: "Fwd: " + subject without existing prefixes.</summary>
    public static string ForwardSubject(string s) => "Fwd: " + StripPrefixes(s);

    /// <summary>
    /// compose.escapeText: escapes plain text for HTML (as html.EscapeString:
    /// <c>&lt;&gt;&amp;'"</c>) and turns newlines into &lt;br&gt;.
    /// </summary>
    public static string EscapeText(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        var text = s.Replace("\r\n", "\n", StringComparison.Ordinal);
        var output = new StringBuilder(text.Length);
        foreach (var c in text)
        {
            switch (c)
            {
                case '&':
                    output.Append("&amp;");
                    break;
                case '\'':
                    output.Append("&#39;");
                    break;
                case '<':
                    output.Append("&lt;");
                    break;
                case '>':
                    output.Append("&gt;");
                    break;
                case '"':
                    output.Append("&#34;");
                    break;
                case '\n':
                    output.Append("<br>");
                    break;
                default:
                    output.Append(c);
                    break;
            }
        }
        return output.ToString();
    }

    /// <summary>
    /// compose.dedupeAddresses: keeps the first occurrence of each address
    /// (case-insensitively) that is not in <paramref name="exclude"/>.
    /// </summary>
    public static IReadOnlyList<Address> DedupeAddresses(IEnumerable<Address> input, IEnumerable<Address> exclude)
    {
        ArgumentNullException.ThrowIfNull(input);
        ArgumentNullException.ThrowIfNull(exclude);
        var seen = new HashSet<string>(exclude.Select(AddressKey), StringComparer.Ordinal);
        var output = new List<Address>();
        foreach (var a in input)
        {
            var key = AddressKey(a);
            if (key.Length == 0 || !seen.Add(key))
            {
                continue;
            }
            output.Add(a);
        }
        return output;
    }

    /// <summary>compose.replyTargets: where a reply goes: Reply-To when the sender set one, otherwise From.</summary>
    internal static IReadOnlyList<Address> ReplyTargets(ComposeSource source) =>
        source.ReplyTo.Count == 0 ? source.From : source.ReplyTo;

    /// <summary>
    /// compose.quoteHTML: the original's text as a cite block under the
    /// attribution and an empty paragraph for the answer, the layout
    /// <c>draft.create</c> produces. Everything from the source is escaped.
    /// </summary>
    internal static string QuoteHtml(ComposeKind kind, ComposeSource source) =>
        "<p><br></p><div>" + EscapeText(Attribution(kind, source)) + "</div><blockquote type=\"cite\">"
        + EscapeText(source.Text) + "</blockquote>";

    /// <summary>compose.forwardHTML: the forwarded-message header block and the text.</summary>
    internal static string ForwardHtml(ComposeKind kind, ComposeSource source) =>
        "<p><br></p><div>" + EscapeText(Attribution(kind, source)) + "</div><br>" + EscapeText(source.Text);

    // compose.subjectPrefix: (?i)^\s*(re|fwd?|aw|wg)\s*:\s*, with RE2's ASCII
    // \s.
    [GeneratedRegex(@"^[\t\n\f\r ]*(re|fwd?|aw|wg)[\t\n\f\r ]*:[\t\n\f\r ]*", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant)]
    private static partial Regex SubjectPrefix();

    // compose.stripPrefixes: removes any number of Re:/Fwd:-style prefixes.
    private static string StripPrefixes(string subject)
    {
        ArgumentNullException.ThrowIfNull(subject);
        var s = subject;
        while (SubjectPrefix().Match(s) is { Success: true } m)
        {
            s = s[(m.Index + m.Length)..];
        }
        return s.Trim();
    }

    private static MessageId? NonEmptyId(MessageId id) => string.IsNullOrEmpty(id.Value) ? null : (MessageId?)id;

    // The source's date, null for none (Go's zero time counts as none).
    private static DateTimeOffset? SourceDate(ComposeSource source) =>
        source.Date is { } date && !date.IsGoZero ? date : null;

    private static string AddressKey(Address a) => (a.Email ?? "").Trim().ToLowerInvariant();

    // compose.displayNames: widget.DisplayName of each, comma-separated. The
    // text goes into the draft, so it is the received one (Format's
    // AsReceived forms), not what the screen shows of it.
    private static string DisplayNames(IEnumerable<Address> list) => string.Join(", ", list.Select(Format.NameAsReceived));

    // compose.formatAll: widget.FormatAddress of each, comma-separated, as received.
    private static string FormatAll(IEnumerable<Address> list) => string.Join(", ", list.Select(Format.AddressAsReceived));

    // widget.FormatDateTime for the quote header, in the catalogue's
    // language, so day and month names match the sentence around them.
    private static string FormatDateTime(DateTimeOffset date)
    {
        CultureInfo culture;
        try
        {
            culture = CultureInfo.GetCultureInfo(L10n.Catalogue.Language);
        }
        catch (CultureNotFoundException)
        {
            culture = CultureInfo.InvariantCulture;
        }
        return Format.FormatDateTime(date, culture);
    }
}
