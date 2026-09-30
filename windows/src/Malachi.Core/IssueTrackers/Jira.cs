// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraView.swift and JiraURL.swift;
// GTK: ui/internal/jira/jira.go (KindBadge, CloudName, DataCenterName,
// emptyValue, maxText, StyleOf, Clean, truncate, DeploymentName, IsJira,
// AlwaysThreaded, SiteHost, AccountLabel, VirtualFolderTitle, VirtualRank,
// VirtualIcon, IssueCard, IsInternal, InternalLabel, IsEvent, EventLines,
// EventText, RowIssue, ThreadRowIssue, AuthBannerText, IsIssueURL, portOf,
// hostKey, punycode).
//
// The view logic of issue-tracker accounts (kind jira, docs/api.md): what
// the sidebar, the message list and the reading pane show for Jira spaces,
// issues, comments and status or assignee changes, and which link opens an
// issue. The daemon does the work (synchronisation, sanitising, comments);
// this only turns its API values into texts and small view models, one to
// one with the Go package (Jira.Wizard.cs ports wizard.go, Jira.Compose.cs
// compose.go, Jira.Transitions.cs transitions.go, Jira.Settings.cs
// settings.go). Go's Translator is L10n here: every text goes through it
// with the GTK msgid.
//
// Every string of an issue except its key and URL is display text from the
// site and hostile input: Clean makes it safe to lay out on one line and
// the views show it as plain text only. Brand names (Jira, Jira Cloud, Jira
// Data Center, id.atlassian.com) are not translated.
//
// Windows: the package's types are top-level (JiraStatusStyle, JiraCardRow,
// JiraCard, JiraIssueRow: Swift's Jira.StatusStyle, CardRow, Card,
// IssueRow), as a member of Jira named like a type would hide it, and the
// namespace is IssueTrackers because a namespace Jira would hide the class
// from every other Malachi.Core namespace. Clean drops what Go drops:
// invalid UTF-8, which in a C# string is a lone surrogate, while a U+FFFD
// the site sent stays as in Go (Swift drops every U+FFFD). Lower case is
// Go's, rune by rune (CodePoints.ToLower; Swift uses its full mapping), Go's
// character classes are the Assistant port's (White_Space and Cc), and
// url.Parse is UrlSyntax, the port of net/url the link checks use.

using System;
using System.Buffers;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.Model;

namespace Malachi.Core.IssueTrackers;

/// <summary>
/// ui/internal/jira: a namespace of functions, so that the Go and Swift
/// names map 1:1 (<c>jira.IssueCard</c>, Swift <c>Jira.issueCard</c>, here
/// <c>Jira.IssueCard</c>).
/// </summary>
public static partial class Jira
{
    /// <summary>
    /// jira.KindBadge: the capsule next to a Jira account's name in the
    /// sidebar. A brand name, never translated.
    /// </summary>
    public const string KindBadge = "JIRA";

    /// <summary>jira.CloudName: the brand name of the cloud deployment, never translated.</summary>
    public const string CloudName = "Jira Cloud";

    /// <summary>jira.DataCenterName: the brand name of the self-hosted deployment, never translated.</summary>
    public const string DataCenterName = "Jira Data Center";

    /// <summary>jira.emptyValue: a missing side of a change (an em dash).</summary>
    internal const string EmptyValue = "—";

    /// <summary>jira.maxText: the cap of a cleaned display string, in UTF-8 bytes.</summary>
    internal const int MaxText = 512;

    /// <summary>
    /// jira.StyleOf: the style of a status category; an unknown or empty
    /// category is <see cref="JiraStatusStyle.Plain"/> (the category is an
    /// open enum).
    /// </summary>
    public static JiraStatusStyle StyleOf(IssueStatusCategory? c) => c?.Value switch
    {
        IssueStatusCategory.Todo => JiraStatusStyle.Todo,
        IssueStatusCategory.InProgress => JiraStatusStyle.InProgress,
        IssueStatusCategory.Done => JiraStatusStyle.Done,
        _ => JiraStatusStyle.Plain,
    };

