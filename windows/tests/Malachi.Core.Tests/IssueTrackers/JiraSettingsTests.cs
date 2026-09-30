// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraSettingsTests.swift, the
// counterpart of ui/internal/jira/settings_test.go (TestSettingsTexts,
// TestSettingsSite, TestSettingsSpaceRows, TestSetSpaceSelected,
// TestFolders, TestNotificationModes, TestDefaultSenders, TestStatusGroups,
// TestSetStatusSelected, TestStatusesProblem, TestNormaliseList,
// TestCheckEntry, TestSuggestions, TestNewSettingsForm,
// TestSettingsFormApply, TestSettingsProblem, TestChanged; TestPatternError
// is JiraPatternTests), with the English catalogue; the Czech halves are in
// JiraTranslationTests. Go's reflect.DeepEqual of structs holding lists is
// a comparison of their JSON (configurations) or member by member (forms).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Tests.Api;
using Xunit;

namespace Malachi.Core.Tests.IssueTrackers;

public sealed class JiraSettingsTests
{
    // cloudAccount: a stored Jira Cloud account with every setting at its default.
    internal static AccountConfig CloudAccount() => new()
    {
        Name = "Acme",
        Email = "jana@acme.example",
        Kind = AccountKind.Jira,
        Jira = new JiraConfig
        {
            SiteUrl = "https://acme.atlassian.net",
            Deployment = JiraDeployment.Cloud,
            CloudId = "0b9e3d2c-1a2b-4c3d-8e9f-001122334455",
            Login = "jana@acme.example",
            Spaces = [new SpaceRef { Id = "10001", Key = "ITSD", Name = "IT Service Desk" }, new SpaceRef { Id = "10002", Key = "WEB", Name = "Website" }],
        },
    };

    // dataCenterAccount: a stored Data Center account.
    internal static AccountConfig DataCenterAccount() => new()
    {
        Name = "Acme Jira",
        Email = "jana@acme.example",
        Kind = AccountKind.Jira,
        Jira = new JiraConfig
        {
            SiteUrl = "https://jira.acme.example/jira",
            Deployment = JiraDeployment.Datacenter,
            Spaces = [new SpaceRef { Id = "20001", Key = "OPS", Name = "Operations" }],
        },
    };

    internal static readonly Space[] SiteSpaces =
    [
        new() { Id = "10001", Key = "ITSD", Name = "IT Service Desk", ServiceDesk = true, Issues = -1 },
        new() { Id = "10003", Key = "MOB", Name = "Mobile", Issues = -1 },
        new() { Id = "10002", Key = "WEB", Name = "Website", Issues = -1 },
    ];

    internal static readonly IssueStatus[] SiteStatuses =
    [
        new() { Id = "1", Name = "Open", Category = IssueStatusCategory.Todo },
        new() { Id = "3", Name = "In Progress", Category = IssueStatusCategory.InProgress },
        new() { Id = "5", Name = "Resolved", Category = IssueStatusCategory.Done },
        new() { Id = "6", Name = "Done", Category = IssueStatusCategory.Done },
        new() { Id = "10010", Name = "Done", Category = IssueStatusCategory.Done },
        new() { Id = "10020", Name = "Waiting for Customer", Category = "waiting" },
        new() { Id = "10021", Name = "Done", Category = IssueStatusCategory.InProgress },
    ];

    private static AccountConfig WithJira(AccountConfig cfg, Func<JiraConfig, JiraConfig> edit) => cfg with { Jira = edit(cfg.Jira!) };

    [Fact]
    public void SettingsTexts()
    {
        var s = Jira.SettingsTexts();
        foreach (var p in typeof(JiraSettingsStrings).GetProperties().Where(p => p.PropertyType == typeof(string)))
        {
            Assert.False(string.IsNullOrEmpty((string?)p.GetValue(s)), $"SettingsStrings.{p.Name} is empty");
        }
        Assert.Equal("Jira Account", s.Title);
        Assert.Equal("Spaces", s.SpacesTitle);
        Assert.Equal("Show Status and Assignee Changes", s.ShowEvents);
        Assert.Equal("Replace Token…", s.ReplaceToken);
        Assert.Equal("Folders", s.FoldersTitle);
        Assert.Equal("Saving the account", s.Saving);
    }

