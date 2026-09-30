// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraTests.swift, the counterpart of
// ui/internal/jira/jira_test.go (TestStyleOf, TestClean,
// TestSiteHostAndAccountLabel, TestKindHelpers, TestVirtualFolders,
// TestIssueCard, TestIssueCardItem, TestEventLines, TestRowIssue,
// TestThreadRowIssue, TestAuthBannerText, TestIsIssueURL, TestPunycode). Go
// passes a translator; the process-wide catalogue is English here, so every
// msgid is its own translation, and the Czech halves (a context entry must
// win over the plain msgid) are in JiraTranslationTests. Go's invalid UTF-8
// is a lone surrogate in a C# string.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Xunit;

namespace Malachi.Core.Tests.IssueTrackers;

public sealed class JiraTests
{
    // Characters the cleaning must drop, as escapes: the source stays free of
    // invisible characters.
    internal const string Rlo = "\u202E"; // RIGHT-TO-LEFT OVERRIDE
    internal const string Zwsp = "\u200B"; // ZERO WIDTH SPACE
    internal const string Shy = "\u00AD"; // SOFT HYPHEN
    internal const string Lsep = "\u2028"; // LINE SEPARATOR
    internal const string Bom = "\uFEFF"; // ZERO WIDTH NO-BREAK SPACE

    // Go's invalid UTF-8: lone surrogates.
    internal const string Invalid = "\uDC00\uD800";

    // jira.maxText: the cap of a cleaned display string, in bytes.
    private const int MaxText = 512;

    private static readonly JiraCardRow[] NoRows = [];

    [Theory]
    [InlineData(IssueStatusCategory.Todo, JiraStatusStyle.Todo)]
    [InlineData(IssueStatusCategory.InProgress, JiraStatusStyle.InProgress)]
    [InlineData(IssueStatusCategory.Done, JiraStatusStyle.Done)]
    [InlineData("", JiraStatusStyle.Plain)]
    [InlineData("blocked", JiraStatusStyle.Plain)]
    [InlineData("Done", JiraStatusStyle.Plain)]
    public void StyleOf(string category, JiraStatusStyle want)
    {
        Assert.Equal(want, Jira.StyleOf(new IssueStatusCategory(category)));
        Assert.Equal(JiraStatusStyle.Plain, Jira.StyleOf(null));
    }

    public static TheoryData<string, string, string> CleanCases => new()
    {
        { "plain", "VPN drops every 10 minutes", "VPN drops every 10 minutes" },
        { "trim and collapse", "  a \t\n  b  ", "a b" },
        { "line breaks become spaces", "first\r\nsecond" + Lsep + "third", "first second third" },
        { "bidi override dropped", "invoice" + Rlo + "fdp.exe", "invoicefdp.exe" },
        { "zero width and soft hyphen dropped", "ad" + Zwsp + "min" + Shy + "istrator" + Bom, "administrator" },
        { "control characters dropped", "a\u0000b\u0007c\u001bd\u007fe", "abcde" },
        { "invalid UTF-8 dropped", "ok" + Invalid + "!", "ok!" },
        { "only invisible", Zwsp + Rlo + " \n", "" },
        { "czech kept", "Jana Dvořáková", "Jana Dvořáková" },
        { "empty", "", "" },
        // Windows: a U+FFFD the site sent is a character, as in Go.
        { "replacement character kept", "a\uFFFDb", "a\uFFFDb" },
    };

    [Theory]
    [MemberData(nameof(CleanCases))]
    public void Clean(string name, string input, string want) => Assert.True(want == Jira.Clean(input), $"{name}: Clean = {Jira.Clean(input)}");

    [Fact]
    public void CleanCapsOnACharacterBoundary()
    {
        var longText = Jira.Clean(new string('č', 600));
        var bytes = Encoding.UTF8.GetByteCount(longText);
        Assert.True(bytes <= MaxText && bytes >= MaxText - 1, $"Clean of 1200 bytes = {bytes} bytes");
        Assert.Equal(new string('č', 256), longText);
        var spaced = Jira.Clean(new string('a', MaxText - 1) + " bcd");
        Assert.Equal(new string('a', MaxText - 1), spaced); // a cut after a space drops the space
        Assert.Equal("", Jira.Clean(null));
    }