    /// <summary>
    /// jira.Clean: display text from the site made safe to lay out on one
    /// line: drops invalid UTF-8 (a lone surrogate here), format characters
    /// (Cf: bidirectional overrides such as U+202E, zero-width characters,
    /// the soft hyphen) and control characters, turns line breaks, tabs and
    /// the line and paragraph separators into spaces, collapses runs of
    /// spaces, trims, and caps the result at 512 bytes on a character
    /// boundary.
    /// </summary>
    public static string Clean(string? s)
    {
        if (string.IsNullOrEmpty(s))
        {
            return "";
        }
        var b = new StringBuilder(s.Length);
        var space = false;
        for (var i = 0; i < s.Length;)
        {
            var status = Rune.DecodeFromUtf16(s.AsSpan(i), out var r, out var used);
            i += Math.Max(used, 1);
            if (status != OperationStatus.Done)
            {
                continue;
            }
            // unicode.IsSpace is White_Space, which holds Zl and Zp.
            if (Assistant.IsSpace(r.Value))
            {
                space = b.Length > 0;
                continue;
            }
            if (Assistant.IsControl(r.Value) || IsFormat(r.Value))
            {
                continue;
            }
            if (space)
            {
                b.Append(' ');
                space = false;
            }
            b.Append(r.ToString());
        }
        return Truncate(b.ToString(), MaxText);
    }

    /// <summary>
    /// jira.truncate: <paramref name="s"/> cut to at most
    /// <paramref name="n"/> UTF-8 bytes on a character boundary, spaces after
    /// the cut trimmed.
    /// </summary>
    internal static string Truncate(string s, int n)
    {
        if (Encoding.UTF8.GetByteCount(s) <= n)
        {
            return s;
        }
        var bytes = 0;
        var end = 0;
        foreach (var r in s.EnumerateRunes())
        {
            if (bytes + r.Utf8SequenceLength > n)
            {
                break;
            }
            bytes += r.Utf8SequenceLength;
            end += r.Utf16SequenceLength;
        }
        return s[..end].TrimEnd(' ');
    }

    /// <summary>unicode.Is(unicode.Cf, r): a format character.</summary>
    internal static bool IsFormat(int r) =>
        Rune.IsValid(r) && Rune.GetUnicodeCategory(new Rune(r)) == UnicodeCategory.Format;

    /// <summary>jira.DeploymentName: the brand name of a deployment; "Jira" for an unknown one.</summary>
    public static string DeploymentName(JiraDeployment d) => d.Value switch
    {
        JiraDeployment.Cloud => CloudName,
        JiraDeployment.Datacenter => DataCenterName,
        _ => "Jira",
    };