    [Fact]
    public void SettingsSite()
    {
        var cloud = CloudAccount();
        var want = new JiraSiteInfo { Address = "https://acme.atlassian.net", Deployment = "Jira Cloud", User = "jana@acme.example", TokenLabel = "API Token" };
        Assert.Equal(want, Jira.SettingsSite(cloud, null));
        Assert.Equal(
            want with { User = "Jana Dvořáková", UserDetail = "jana@acme.example" },
            Jira.SettingsSite(cloud, new SiteUser { Name = "Jana Dvořáková", Email = "jana@acme.example" }));

        var dc = DataCenterAccount();
        Assert.Equal(
            new JiraSiteInfo
            {
                Address = "https://jira.acme.example/jira",
                Deployment = "Jira Data Center",
                User = "jdvorakova",
                UserDetail = "jana@acme.example",
                TokenLabel = "Personal Access Token",
            },
            Jira.SettingsSite(dc, new SiteUser { Name = "jdvorakova" }));
        // A user without a name is the address; a name that is the address has no detail.
        var nameless = Jira.SettingsSite(dc, new SiteUser { Name = "" });
        Assert.Equal("jana@acme.example", nameless.User);
        Assert.Equal("", nameless.UserDetail);
        Assert.Equal("", Jira.SettingsSite(dc, new SiteUser { Name = "jana@acme.example", Email = "jana@acme.example" }).UserDetail);
        // Hostile display text is cleaned.
        var hostile = Jira.SettingsSite(dc, new SiteUser { Name = "Jana" + JiraTests.Rlo + "\nDvořáková\0", Email = "a@b.example\r\nX: y" });
        Assert.Equal("Jana Dvořáková", hostile.User);
        Assert.Equal("a@b.example X: y", hostile.UserDetail);
        // An account of another kind has no site.
        var mail = Jira.SettingsSite(new AccountConfig { Name = "", Email = "jana@acme.example" }, null);
        Assert.Equal("", mail.Address);
        Assert.Equal("Jira", mail.Deployment);
        Assert.Equal("jana@acme.example", mail.User);
    }

    [Fact]
    public void SettingsSpaceRows()
    {
        var stored = CloudAccount().Jira!.Spaces;
        Assert.Equal<JiraSpaceRow>(
            [
                new() { Id = "10001", Title = "ITSD – IT Service Desk", ServiceDesk = true },
                new() { Id = "10003", Title = "MOB – Mobile" },
                new() { Id = "10002", Title = "WEB – Website" },
            ],
            Jira.SettingsSpaceRows(stored, SiteSpaces));
        // A stored space the listing lacks stays, after the listed ones.
        var rows = Jira.SettingsSpaceRows([new SpaceRef { Id = "10009", Key = "OLD", Name = "Archive" }, .. stored], SiteSpaces);
        Assert.Equal(4, rows.Count);
        Assert.Equal(new JiraSpaceRow { Id = "10009", Title = "OLD – Archive" }, rows[3]);
        // Without a listing the stored spaces are the rows.
        Assert.Equal<JiraSpaceRow>(
            [new() { Id = "10001", Title = "ITSD – IT Service Desk" }, new() { Id = "10002", Title = "WEB – Website" }],
            Jira.SettingsSpaceRows(stored, null));
        Assert.Empty(Jira.SettingsSpaceRows(null, null));
        // A stored space listed twice is one row.
        Assert.Single(Jira.SettingsSpaceRows([new SpaceRef { Id = "7", Key = "A" }, new SpaceRef { Id = "7", Key = "A" }], null));
    }

    private static string Ids(IEnumerable<SpaceRef> refs) => string.Join(",", refs.Select(r => r.Id));

    private static string Ids(IEnumerable<StatusRef> refs) => string.Join(",", refs.Select(r => r.Id));

    [Fact]
    public void SetSpaceSelected()
    {
        SpaceRef[] stored = [new() { Id = "10009", Key = "OLD", Name = "Archive" }, .. CloudAccount().Jira!.Spaces];
        // Ticking one: the order of the rows, the listed ones first.
        Assert.Equal("10001,10003,10002,10009", Ids(Jira.SetSpaceSelected(stored, stored, SiteSpaces, "10003", true)));
        // The key and the name come from the listing.
        var got = Jira.SetSpaceSelected(stored, stored, [new Space { Id = "10001", Key = "HELP", Name = "Help Desk" }], "10002", false);
        Assert.Equal("10001,10009", Ids(got));
        Assert.Equal(new SpaceRef { Id = "10001", Key = "HELP", Name = "Help Desk" }, got[0]);
        // Unticking the last one leaves nothing; an unknown id changes nothing.
        SpaceRef[] one = [new() { Id = "10001", Key = "ITSD" }];
        Assert.Empty(Jira.SetSpaceSelected(one, one, null, "10001", false));
        Assert.Equal("10001", Ids(Jira.SetSpaceSelected(one, one, null, "nope", true)));
    }