    internal static AccountConfig JiraConfig(string name, string site) => new()
    {
        Name = name,
        Email = "jana@acme.example",
        Kind = AccountKind.Jira,
        Jira = new JiraConfig { SiteUrl = site, Deployment = JiraDeployment.Cloud },
    };

    public static TheoryData<string, AccountConfig, string, string> SiteHostCases => new()
    {
        { "named", JiraConfig("Acme Jira", "https://acme.atlassian.net"), "acme.atlassian.net", "Acme Jira" },
        { "unnamed", JiraConfig(" ", "https://ACME.atlassian.net"), "acme.atlassian.net", "acme.atlassian.net" },
        { "port and path", JiraConfig("", "https://jira.acme.example:8443/jira"), "jira.acme.example", "jira.acme.example" },
        { "not a URL", JiraConfig("", "https://[bad"), "", "jana@acme.example" },
        { "no jira block", new AccountConfig { Name = "", Kind = AccountKind.Jira, Email = "jana@acme.example" }, "", "jana@acme.example" },
        {
            "mail account",
            new AccountConfig { Name = "", Email = " jana@acme.example ", Jira = new JiraConfig { SiteUrl = "https://acme.atlassian.net", Deployment = "" } },
            "",
            "jana@acme.example"
        },
        { "mail account with a name", new AccountConfig { Name = "Work", Email = "jana@acme.example" }, "", "Work" },
    };

    [Theory]
    [MemberData(nameof(SiteHostCases))]
    public void SiteHostAndAccountLabel(string name, AccountConfig cfg, string host, string label)
    {
        Assert.True(host == Jira.SiteHost(cfg), $"{name}: SiteHost = {Jira.SiteHost(cfg)}");
        Assert.True(label == Jira.AccountLabel(cfg), $"{name}: AccountLabel = {Jira.AccountLabel(cfg)}");
    }

    [Fact]
    public void KindHelpers()
    {
        Assert.True(Jira.IsJira(JiraConfig("", "")));
        Assert.False(Jira.IsJira(new AccountConfig { Name = "", Email = "" }));
        Assert.False(Jira.IsJira(new AccountConfig { Name = "", Email = "", Kind = AccountKind.Graph }));
        Assert.True(Jira.AlwaysThreaded(JiraConfig("", "")));
        Assert.False(Jira.AlwaysThreaded(new AccountConfig { Name = "", Email = "" }));
        Assert.Equal("Jira Cloud", Jira.DeploymentName(JiraDeployment.Cloud));
        Assert.Equal("Jira Data Center", Jira.DeploymentName(JiraDeployment.Datacenter));
        Assert.Equal("Jira", Jira.DeploymentName(""));
        Assert.Equal("Jira", Jira.DeploymentName("server"));
        Assert.Equal("Jira", Jira.DeploymentName(default));
        Assert.Equal("JIRA", Jira.KindBadge);
    }

    [Theory]
    [InlineData(VirtualFolder.AssignedToMe, "Assigned to Me", 0, "folder-saved-search-symbolic")]
    [InlineData(VirtualFolder.Watching, "Watching", 1, "folder-saved-search-symbolic")]
    [InlineData(VirtualFolder.Open, "Open", 2, "folder-saved-search-symbolic")]
    [InlineData("recent", "", 100, "folder-saved-search-symbolic")]
    [InlineData("", "", 100, "")]
    public void VirtualFolders(string v, string title, int rank, string icon)
    {
        Assert.Equal(title, Jira.VirtualFolderTitle(new VirtualFolder(v)));
        Assert.Equal(rank, Jira.VirtualRank(new VirtualFolder(v)));
        Assert.Equal(icon, Jira.VirtualIcon(new VirtualFolder(v)));
        Assert.Equal("", Jira.VirtualFolderTitle(null));
        Assert.Equal(100, Jira.VirtualRank(null));
        Assert.Equal("", Jira.VirtualIcon(null));
    }

    internal static IssueInfo FullIssue() => new()
    {
        Key = "ITSD-42",
        Url = "https://acme.atlassian.net/browse/ITSD-42",
        Summary = "VPN drops every 10 minutes",
        Status = "In Progress",
        StatusCategory = IssueStatusCategory.InProgress,
        Type = "Incident",
        Priority = "High",
        Assignee = "Jana Dvořáková",
        Reporter = "Petr Novák",
        AssignedToMe = true,
        CommentVisibilities = [CommentVisibility.Public, CommentVisibility.Internal],
    };