    /// <summary>jira.IsJira: an account of kind jira.</summary>
    public static bool IsJira(AccountConfig cfg)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        return cfg.ProtocolKind == AccountKind.Jira;
    }

    /// <summary>
    /// jira.AlwaysThreaded: an account whose folders are always listed as
    /// conversations (thread.list), whatever the "group by conversation"
    /// setting: a Jira issue is one thread.
    /// </summary>
    public static bool AlwaysThreaded(AccountConfig cfg) => IsJira(cfg);

    /// <summary>
    /// jira.SiteHost: the host of a Jira account's site, lower-case and
    /// without port ("acme.atlassian.net"); "" for another kind of account or
    /// a site that is not a URL.
    /// </summary>
    public static string SiteHost(AccountConfig cfg)
    {
        if (!IsJira(cfg) || cfg.Jira is null || UrlSyntax.Parse(cfg.Jira.SiteUrl.Trim()) is not { } u)
        {
            return "";
        }
        return Clean(CodePoints.ToLower(UrlSyntax.SplitHostPort(u.Rest.Host).Host));
    }

    /// <summary>
    /// jira.AccountLabel: the name the sidebar and the settings show for an
    /// account: its name; for a Jira account without one the site's host;
    /// else its e-mail address (model.go accountLabel for mail accounts).
    /// </summary>
    public static string AccountLabel(AccountConfig cfg)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        var name = cfg.Name.Trim();
        if (name.Length > 0)
        {
            return name;
        }
        var host = SiteHost(cfg);
        return host.Length > 0 ? host : cfg.Email.Trim();
    }

    /// <summary>
    /// jira.VirtualFolderTitle: the localised name of a fixed view of a
    /// Jira account; "" for no view or one this client does not know (show
    /// the folder's name then).
    /// </summary>
    public static string VirtualFolderTitle(VirtualFolder? v) => v?.Value switch
    {
        // TRANSLATORS: a folder of a Jira account: the issues assigned to the user.
        VirtualFolder.AssignedToMe => L10n.C("folder", "Assigned to Me"),
        // TRANSLATORS: a folder of a Jira account: the issues the user watches.
        VirtualFolder.Watching => L10n.C("folder", "Watching"),
        // TRANSLATORS: a folder of a Jira account: the issues that are not closed yet.
        VirtualFolder.Open => L10n.C("folder", "Open"),
        _ => "",
    };

    /// <summary>
    /// jira.VirtualRank: the order of the fixed views in the sidebar: below
    /// the account's mail role folders, above its spaces. 100 for no view or
    /// an unknown one, like an ordinary folder.
    /// </summary>
    public static int VirtualRank(VirtualFolder? v) => v?.Value switch
    {
        VirtualFolder.AssignedToMe => 0,
        VirtualFolder.Watching => 1,
        VirtualFolder.Open => 2,
        _ => 100,
    };

    /// <summary>
    /// jira.VirtualIcon: the icon of a fixed view (a GTK icon name, which
    /// IconGlyphs maps to a glyph); "" for no view.
    /// </summary>
    public static string VirtualIcon(VirtualFolder? v) =>
        string.IsNullOrEmpty(v?.Value) ? "" : "folder-saved-search-symbolic";

    /// <summary>
    /// jira.IssueCard: the card of <paramref name="issue"/>;
    /// <paramref name="item"/>, when not null, is the message's part of it
    /// (<see cref="MessageSummary.Issue"/>) and adds the internal badge,
    /// <see cref="JiraCard.Via"/> and <see cref="JiraCard.Edited"/>.
    /// </summary>
    public static JiraCard IssueCard(IssueInfo issue, MessageIssue? item = null)
    {
        ArgumentNullException.ThrowIfNull(issue);
        var key = Clean(issue.Key);
        JiraCardRow None(string? v)
        {
            v = Clean(v);
            if (v.Length > 0)
            {
                return new JiraCardRow { Value = v };
            }
            // TRANSLATORS: the value of an empty field of a Jira issue (priority, type, reporter).
            return new JiraCardRow { Value = L10n.C("jira value", "None"), Missing = true };
        }
        var assignee = Clean(issue.Assignee) is { Length: > 0 } a
            ? new JiraCardRow { Value = a }
            : new JiraCardRow { Value = L10n.T("Unassigned"), Missing = true };
        var c = new JiraCard
        {
            Key = key,
            Url = issue.Url.Trim(),
            // TRANSLATORS: tooltip of an issue key such as "ITSD-42".
            OpenTooltip = L10n.T("Open %s in the Browser", key),
            Summary = Clean(issue.Summary),
            Status = Clean(issue.Status),
            // TRANSLATORS: a field of a Jira issue.
            StatusLabel = L10n.T("Status"),
            StatusStyle = StyleOf(issue.StatusCategory),
            Rows =
            [
                // TRANSLATORS: a field of a Jira issue: the person who works on it.
                assignee with { Label = L10n.T("Assignee") },
                // TRANSLATORS: a field of a Jira issue.
                None(issue.Priority) with { Label = L10n.T("Priority") },
                // TRANSLATORS: a field of a Jira issue: its type, such as Bug or Task.
                None(issue.Type) with { Label = L10n.T("Type") },
                // TRANSLATORS: a field of a Jira issue: the person who created it.
                None(issue.Reporter) with { Label = L10n.T("Reporter") },
            ],
        };
        if (item is null)
        {
            return c;
        }
        if (IsInternal(item))
        {
            c = c with { Internal = true, InternalLabel = InternalLabel() };
        }
        if (Clean(item.Via) is { Length: > 0 } via)
        {
            // TRANSLATORS: %s is an integration (a bot) that posted a comment on its author's behalf.
            c = c with { Via = L10n.T("via %s", via) };
        }
        if (item.Edited == true)
        {
            // TRANSLATORS: a comment of a Jira issue was changed after it was posted.
            c = c with { Edited = L10n.T("Edited") };
        }
        return c;
    }

    /// <summary>jira.IsInternal: an internal comment of a service-desk issue.</summary>
    public static bool IsInternal(MessageIssue? item) =>
        item is not null && item.Item == IssueItemKind.Comment && item.Visibility == CommentVisibility.Internal;

    /// <summary>jira.InternalLabel: the badge of an internal comment.</summary>
    public static string InternalLabel() =>
        // TRANSLATORS: badge of a comment only the service-desk team can read.
        L10n.C("jira", "Internal");

    /// <summary>jira.IsEvent: a message that stands for status or assignee changes.</summary>
    public static bool IsEvent(MessageIssue? item) => item is not null && item.Item == IssueItemKind.Event;

    /// <summary>
    /// jira.EventLines: the sentences of an event message, one per change it
    /// knows ("Status: To Do → In Progress"); a change of a field this client
    /// does not know is skipped (the field is an open enum). An empty side is
    /// "Unassigned" for the assignee and "—" otherwise.
    /// </summary>
    public static IReadOnlyList<string> EventLines(IReadOnlyList<IssueChange>? changes)
    {
        var output = new List<string>();
        foreach (var ch in changes ?? [])
        {
            var from = Clean(ch.From);
            var to = Clean(ch.To);
            switch (ch.Field.Value)
            {
                case IssueField.Status:
                    // TRANSLATORS: an issue's status changed; the first %s is the old status, the second the new one.
                    output.Add(L10n.T("Status: %s → %s", OrEmpty(from), OrEmpty(to)));
                    break;
                case IssueField.Assignee:
                    if (from.Length == 0)
                    {
                        from = L10n.T("Unassigned");
                    }
                    if (to.Length == 0)
                    {
                        to = L10n.T("Unassigned");
                    }
                    // TRANSLATORS: an issue was assigned to someone else; the first %s is the old assignee, the second the new one.
                    output.Add(L10n.T("Assignee: %s → %s", from, to));
                    break;
                default:
                    break;
            }
        }
        return output;
    }

    /// <summary>jira.EventText: <see cref="EventLines"/> on one line, for the message list.</summary>
    public static string EventText(IReadOnlyList<IssueChange>? changes)
    {
        var lines = EventLines(changes);
        if (lines.Count == 0)
        {
            return "";
        }
        // TRANSLATORS: put between two changes of an issue on one line ("Status: A → B; Assignee: C → D").
        return string.Join(L10n.C("change list separator", "; "), lines);
    }

    private static string OrEmpty(string s) => s.Length == 0 ? EmptyValue : s;

    /// <summary>jira.RowIssue: the issue part of a message row; null for a message of a mail account.</summary>
    public static JiraIssueRow? RowIssue(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (s.Issue is not { } issue)
        {
            return null;
        }
        var r = NewRow(issue.Info, issue);
        return r with { Unread = !r.Event && !s.Flags.Contains(Flag.Seen) };
    }

    /// <summary>
    /// jira.ThreadRowIssue: the issue part of a conversation row: the
    /// thread's issue, and the badge or the event text of its latest member.
    /// Null for a conversation of a mail account.
    /// </summary>
    public static JiraIssueRow? ThreadRowIssue(ThreadSummary t)
    {
        ArgumentNullException.ThrowIfNull(t);
        if ((t.Issue ?? t.Latest.Issue?.Info) is not { } info)
        {
            return null;
        }
        return NewRow(info, t.Latest.Issue) with { Unread = t.UnreadCount > 0 };
    }

    private static JiraIssueRow NewRow(IssueInfo info, MessageIssue? item)
    {
        var r = new JiraIssueRow
        {
            Key = Clean(info.Key),
            Summary = Clean(info.Summary),
            Status = Clean(info.Status),
            StatusStyle = StyleOf(info.StatusCategory),
        };
        if (IsInternal(item))
        {
            r = r with { Internal = true, InternalLabel = InternalLabel() };
        }
        if (IsEvent(item))
        {
            r = r with { Event = true, EventText = EventText(item!.Changes) };
        }
        return r;
    }

    /// <summary>
    /// jira.AuthBannerText: the sign-in banner of a Jira account for a
    /// notify.authRequired reason; <paramref name="account"/> is the
    /// account's display name. "" for another kind of account, and for a
    /// reason whose sentence is the mail accounts' (a keyring failure).
    /// </summary>
    public static string AuthBannerText(AccountKind kind, ErrorCode reason, string account)
    {
        if (kind != AccountKind.Jira)
        {
            return "";
        }
        return reason.Value switch
        {
            // TRANSLATORS: banner; %s is an account name.
            ErrorCode.AuthRequired => L10n.T("No API token is stored for %s", account),
            // TRANSLATORS: banner; %s is an account name.
            ErrorCode.AuthFailed => L10n.T("The Jira site rejected the token of %s", account),
            _ => "",
        };
    }

    /// <summary>
    /// jira.IsIssueURL: a link that may be opened as an issue of the site
    /// <paramref name="siteUrl"/> (<see cref="JiraConfig.SiteUrl"/>): an
    /// absolute https URL, or http when the site itself is http, without
    /// user info, whose host is the site's (ignoring case and one trailing
    /// dot; an internationalised name matches its punycode form) on the same
    /// port. The raw text must be valid UTF-8 (no lone surrogate here)
    /// without spaces, control or format characters and backslashes, and
    /// its authority without '%' (parsers disagree about those). Anything
    /// else is refused.
    /// </summary>
    public static bool IsIssueUrl(string raw, string siteUrl)
    {
        ArgumentNullException.ThrowIfNull(raw);
        ArgumentNullException.ThrowIfNull(siteUrl);
        if (UrlSyntax.Parse(siteUrl.Trim()) is not { } site || site.Scheme is not ("https" or "http") || site.Rest.Host.Length == 0)
        {
            return false;
        }
        for (var i = 0; i < raw.Length;)
        {
            if (Rune.DecodeFromUtf16(raw.AsSpan(i), out var r, out var used) != OperationStatus.Done)
            {
                return false;
            }
            i += used;
            var v = r.Value;
            if (v <= ' ' || v == 0x7F || v == '\\' || Assistant.IsSpace(v) || Assistant.IsControl(v) || IsFormat(v))
            {
                return false;
            }
        }
        if (UrlSyntax.Parse(raw) is not { } u || !u.Rest.Opaque.IsEmpty || u.Rest.HasUserinfo || u.Rest.Host.Length == 0)
        {
            return false;
        }
        switch (u.Scheme)
        {
            case "https":
                break;
            case "http" when site.Scheme == "http":
                break;
            default:
                return false;
        }
        var bytes = Encoding.UTF8.GetBytes(raw);
        var prefix = Encoding.ASCII.GetBytes(u.Scheme + "://");
        if (bytes.Length < prefix.Length)
        {
            return false;
        }
        for (var i = 0; i < prefix.Length; i++)
        {
            if (LowerAscii(bytes[i]) != prefix[i])
            {
                return false;
            }
        }
        var authority = bytes.AsSpan(prefix.Length);
        var end = authority.IndexOfAny("/?#"u8);
        if (end >= 0)
        {
            authority = authority[..end];
        }
        if (authority.IndexOfAny("%@"u8) >= 0)
        {
            return false;
        }
        var (host, port) = UrlSyntax.SplitHostPort(u.Rest.Host);
        var (siteHost, sitePort) = UrlSyntax.SplitHostPort(site.Rest.Host);
        if (HostKey(host) is not { } key || HostKey(siteHost) is not { } want || !string.Equals(key, want, StringComparison.Ordinal))
        {
            return false;
        }
        return PortOf(u.Scheme, port) is { } n && PortOf(site.Scheme, sitePort) is { } siteN && n == siteN;
    }

    // An ASCII letter in lower case, any other byte as it is (strings.EqualFold
    // of the scheme prefix, which url.Parse only takes from ASCII letters).
    private static byte LowerAscii(byte b) => b is >= (byte)'A' and <= (byte)'Z' ? (byte)(b + 32) : b;

    /// <summary>jira.portOf: the URL's port, or its scheme's default; null for a bad one.</summary>
    internal static int? PortOf(string scheme, string port)
    {
        if (port.Length == 0)
        {
            return scheme == "http" ? 80 : 443;
        }
        return int.TryParse(port, NumberStyles.None, CultureInfo.InvariantCulture, out var n) && n is > 0 and <= 65535 ? n : null;
    }

    /// <summary>
    /// jira.hostKey: the form two host names are compared in: lower case, one
    /// trailing dot dropped, every non-ASCII label as its punycode "xn--"
    /// form. An IPv6 literal (SplitHostPort drops its brackets) is compared
    /// lower case. Null for an empty name or an empty label.
    /// </summary>
    internal static string? HostKey(string host)
    {
        if (host.Contains(':', StringComparison.Ordinal))
        {
            return host.Length == 0 ? null : CodePoints.ToLower(host);
        }
        if (host.EndsWith('.'))
        {
            host = host[..^1];
        }
        if (host.Length == 0)
        {
            return null;
        }
        var labels = CodePoints.ToLower(host).Split('.');
        for (var i = 0; i < labels.Length; i++)
        {
            var l = labels[i];
            if (l.Length == 0)
            {
                return null;
            }
            if (l.All(char.IsAscii))
            {
                continue;
            }
            if (Punycode(l) is not { } enc)
            {
                return null;
            }
            labels[i] = "xn--" + enc;
        }
        return string.Join('.', labels);
    }

    // Punycode parameters (RFC 3492 §5).
    private const int PcBase = 36;
    private const int PcTMin = 1;
    private const int PcTMax = 26;
    private const int PcSkew = 38;
    private const int PcDamp = 700;
    private const int PcInitialBias = 72;
    private const int PcInitialN = 128;

    // Far above any label; guards the arithmetic.
    private const int PcMaxDelta = 1 << 30;

    /// <summary>
    /// jira.punycode: a label encoded by RFC 3492 §6.3 without the "xn--"
    /// prefix; the ASCII characters are kept as they are. Null on overflow
    /// or an invalid character (U+FFFD, which a lone surrogate reads as).
    /// Public for the tests, as Go's and Swift's see it.
    /// </summary>
    public static string? Punycode(string label)
    {
        var runes = label.EnumerateRunes().Select(r => r.Value).ToArray();
        var output = new StringBuilder();
        foreach (var r in runes)
        {
            if (r == 0xFFFD)
            {
                return null;
            }
            if (r < PcInitialN)
            {
                output.Append((char)r);
            }
        }
        var basic = output.Length;
        var handled = basic;
        if (basic > 0)
        {
            output.Append('-');
        }
        var n = PcInitialN;
        var delta = 0;
        var bias = PcInitialBias;
        while (handled < runes.Length)
        {
            var m = 0x10FFFF + 1;
            foreach (var r in runes)
            {
                if (r >= n && r < m)
                {
                    m = r;
                }
            }
            if (m - n > (PcMaxDelta - delta) / (handled + 1))
            {
                return null;
            }
            delta += (m - n) * (handled + 1);
            n = m;
            foreach (var r in runes)
            {
                if (r < n)
                {
                    delta++;
                    if (delta > PcMaxDelta)
                    {
                        return null;
                    }
                }
                if (r != n)
                {
                    continue;
                }
                var q = delta;
                for (var k = PcBase; ; k += PcBase)
                {
                    var t = Math.Clamp(k - bias, PcTMin, PcTMax);
                    if (q < t)
                    {
                        break;
                    }
                    output.Append(PcDigit(t + ((q - t) % (PcBase - t))));
                    q = (q - t) / (PcBase - t);
                }
                output.Append(PcDigit(q));
                bias = PcAdapt(delta, handled + 1, handled == basic);
                delta = 0;
                handled++;
            }
            delta++;
            n++;
        }
        return output.ToString();
    }

    private static char PcDigit(int d) => d < 26 ? (char)('a' + d) : (char)('0' + d - 26);

    private static int PcAdapt(int delta, int points, bool first)
    {
        delta = first ? delta / PcDamp : delta / 2;
        delta += delta / points;
        var k = 0;
        while (delta > (PcBase - PcTMin) * PcTMax / 2)
        {
            delta /= PcBase - PcTMin;
            k += PcBase;
        }
        return k + ((PcBase - PcTMin + 1) * delta / (delta + PcSkew));
    }
}