    [Fact]
    public void Folders()
    {
        Assert.Equal<VirtualFolder>([VirtualFolder.AssignedToMe, VirtualFolder.Watching, VirtualFolder.Open], Jira.VirtualFolders);
        Assert.All(Jira.VirtualFolders, v => Assert.True(Jira.FolderShown(null, v)));
        var disabled = Jira.SetFolderShown(null, VirtualFolder.Open, false);
        disabled = Jira.SetFolderShown(disabled, VirtualFolder.AssignedToMe, false);
        Assert.Equal<VirtualFolder>([VirtualFolder.AssignedToMe, VirtualFolder.Open], disabled);
        Assert.False(Jira.FolderShown(disabled, VirtualFolder.Open));
        Assert.True(Jira.FolderShown(disabled, VirtualFolder.Watching));
        // Hiding twice, and an unknown or repeated stored view, leave one entry per known view.
        disabled = Jira.SetFolderShown(["open", "open", "archive"], VirtualFolder.Open, false);
        Assert.Equal<VirtualFolder>([VirtualFolder.Open], disabled);
        Assert.Empty(Jira.SetFolderShown(disabled, VirtualFolder.Open, true)); // Go's nil
    }

    [Fact]
    public void NotificationModes()
    {
        var labels = Jira.NotificationModeLabels();
        Assert.Equal(["Check the Issue at Once", "Check the Issue and Hide the E-mail", "Do Nothing"], labels);
        Assert.Equal(Jira.NotificationModes.Count, labels.Count);
        foreach (var (mode, index) in new[] { ("", 0), ("sync", 0), ("hide", 1), ("ignore", 2), ("later", 0) })
        {
            Assert.Equal(index, Jira.IndexOfNotificationMode(new NotificationMailMode(mode)));
        }
        Assert.Equal(0, Jira.IndexOfNotificationMode(null));
        Assert.Equal("Hidden e-mails stay in your mailbox and come back when you turn this off", Jira.NotificationHint(NotificationMailMode.Hide));
        foreach (var mode in new NotificationMailMode[] { "", NotificationMailMode.Sync, NotificationMailMode.Ignore })
        {
            Assert.Equal("", Jira.NotificationHint(mode));
        }
        Assert.True(Jira.SendersEditable(""));
        Assert.True(Jira.SendersEditable(NotificationMailMode.Hide));
        Assert.False(Jira.SendersEditable(NotificationMailMode.Ignore));
    }

    [Fact]
    public void DefaultSenders()
    {
        Assert.Equal("@acme.atlassian.net", Jira.DefaultSenders(CloudAccount()));
        Assert.Equal("", Jira.DefaultSenders(DataCenterAccount()));
        Assert.Equal("@acme.atlassian.net", Jira.DefaultSenders(WithJira(CloudAccount(), j => j with { SiteUrl = "https://ACME.Atlassian.NET:8443/" })));
        Assert.Equal("", Jira.DefaultSenders(WithJira(CloudAccount(), j => j with { SiteUrl = "::" })));
        Assert.Equal("", Jira.DefaultSenders(new AccountConfig { Name = "", Email = "a@b.example" }));
    }

    private static string StyleClass(JiraStatusStyle s) => s switch
    {
        JiraStatusStyle.Todo => "status-todo",
        JiraStatusStyle.InProgress => "status-in-progress",
        JiraStatusStyle.Done => "status-done",
        _ => "",
    };

    // groupsText: the picker on one line: "Title[style]: Name*(ids) Name(ids); …",
    // a star for a selected choice.
    internal static string GroupsText(IReadOnlyList<JiraStatusGroup> groups) =>
        string.Join("; ", groups.Select(g => $"{g.Title}[{StyleClass(g.Style)}]: "
            + string.Join(" ", g.Choices.Select(c => $"{c.Name}{(c.Selected ? "*" : "")}({string.Join(",", c.Ids)})"))));

    [Fact]
    public void StatusGroups()
    {
        // The default: the statuses of the category done; a name is one
        // choice per category, with every id of it.
        Assert.Equal(
            "To Do[status-todo]: Open(1); "
                + "In Progress[status-in-progress]: In Progress(3) Done(10021); "
                + "Done[status-done]: Resolved*(5) Done*(6,10010); "
                + "Other[]: Waiting for Customer(10020)",
            GroupsText(Jira.StatusGroups(SiteStatuses, null)));
        // Stored statuses: a choice is selected when one of its ids is stored;
        // a stored status the site lacks is listed among the others.
        StatusRef[] closed = [new() { Id = "10010", Name = "Done" }, new() { Id = "10020" }, new() { Id = "777", Name = "Cancelled" }, new() { Id = "778" }];
        Assert.Equal(
            "To Do[status-todo]: Open(1); "
                + "In Progress[status-in-progress]: In Progress(3) Done(10021); "
                + "Done[status-done]: Resolved(5) Done*(6,10010); "
                + "Other[]: Waiting for Customer*(10020) Cancelled*(777) 778*(778)",
            GroupsText(Jira.StatusGroups(SiteStatuses, closed)));
        // Without a listing only the stored ones are known.
        Assert.Equal("Other[]: Done*(10010)", GroupsText(Jira.StatusGroups(null, closed[..1])));
        Assert.Empty(Jira.StatusGroups(null, null));
        // Hostile names are cleaned; a status without an id or listed twice
        // is left out; a nameless one shows its id.
        IssueStatus[] hostile =
        [
            new() { Id = "1", Name = "Do" + JiraTests.Zwsp + "ne\n" + JiraTests.Rlo + "now", Category = IssueStatusCategory.Done },
            new() { Id = "", Name = "Nameless", Category = IssueStatusCategory.Done },
            new() { Id = "1", Name = "Again", Category = IssueStatusCategory.Todo },
            new() { Id = "2", Name = " \t ", Category = IssueStatusCategory.Todo },
        ];
        Assert.Equal("To Do[status-todo]: 2(2); Done[status-done]: Done now*(1)", GroupsText(Jira.StatusGroups(hostile, null)));
    }