    private static IssueInfo Issue(string key) => new() { Key = key, Url = "", Summary = "", Status = "" };

    // Swift's == of two cards (reflect.DeepEqual in Go): the rows apart, as a
    // record compares its list by reference.
    internal static void AssertCard(JiraCard want, JiraCard got)
    {
        Assert.Equal(want with { Rows = NoRows }, got with { Rows = NoRows });
        Assert.Equal(want.Rows, got.Rows);
    }

    [Fact]
    public void IssueCard()
    {
        AssertCard(
            new JiraCard
            {
                Key = "ITSD-42",
                Url = "https://acme.atlassian.net/browse/ITSD-42",
                OpenTooltip = "Open ITSD-42 in the Browser",
                Summary = "VPN drops every 10 minutes",
                Status = "In Progress",
                StatusLabel = "Status",
                StatusStyle = JiraStatusStyle.InProgress,
                Rows =
                [
                    new JiraCardRow { Label = "Assignee", Value = "Jana Dvořáková" },
                    new JiraCardRow { Label = "Priority", Value = "High" },
                    new JiraCardRow { Label = "Type", Value = "Incident" },
                    new JiraCardRow { Label = "Reporter", Value = "Petr Novák" },
                ],
            },
            Jira.IssueCard(FullIssue()));

        var empty = Jira.IssueCard(Issue("WEB-7") with { Summary = " " + Rlo + "Login\nbroken " });
        Assert.Equal("Login broken", empty.Summary);
        Assert.Equal("", empty.Status);
        Assert.Equal(JiraStatusStyle.Plain, empty.StatusStyle);
        Assert.Equal<JiraCardRow>(
            [
                new JiraCardRow { Label = "Assignee", Value = "Unassigned", Missing = true },
                new JiraCardRow { Label = "Priority", Value = "None", Missing = true },
                new JiraCardRow { Label = "Type", Value = "None", Missing = true },
                new JiraCardRow { Label = "Reporter", Value = "None", Missing = true },
            ],
            empty.Rows);
        Assert.True(Jira.IssueCard(Issue("WEB-7") with { Priority = Zwsp + " " }).Rows[1].Missing); // an invisible priority is missing
    }

    [Fact]
    public void IssueCardItem()
    {
        var issue = FullIssue();
        var internalComment = MessageIssue.Of(issue, IssueItemKind.Comment) with { Visibility = CommentVisibility.Internal, Via = "Issue Sync", Edited = true };
        var c = Jira.IssueCard(issue, internalComment);
        Assert.True(c.Internal);
        Assert.Equal("Internal", c.InternalLabel);
        Assert.Equal("via Issue Sync", c.Via);
        Assert.Equal("Edited", c.Edited);
        var publicComment = MessageIssue.Of(issue, IssueItemKind.Comment) with { Visibility = CommentVisibility.Public };
        var p = Jira.IssueCard(issue, publicComment);
        Assert.False(p.Internal);
        Assert.Equal("", p.InternalLabel);
        Assert.Equal("", p.Via);
        Assert.Equal("", p.Edited);
        // Only a comment is internal, whatever the visibility member says.
        var description = MessageIssue.Of(issue, IssueItemKind.Description) with { Visibility = CommentVisibility.Internal };
        Assert.False(Jira.IssueCard(issue, description).Internal);
        var invisibleVia = MessageIssue.Of(Issue(""), IssueItemKind.Comment) with { Via = Rlo + Zwsp };
        Assert.Equal("", Jira.IssueCard(issue, invisibleVia).Via);
    }

    private static IssueChange Change(string field, string? from = null, string? to = null) => new() { Field = field, From = from, To = to };