    private static JiraStatusChoice Choice(IReadOnlyList<StatusRef>? closed, string name) =>
        Jira.StatusGroups(SiteStatuses, closed).SelectMany(g => g.Choices).First(c => c.Name == name);

    [Fact]
    public void SetStatusSelected()
    {
        var def = Jira.DefaultClosedStatuses(SiteStatuses);
        Assert.Equal("5,6,10010", Ids(def));
        Assert.Equal("Done", def[1].Name);
        // From the default: ticking adds to the statuses of the category done.
        var closed = Jira.SetStatusSelected(SiteStatuses, null, Choice(null, "Waiting for Customer"), true);
        Assert.Equal("5,6,10010,10020", Ids(closed));
        Assert.Equal("Waiting for Customer", closed[3].Name);
        // Unticking a name takes every status of it.
        closed = Jira.SetStatusSelected(SiteStatuses, closed, Choice(closed, "Resolved"), false);
        Assert.Equal("6,10010,10020", Ids(closed));
        // Back at the statuses of the category done: the default, empty.
        closed = Jira.SetStatusSelected(SiteStatuses, closed, Choice(closed, "Resolved"), true);
        closed = Jira.SetStatusSelected(SiteStatuses, closed, Choice(closed, "Waiting for Customer"), false);
        Assert.Empty(closed);
        // One stored id of a name: ticking stores the others too.
        closed = Jira.SetStatusSelected(SiteStatuses, [new StatusRef { Id = "10010" }, new StatusRef { Id = "1" }], new JiraStatusChoice { Name = "Done", Ids = ["6", "10010"] }, true);
        Assert.Equal("10010,1,6", Ids(closed));
        // Nothing left is the default.
        Assert.Empty(Jira.SetStatusSelected(SiteStatuses, [new StatusRef { Id = "1" }], new JiraStatusChoice { Name = "Open", Ids = ["1"] }, false));
        // Without a listing the stored ones can still be unticked.
        StatusRef[] stored = [new() { Id = "777", Name = "Cancelled" }, new() { Id = "5", Name = "Resolved" }];
        Assert.Equal("5", Ids(Jira.SetStatusSelected(null, stored, new JiraStatusChoice { Name = "Cancelled", Ids = ["777"] }, false)));
        // A choice the site does not list keeps its name.
        closed = Jira.SetStatusSelected(null, stored[1..], new JiraStatusChoice { Name = "Cancelled", Ids = ["777"] }, true);
        Assert.Equal("5,777", Ids(closed));
        Assert.Equal("Cancelled", closed[1].Name);
    }

    [Fact]
    public void StatusesProblem()
    {
        var many = Enumerable.Range(0, API.Limits.MaxJiraStatuses).Select(i => new StatusRef { Id = i.ToString(CultureInfo.InvariantCulture) }).ToList();
        Assert.Equal("", Jira.StatusesProblem(many));
        many.Add(new StatusRef { Id = "x" });
        Assert.Equal("Select at most 64 statuses", Jira.StatusesProblem(many));
        Assert.Equal("", Jira.StatusesProblem(null));
    }

    public static TheoryData<JiraListKind, string[], string[]> NormaliseCases => new()
    {
        { JiraListKind.BotNames, [], [] },
        { JiraListKind.BotNames, ["", "  "], [] },
        {
            JiraListKind.BotNames,
            [" Issue Sync – Synchronization for Jira ", "issue sync - synchronization  for jira", "Deploy Bot"],
            ["Issue Sync – Synchronization for Jira", "Deploy Bot"]
        },
        { JiraListKind.MetadataFilters, ["^a$", " ^a$ ", "^A$", ""], ["^a$", "^A$"] },
        { JiraListKind.AuthorPrefixes, ["ACME", "acme", " Acme s.r.o. "], ["ACME", "Acme s.r.o."] },
        { JiraListKind.Senders, [" Jira@Acme.Example ", "jira@acme.example", "@ACME.example"], ["jira@acme.example", "@acme.example"] },
    };

    [Theory]
    [MemberData(nameof(NormaliseCases))]
    public void NormaliseList(JiraListKind kind, string[] input, string[] want) => Assert.Equal(want, Jira.NormaliseList(kind, input));