    public static TheoryData<string, IssueChange[], string[]> EventCases => new()
    {
        { "none", [], [] },
        { "status", [Change(IssueField.Status, "To Do", "In Progress")], ["Status: To Do → In Progress"] },
        { "status without the old value", [Change(IssueField.Status, to: "Done")], ["Status: — → Done"] },
        { "status without the new value", [Change(IssueField.Status, "Done")], ["Status: Done → —"] },
        { "status both empty", [Change(IssueField.Status)], ["Status: — → —"] },
        { "assigned", [Change(IssueField.Assignee, to: "Jana Dvořáková")], ["Assignee: Unassigned → Jana Dvořáková"] },
        { "unassigned", [Change(IssueField.Assignee, "Jana Dvořáková")], ["Assignee: Jana Dvořáková → Unassigned"] },
        { "unknown field skipped", [Change("priority", "Low", "High"), Change(IssueField.Assignee, "A", "B")], ["Assignee: A → B"] },
        { "hostile values cleaned", [Change(IssueField.Status, "To\nDo", Rlo + Zwsp)], ["Status: To Do → —"] },
        { "two changes", [Change(IssueField.Status, "A", "B"), Change(IssueField.Assignee, to: "C")], ["Status: A → B", "Assignee: Unassigned → C"] },
    };

    [Theory]
    [MemberData(nameof(EventCases))]
    public void EventLines(string name, IssueChange[] changes, string[] want) =>
        Assert.True(want.SequenceEqual(Jira.EventLines(changes)), $"{name}: EventLines = [{string.Join(" | ", Jira.EventLines(changes))}]");

    [Fact]
    public void EventText()
    {
        Assert.Equal("Status: A → B; Assignee: Unassigned → C", Jira.EventText([Change(IssueField.Status, "A", "B"), Change(IssueField.Assignee, to: "C")]));
        Assert.Equal("", Jira.EventText([Change("labels")]));
        Assert.Empty(Jira.EventLines(null));
    }

    private static MessageSummary Summary(MessageIssue? issue, params Flag[] flags) => new()
    {
        Id = "m",
        AccountId = "a",
        FolderId = "f",
        Subject = "Hello",
        Date = DateTimeOffset.UnixEpoch,
        Snippet = "",
        HasAttachments = false,
        Size = 0,
        Flags = flags,
        Issue = issue,
    };

    [Fact]
    public void RowIssue()
    {
        var issue = FullIssue();
        Assert.Null(Jira.RowIssue(Summary(null))); // a mail message has no issue row
        var comment = Summary(MessageIssue.Of(issue, IssueItemKind.Comment) with { Visibility = CommentVisibility.Internal });
        Assert.Equal(
            new JiraIssueRow
            {
                Key = "ITSD-42",
                Summary = "VPN drops every 10 minutes",
                Status = "In Progress",
                StatusStyle = JiraStatusStyle.InProgress,
                Internal = true,
                InternalLabel = "Internal",
                Unread = true,
            },
            Jira.RowIssue(comment));
        Assert.False(Jira.RowIssue(comment with { Flags = [Flag.Flagged, Flag.Seen] })!.Unread); // a seen comment is read
        var eventRow = Jira.RowIssue(Summary(MessageIssue.Of(issue, IssueItemKind.Event) with { Changes = [Change(IssueField.Status, "To Do", "In Progress")] }))!;
        Assert.True(eventRow.Event);
        Assert.Equal("Status: To Do → In Progress", eventRow.EventText);
        Assert.False(eventRow.Unread); // an event without seen is read all the same
        Assert.False(eventRow.Internal);
    }

    [Fact]
    public void ThreadRowIssue()
    {
        var issue = FullIssue();
        var mail = new ThreadSummary
        {
            Id = "t",
            AccountId = "a",
            Subject = "Hello",
            MessageCount = 1,
            UnreadCount = 0,
            LatestDate = DateTimeOffset.UnixEpoch,
            Latest = Summary(null),
            Snippet = "",
            HasAttachments = false,
        };
        Assert.Null(Jira.ThreadRowIssue(mail)); // a mail conversation has no issue row
        var thread = mail with
        {
            Issue = issue,
            UnreadCount = 2,
            Latest = Summary(MessageIssue.Of(Issue("ITSD-42") with { Summary = "old summary" }, IssueItemKind.Event) with
            {
                Changes = [Change(IssueField.Assignee, to: "Jana Dvořáková")],
            }),
        };
        var r = Jira.ThreadRowIssue(thread)!;
        Assert.Equal("VPN drops every 10 minutes", r.Summary);
        Assert.True(r.Event);
        Assert.Equal("Assignee: Unassigned → Jana Dvořáková", r.EventText);
        Assert.True(r.Unread);
        thread = thread with { UnreadCount = 0, Latest = Summary(MessageIssue.Of(issue, IssueItemKind.Comment)) };
        var read = Jira.ThreadRowIssue(thread)!;
        Assert.False(read.Event || read.Unread);
        Assert.Equal("", read.EventText);
        // A thread without Issue falls back to its latest member.
        Assert.Equal("ITSD-42", Jira.ThreadRowIssue(thread with { Issue = null })?.Key);
    }

    [Theory]
    [InlineData(AccountKind.Jira, ErrorCode.AuthRequired, "No API token is stored for Acme")]
    [InlineData(AccountKind.Jira, ErrorCode.AuthFailed, "The Jira site rejected the token of Acme")]
    [InlineData(AccountKind.Jira, ErrorCode.KeyringError, "")]
    [InlineData(AccountKind.Imap, ErrorCode.AuthFailed, "")]
    [InlineData("", ErrorCode.AuthRequired, "")]
    public void AuthBannerText(string kind, int reason, string want) =>
        Assert.Equal(want, Jira.AuthBannerText(new AccountKind(kind), new ErrorCode(reason), "Acme"));

    private const string Cloud = "https://acme.atlassian.net";

    public static TheoryData<string, string, string, bool> IssueUrlCases => new()
    {
        { "issue", "https://acme.atlassian.net/browse/ITSD-42", Cloud, true },
        { "site root", "https://acme.atlassian.net", Cloud, true },
        { "query and fragment", "https://acme.atlassian.net/browse/ITSD-42?focusedCommentId=7#comment-7", Cloud, true },
        { "host case", "https://ACME.Atlassian.NET/browse/ITSD-42", Cloud, true },
        { "scheme case", "HTTPS://acme.atlassian.net/browse/ITSD-42", Cloud, true },
        { "site with a trailing slash", "https://acme.atlassian.net/browse/X-1", Cloud + "/", true },
        { "site with upper case", "https://acme.atlassian.net/browse/X-1", "https://ACME.atlassian.net", true },
        { "http on an https site", "http://acme.atlassian.net/browse/ITSD-42", Cloud, false },
        { "foreign host", "https://evil.example/browse/ITSD-42", Cloud, false },
        { "site as a subdomain", "https://acme.atlassian.net.evil.example/browse/ITSD-42", Cloud, false },
        { "subdomain of the site", "https://www.acme.atlassian.net/browse/ITSD-42", Cloud, false },
        { "parent domain", "https://atlassian.net/browse/ITSD-42", Cloud, false },
        { "similar host", "https://acme-atlassian.net/browse/ITSD-42", Cloud, false },
        { "javascript", "javascript:alert(1)", Cloud, false },
        { "javascript with the host", "javascript://acme.atlassian.net/%0aalert(1)", Cloud, false },
        { "data", "data:text/html,<script>alert(1)</script>", Cloud, false },
        { "file", "file:///etc/passwd", Cloud, false },
        { "mailto", "mailto:jana@acme.atlassian.net", Cloud, false },
        { "scheme-relative", "//acme.atlassian.net/browse/ITSD-42", Cloud, false },
        { "relative", "/browse/ITSD-42", Cloud, false },
        { "opaque", "https:acme.atlassian.net/browse/ITSD-42", Cloud, false },
        { "userinfo", "https://jana@acme.atlassian.net/browse/ITSD-42", Cloud, false },
        { "userinfo hiding the host", "https://acme.atlassian.net@evil.example/browse/ITSD-42", Cloud, false },
        { "userinfo with password", "https://jana:secret@acme.atlassian.net/", Cloud, false },
        { "backslash", "https://acme.atlassian.net\\@evil.example/", Cloud, false },
        { "backslash in the path", "https://acme.atlassian.net/browse\\ITSD-42", Cloud, false },
        { "percent-encoded host", "https://%61cme.atlassian.net/browse/ITSD-42", Cloud, false },
        { "percent in the path", "https://acme.atlassian.net/browse/ITSD-42%20x", Cloud, true },
        { "space", "https://acme.atlassian.net/browse/ITSD 42", Cloud, false },
        { "leading space", " https://acme.atlassian.net/browse/ITSD-42", Cloud, false },
        { "newline", "https://acme.atlassian.net/browse/ITSD-42\n", Cloud, false },
        { "invalid UTF-8", "https://acme.atlassian.net/browse/" + Invalid, Cloud, false },
        { "tab in the host", "https://acme.atlas\tsian.net/", Cloud, false },
        { "bidi override", "https://acme.atlassian.net/browse/" + Rlo + "24-DSTI", Cloud, false },
        { "trailing dot", "https://acme.atlassian.net./browse/ITSD-42", Cloud, true },
        { "trailing dot on the site", "https://acme.atlassian.net/browse/ITSD-42", "https://acme.atlassian.net.", true },
        { "two trailing dots", "https://acme.atlassian.net../browse/ITSD-42", Cloud, false },
        { "empty label", "https://acme..atlassian.net/browse/ITSD-42", Cloud, false },
        { "only a dot", "https://./browse/ITSD-42", Cloud, false },
        { "default port", "https://acme.atlassian.net:443/browse/ITSD-42", Cloud, true },
        { "other port", "https://acme.atlassian.net:8443/browse/ITSD-42", Cloud, false },
        { "empty port", "https://acme.atlassian.net:/browse/ITSD-42", Cloud, true },
        { "bad port", "https://acme.atlassian.net:99999/browse/ITSD-42", Cloud, false },
        { "zero-padded port", "https://acme.atlassian.net:0443/browse/ITSD-42", Cloud, true },
        { "site with a port", "https://jira.acme.example:8443/jira/browse/WEB-1", "https://jira.acme.example:8443/jira", true },
        { "site port missing", "https://jira.acme.example/jira/browse/WEB-1", "https://jira.acme.example:8443/jira", false },
        { "http site", "http://jira.local:8080/browse/WEB-1", "http://jira.local:8080", true },
        { "https on an http site, same port", "https://jira.local:8080/browse/WEB-1", "http://jira.local:8080", true },
        { "https on an http site, default ports", "https://jira.local/browse/WEB-1", "http://jira.local", false },
        { "http site, http default port", "http://jira.local:80/browse/WEB-1", "http://jira.local", true },
        { "IPv6 site", "http://[::1]:8080/browse/WEB-1", "http://[::1]:8080", true },
        { "IPv6 case", "https://[FE80::1]/browse/WEB-1", "https://[fe80::1]", true },
        { "IPv6 zone", "https://[fe80::1%25en0]/browse/WEB-1", "https://[fe80::1]", false },
        { "IDN to punycode site", "https://bücher.example/browse/WEB-1", "https://xn--bcher-kva.example", true },
        { "punycode to IDN site", "https://xn--bcher-kva.example/browse/WEB-1", "https://bücher.example", true },
        { "IDN upper case", "https://BÜCHER.example/browse/WEB-1", "https://bücher.example", true },
        { "IDN homograph", "https://bucher.example/browse/WEB-1", "https://bücher.example", false },
        { "cyrillic a", "https://\u0430cme.atlassian.net/browse/ITSD-42", Cloud, false },
        { "fullwidth dot", "https://acme\u3002atlassian.net/browse/ITSD-42", Cloud, false },
        { "empty", "", Cloud, false },
        { "empty site", "https://acme.atlassian.net/browse/ITSD-42", "", false },
        { "site not http", "https://acme.atlassian.net/browse/ITSD-42", "ftp://acme.atlassian.net", false },
        { "site without a scheme", "https://acme.atlassian.net/browse/ITSD-42", "acme.atlassian.net", false },
    };

    [Theory]
    [MemberData(nameof(IssueUrlCases))]
    public void IsIssueUrl(string name, string raw, string site, bool want) =>
        Assert.True(want == Jira.IsIssueUrl(raw, site), $"{name}: IsIssueUrl({raw}, {site}) = {!want}");

    // RFC 3492 §7.1 samples (B and L) and two common names.
    [Theory]
    [InlineData("bücher", "bcher-kva")]
    [InlineData("münchen", "mnchen-3ya")]
    [InlineData("他们为什么不说中文", "ihqwcrb4cv8a8dqg056pqjye")]
    [InlineData("Pročprostěnemluvíčesky", "Proprostnemluvesky-uyb24dma41a")]
    [InlineData("ü", "tda")]
    [InlineData("abc", "abc-")]
    public void Punycode(string label, string want) => Assert.Equal(want, Jira.Punycode(label));

    [Fact]
    public void PunycodeRefusesInvalidUtf8()
    {
        Assert.Null(Jira.Punycode("a" + Invalid + "b"));
        Assert.Null(Jira.Punycode("a\uFFFDb")); // Go's []rune reads both as RuneError
    }

}