    private const string TooLong = "This entry is too long";
    private const string Control = "This entry contains control characters";
    private const string Duplicate = "This entry is already in the list";
    private const string Full = "The list holds at most 32 entries";
    private const string Sender = "Enter an address, or a domain such as @example.org";
    private const string Short = "A bot name needs at least 3 characters";

    private static readonly string[] ThirtyTwo = [.. Enumerable.Range(0, API.Limits.MaxJiraListEntries).Select(i => $"entry {i}")];

    private static readonly string Exact = new('a', API.Limits.MaxJiraPatternBytes);

    public static TheoryData<JiraListKind, string, string[], string, string> EntryCases => new()
    {
        // Nothing typed is nothing to add, and no problem.
        { JiraListKind.BotNames, "", [], "", "" },
        { JiraListKind.MetadataFilters, " \t ", [], "", "" },

        { JiraListKind.BotNames, "  Issue Sync – Synchronization for Jira ", [], "Issue Sync – Synchronization for Jira", "" },
        { JiraListKind.BotNames, "Bot", [], "Bot", "" },
        { JiraListKind.BotNames, "Žů", [], "", Short },
        { JiraListKind.BotNames, "a" + JiraTests.Zwsp + JiraTests.Zwsp + "b", [], "", Short },
        { JiraListKind.BotNames, "issue sync - synchronization for jira", [Jira.SuggestedBotName], "", Duplicate },
        { JiraListKind.BotNames, "Deploy\tBot", [], "", Control },
        { JiraListKind.BotNames, "Deploy Bot\0", [], "", Control },
        { JiraListKind.BotNames, Exact, [], Exact, "" },
        { JiraListKind.BotNames, Exact + "a", [], "", TooLong },
        { JiraListKind.BotNames, new string('ž', (API.Limits.MaxJiraPatternBytes / 2) + 1), [], "", TooLong },
        { JiraListKind.BotNames, "One More", ThirtyTwo, "", Full },
        { JiraListKind.BotNames, "Entry 3", ThirtyTwo, "", Duplicate },

        { JiraListKind.MetadataFilters, " ^Remote comment create date:.*$ ", [], "^Remote comment create date:.*$", "" },
        { JiraListKind.MetadataFilters, "^a$", ["^A$"], "^a$", "" },
        { JiraListKind.MetadataFilters, "^a$", [" ^a$"], "", Duplicate },
        { JiraListKind.MetadataFilters, "(a", [], "", "This pattern is not valid: missing closing )" },
        { JiraListKind.MetadataFilters, "(?=a)", [], "", "This pattern is not valid: invalid or unsupported Perl syntax" },
        { JiraListKind.MetadataFilters, "a{1001}", [], "", "This pattern is not valid: invalid repeat count" },
        { JiraListKind.MetadataFilters, "a\nb", [], "", Control },
        { JiraListKind.MetadataFilters, "x", ThirtyTwo, "", Full },

        { JiraListKind.AuthorPrefixes, " ACME ", [], "ACME", "" },
        { JiraListKind.AuthorPrefixes, "a", [], "a", "" },
        { JiraListKind.AuthorPrefixes, "acme", ["ACME"], "", Duplicate },
        { JiraListKind.AuthorPrefixes, JiraTests.Zwsp, [], "", Control },

        { JiraListKind.Senders, " Jira@Acme.Example ", [], "jira@acme.example", "" },
        { JiraListKind.Senders, "@Acme.Example", [], "@acme.example", "" },
        { JiraListKind.Senders, "@localhost", [], "@localhost", "" },
        { JiraListKind.Senders, "JIRA@acme.example", ["jira@acme.example"], "", Duplicate },
        { JiraListKind.Senders, "acme.example", [], "", Sender },
        { JiraListKind.Senders, "@", [], "", Sender },
        { JiraListKind.Senders, "@acme..example", [], "", Sender },
        { JiraListKind.Senders, "@-acme.example", [], "", Sender },
        { JiraListKind.Senders, "@acme_.example", [], "", Sender },
        { JiraListKind.Senders, "@acme.example/path", [], "", Sender },
        { JiraListKind.Senders, "Jira <jira@acme.example>", [], "", Sender },
        { JiraListKind.Senders, "jira@acme.example, x@acme.example", [], "", Sender },
        { JiraListKind.Senders, "jira@", [], "", Sender },
        { JiraListKind.Senders, "@" + new string('a', 64) + ".example", [], "", Sender },

        // Windows: Go's invalid UTF-8, a lone surrogate, is refused as a control character.
        { JiraListKind.BotNames, "Deploy Bot" + JiraTests.Invalid, [], "", Control },
    };

    [Theory]
    [MemberData(nameof(EntryCases))]
    public void CheckEntry(JiraListKind kind, string input, string[] have, string entry, string problem) =>
        Assert.Equal((entry, problem), Jira.CheckEntry(kind, input, have));

    [Fact]
    public void Suggestions()
    {
        Assert.Equal<JiraSuggestion>(
            [new("Issue Sync – Synchronization for Jira", "Add Issue Sync – Synchronization for Jira")],
            Jira.Suggestions(JiraListKind.BotNames, null));
        Assert.Equal<JiraSuggestion>(
            [new("^Remote comment create date:.*$", "Add ^Remote comment create date:.*$")],
            Jira.Suggestions(JiraListKind.MetadataFilters, ["^x$"]));
        // Not once the list has it (a bot name in any spelling of its dash).
        Assert.Empty(Jira.Suggestions(JiraListKind.BotNames, ["Deploy Bot", " issue sync - Synchronization for JIRA"]));
        Assert.Empty(Jira.Suggestions(JiraListKind.MetadataFilters, ["^Remote comment create date:.*$"]));
        // Not for the other lists, and not for a full list.
        Assert.Empty(Jira.Suggestions(JiraListKind.AuthorPrefixes, null));
        Assert.Empty(Jira.Suggestions(JiraListKind.Senders, null));
        Assert.Empty(Jira.Suggestions(JiraListKind.BotNames, [.. Enumerable.Range(0, API.Limits.MaxJiraListEntries).Select(i => $"Bot {i}")]));
        // Both suggestions pass the page's own check.
        Assert.Equal((Jira.SuggestedBotName, ""), Jira.CheckEntry(JiraListKind.BotNames, Jira.SuggestedBotName, null));
        Assert.Equal((Jira.SuggestedMetadataFilter, ""), Jira.CheckEntry(JiraListKind.MetadataFilters, Jira.SuggestedMetadataFilter, null));
    }

    // Go's reflect.DeepEqual of two forms: member by member, the lists by content.
    internal static void AssertForm(JiraSettingsForm want, JiraSettingsForm got)
    {
        Assert.Equal(want.Name, got.Name);
        Assert.Equal(want.Spaces, got.Spaces);
        Assert.Equal(want.OfflineDays, got.OfflineDays);
        Assert.Equal(want.OnlyMine, got.OnlyMine);
        Assert.Equal(want.ShowEvents, got.ShowEvents);
        Assert.Equal(want.DisabledFolders, got.DisabledFolders);
        Assert.Equal(want.ClosedStatuses, got.ClosedStatuses);
        Assert.Equal(want.NotificationMail, got.NotificationMail);
        Assert.Equal(want.NotificationSenders, got.NotificationSenders);
        Assert.Equal(want.BotNames, got.BotNames);
        Assert.Equal(want.MetadataFilters, got.MetadataFilters);
        Assert.Equal(want.AuthorPrefixes, got.AuthorPrefixes);
    }

    [Fact]
    public void NewSettingsForm()
    {
        var spaces = CloudAccount().Jira!.Spaces;
        AssertForm(new JiraSettingsForm { Name = "Acme", Spaces = spaces }, Jira.NewSettingsForm(CloudAccount()));

        var cfg = WithJira(CloudAccount(), j => j with
        {
            OfflineDays = 45,
            OnlyMine = true,
            HideEvents = true,
            DisabledFolders = [VirtualFolder.Watching],
            ClosedStatuses = [new StatusRef { Id = "5", Name = "Resolved" }],
            NotificationMail = NotificationMailMode.Hide,
            NotificationSenders = ["jira@acme.example"],
            BotNames = [Jira.SuggestedBotName],
            MetadataFilters = [Jira.SuggestedMetadataFilter],
            AuthorPrefixes = ["ACME"],
        });
        var got = Jira.NewSettingsForm(cfg);
        AssertForm(
            new JiraSettingsForm
            {
                Name = "Acme",
                Spaces = spaces,
                OfflineDays = 45,
                OnlyMine = true,
                ShowEvents = false,
                DisabledFolders = [VirtualFolder.Watching],
                ClosedStatuses = [new StatusRef { Id = "5", Name = "Resolved" }],
                NotificationMail = NotificationMailMode.Hide,
                NotificationSenders = ["jira@acme.example"],
                BotNames = [Jira.SuggestedBotName],
                MetadataFilters = [Jira.SuggestedMetadataFilter],
                AuthorPrefixes = ["ACME"],
            },
            got);
        // The form is a copy: its lists are not the account's.
        Assert.NotSame(cfg.Jira!.Spaces, got.Spaces);
        Assert.NotSame(cfg.Jira.BotNames, got.BotNames);
        // A mode this client does not know is shown as the default; an
        // account of another kind has an empty form.
        Assert.Equal(new NotificationMailMode(NotificationMailMode.Sync), Jira.NewSettingsForm(WithJira(cfg, j => j with { NotificationMail = "later" })).NotificationMail);
        AssertForm(new JiraSettingsForm { Name = "Mail" }, Jira.NewSettingsForm(new AccountConfig { Name = "Mail", Email = "jana@acme.example" }));
    }

    [Fact]
    public void SettingsFormApply()
    {
        // Nothing edited: the account as it is stored.
        var stored = CloudAccount();
        var got = Jira.NewSettingsForm(stored).Apply(stored);
        ApiJson.AssertSameValue(stored, got);
        Assert.False(Jira.Changed(stored, got), "an unedited form counts as changed");
        Assert.NotSame(stored.Jira, got.Jira);

        var f = Jira.NewSettingsForm(stored) with
        {
            Name = "  Acme Jira  ",
            Spaces = [new SpaceRef { Id = "10003", Key = "MOB", Name = "Mobile" }, new SpaceRef { Id = "10003", Key = "MOB" }, new SpaceRef { Id = "", Key = "X" }, new SpaceRef { Id = "10001", Key = "ITSD" }],
            OfflineDays = 90,
            OnlyMine = true,
            ShowEvents = false,
            DisabledFolders = [VirtualFolder.Open, VirtualFolder.AssignedToMe, VirtualFolder.Open],
            ClosedStatuses = [new StatusRef { Id = " 5 ", Name = " Resolved " }, new StatusRef { Id = "5" }, new StatusRef { Id = "" }, new StatusRef { Id = "6", Name = "Done" }],
            NotificationMail = NotificationMailMode.Hide,
            NotificationSenders = [" Jira@Acme.Example", "jira@acme.example", ""],
            BotNames = [" Issue Sync – Synchronization for Jira ", "issue sync - synchronization for jira"],
            MetadataFilters = [Jira.SuggestedMetadataFilter, " " + Jira.SuggestedMetadataFilter],
            AuthorPrefixes = ["ACME", "  ", "Acme"],
        };
        got = f.Apply(stored);
        var want = WithJira(CloudAccount() with { Name = "Acme Jira" }, j => j with
        {
            Spaces = [new SpaceRef { Id = "10003", Key = "MOB", Name = "Mobile" }, new SpaceRef { Id = "10001", Key = "ITSD" }],
            OfflineDays = 90,
            OnlyMine = true,
            HideEvents = true,
            DisabledFolders = [VirtualFolder.AssignedToMe, VirtualFolder.Open],
            ClosedStatuses = [new StatusRef { Id = "5", Name = "Resolved" }, new StatusRef { Id = "6", Name = "Done" }],
            NotificationMail = NotificationMailMode.Hide,
            NotificationSenders = ["jira@acme.example"],
            BotNames = ["Issue Sync – Synchronization for Jira"],
            MetadataFilters = [Jira.SuggestedMetadataFilter],
            AuthorPrefixes = ["ACME"],
        });
        ApiJson.AssertSameValue(want, got);
        Assert.True(Jira.Changed(stored, got), "an edited form does not count as changed");
        // The connection stays, and the stored account is not touched.
        Assert.Equal(stored.Jira!.SiteUrl, got.Jira!.SiteUrl);
        Assert.Equal(stored.Jira.Login, got.Jira.Login);
        Assert.Equal(stored.Jira.CloudId, got.Jira.CloudId);
        Assert.Equal(stored.Email, got.Email);
        ApiJson.AssertSameValue(CloudAccount(), stored);

        // Back to the defaults: everything optional is left out.
        var back = Jira.NewSettingsForm(got) with
        {
            OfflineDays = 0,
            OnlyMine = false,
            ShowEvents = true,
            DisabledFolders = [],
            ClosedStatuses = [],
            NotificationMail = NotificationMailMode.Sync,
            NotificationSenders = [],
            BotNames = [" "],
            MetadataFilters = [],
            AuthorPrefixes = [],
            Spaces = CloudAccount().Jira!.Spaces,
            Name = "Acme",
        };
        ApiJson.AssertSameValue(CloudAccount(), back.Apply(got));

        // An empty name is the site's host; the window is kept within the limit.
        f = Jira.NewSettingsForm(stored) with { Name = " \t", OfflineDays = 5000 };
        got = f.Apply(stored);
        Assert.Equal("acme.atlassian.net", got.Name);
        Assert.Equal(API.Limits.MaxJiraOfflineDays, got.Jira!.OfflineDays);
        f = f with { OfflineDays = -3, Name = new string('ž', 200) };
        got = f.Apply(stored);
        Assert.Null(got.Jira!.OfflineDays); // Go's 0
        Assert.Equal(256, Encoding.UTF8.GetByteCount(got.Name));
        // An unknown mode is the default; an account of another kind is left alone.
        f = f with { NotificationMail = "later" };
        Assert.Null(f.Apply(stored).Jira!.NotificationMail);
        var mail = new AccountConfig { Name = "Mail", Email = "jana@acme.example" };
        Assert.Same(mail, f.Apply(mail));
    }

    [Fact]
    public void SettingsProblem()
    {
        var f = Jira.NewSettingsForm(CloudAccount());
        Assert.Equal("", f.SettingsProblem());
        Assert.Equal("Select at least one space", (f with { Spaces = [] }).SettingsProblem());
        var tooMany = Enumerable.Range(0, API.Limits.MaxJiraStatuses + 1).Select(i => new StatusRef { Id = i.ToString(CultureInfo.InvariantCulture) }).ToArray();
        Assert.Equal("Select at most 64 statuses", (f with { ClosedStatuses = tooMany }).SettingsProblem());
    }

    private static AccountConfig Edited(Func<AccountConfig, AccountConfig> edit) =>
        edit(WithJira(CloudAccount(), j => j with
        {
            ClosedStatuses = [new StatusRef { Id = "5", Name = "Resolved" }, new StatusRef { Id = "6", Name = "Done" }],
            DisabledFolders = [VirtualFolder.Watching, VirtualFolder.Open],
            BotNames = ["Deploy Bot", Jira.SuggestedBotName],
            AuthorPrefixes = ["ACME", "Globex"],
        }));

    public static TheoryData<string, Func<AccountConfig, AccountConfig>, bool> ChangedCases => new()
    {
        { "nothing", c => c, false },
        { "the default window written out", c => WithJira(c, j => j with { OfflineDays = 30 }), false },
        { "the default mode written out", c => WithJira(c, j => j with { NotificationMail = NotificationMailMode.Sync }), false },
        { "spaces around the name", c => c with { Name = " Acme " }, false },
        { "the order of the spaces", c => WithJira(c, j => j with { Spaces = [j.Spaces[1], j.Spaces[0]] }), false },
        { "the name of a space", c => WithJira(c, j => j with { Spaces = [j.Spaces[0] with { Name = "Help Desk" }, j.Spaces[1]] }), false },
        {
            "the name and order of the statuses",
            c => WithJira(c, j => j with { ClosedStatuses = [new StatusRef { Id = "6" }, new StatusRef { Id = "5", Name = "Closed" }, new StatusRef { Id = "6" }] }),
            false
        },
        { "the order of the views", c => WithJira(c, j => j with { DisabledFolders = [VirtualFolder.Open, VirtualFolder.Watching, VirtualFolder.Open] }), false },
        { "the order and spelling of the bots", c => WithJira(c, j => j with { BotNames = [" issue sync - synchronization for jira", "Deploy Bot", "deploy bot"] }), false },
        { "an empty list for none", c => WithJira(c, j => j with { MetadataFilters = [] }), false },
        { "the kind written out", c => c with { Kind = AccountKind.Jira }, false },
        { "the name", c => c with { Name = "Acme Jira" }, true },
        { "a space more", c => WithJira(c, j => j with { Spaces = [.. j.Spaces, new SpaceRef { Id = "10003", Key = "MOB" }] }), true },
        { "the key of a space", c => WithJira(c, j => j with { Spaces = [j.Spaces[0] with { Key = "HELP" }, j.Spaces[1]] }), true },
        { "the window", c => WithJira(c, j => j with { OfflineDays = 90 }), true },
        { "only mine", c => WithJira(c, j => j with { OnlyMine = true }), true },
        { "the events", c => WithJira(c, j => j with { HideEvents = true }), true },
        { "a view", c => WithJira(c, j => j with { DisabledFolders = [VirtualFolder.Watching] }), true },
        { "a status", c => WithJira(c, j => j with { ClosedStatuses = [j.ClosedStatuses![0]] }), true },
        { "no statuses", c => WithJira(c, j => j with { ClosedStatuses = null }), true },
        { "the mode", c => WithJira(c, j => j with { NotificationMail = NotificationMailMode.Ignore }), true },
        { "a sender", c => WithJira(c, j => j with { NotificationSenders = ["@acme.example"] }), true },
        { "a bot", c => WithJira(c, j => j with { BotNames = [j.BotNames![0]] }), true },
        { "a pattern", c => WithJira(c, j => j with { MetadataFilters = ["^x$"] }), true },
        { "the order of the prefixes", c => WithJira(c, j => j with { AuthorPrefixes = ["Globex", "ACME"] }), true },
        { "the site", c => WithJira(c, j => j with { SiteUrl = "https://globex.atlassian.net" }), true },
        { "the login", c => WithJira(c, j => j with { Login = "petr@acme.example" }), true },
        { "the interval", c => c with { SyncIntervalSeconds = 600 }, true },
    };

    [Theory]
    [MemberData(nameof(ChangedCases))]
    public void Changed(string name, Func<AccountConfig, AccountConfig> edit, bool want) =>
        Assert.True(want == Jira.Changed(Edited(c => c), Edited(edit)), $"Changed after {name}");

    [Fact]
    public void ChangedOfMailAccounts()
    {
        var mail = new AccountConfig { Name = "Mail", Email = "jana@acme.example" };
        Assert.False(Jira.Changed(mail, mail));
        Assert.True(Jira.Changed(mail, mail with { Email = "petr@acme.example" }));
        Assert.True(Jira.Changed(mail, Edited(c => c)));
    }
}
