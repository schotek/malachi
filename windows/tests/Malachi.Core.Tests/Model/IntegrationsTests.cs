// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/IntegrationsTests.swift; GTK:
// ui/internal/integrations/integrations_test.go (TestPredicatesByKind,
// TestSplitKeepsTheOrder, TestFullOrder, TestCatalog, TestRowForByKind,
// TestTexts). The texts are the msgids (no catalogue is loaded).

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class IntegrationsTests
{
    // integrations_test.go account: an account of the kind with its block.
    private static Account Account(string id, string? kind)
    {
        var config = new AccountConfig { Name = "", Kind = kind is null ? (AccountKind?)null : new AccountKind(kind), Email = id + "@example.invalid" };
        config = kind switch
        {
            AccountKind.Jira => config with { Jira = new JiraConfig { SiteUrl = "https://Acme.atlassian.net/", Deployment = JiraDeployment.Cloud } },
            AccountKind.CalDAV => config with { CalDAV = new CalDAVConfig { ServerUrl = "https://Cloud.example.org/remote.php/dav/", Username = "me" } },
            AccountKind.Eway => config with { Eway = new EwayConfig { ServiceUrl = "https://Hosting.eway-crm.com/acme", Username = "jana" } },
            _ => config,
        };
        return new Account
        {
            Id = new AccountId(id),
            Config = config,
            Enabled = true,
            State = new SyncState { AccountId = new AccountId(id), Status = SyncStatus.Idle },
        };
    }

    private static IEnumerable<string> Ids(IEnumerable<Account> accounts) => accounts.Select(a => a.Id.Value);

    private static List<AccountId> Order(params string[] ids) => [.. ids.Select(i => new AccountId(i))];

    private static readonly Account[] All =
    [
        Account("j1", AccountKind.Jira), Account("m1", null), Account("c1", AccountKind.CalDAV),
        Account("m2", AccountKind.Graph), Account("j2", AccountKind.Jira), Account("m3", AccountKind.Imap),
    ];

    [Theory]
    [InlineData(null, true)]
    [InlineData("", true)]
    [InlineData(AccountKind.Imap, true)]
    [InlineData(AccountKind.Graph, true)]
    [InlineData(AccountKind.Jira, false)]
    [InlineData(AccountKind.CalDAV, false)]
    [InlineData(AccountKind.Eway, false)]
    [InlineData("future", false)]
    public void PredicatesByKind(string? kind, bool mail)
    {
        var a = Account("a", kind);
        Assert.Equal(mail, Integrations.ShowsInAccounts(a));
        Assert.Equal(!mail, Integrations.ShowsInIntegrations(a));
        Assert.Equal(mail, Integrations.ShowsIn(Integrations.Page.Accounts, a));
        Assert.Equal(!mail, Integrations.ShowsIn(Integrations.Page.Integrations, a));
    }

    [Fact]
    public void SplitKeepsTheOrder()
    {
        var (mail, other) = Integrations.Split(All);
        Assert.Equal(["m1", "m2", "m3"], Ids(mail));
        Assert.Equal(["j1", "c1", "j2"], Ids(other));
        var (none, nothing) = Integrations.Split([]);
        Assert.Empty(none);
        Assert.Empty(nothing);
    }

    public static TheoryData<string, Integrations.Page, string[], string[]> FullOrderCases => new()
    {
        { "accounts reordered", Integrations.Page.Accounts, ["m3", "m1", "m2"], ["m3", "m1", "m2", "j1", "c1", "j2"] },
        { "integrations reordered", Integrations.Page.Integrations, ["j2", "c1", "j1"], ["m1", "m2", "m3", "j2", "c1", "j1"] },
        { "unknown and other page's ids ignored", Integrations.Page.Accounts, ["x", "j1", "m2", "m2", "m1", "m3"], ["m2", "m1", "m3", "j1", "c1", "j2"] },
        { "omitted keep their order", Integrations.Page.Integrations, ["j2"], ["m1", "m2", "m3", "j2", "j1", "c1"] },
        { "empty order", Integrations.Page.Accounts, [], ["m1", "m2", "m3", "j1", "c1", "j2"] },
    };

    [Theory]
    [MemberData(nameof(FullOrderCases))]
    public void FullOrder(string name, Integrations.Page page, string[] order, string[] want)
    {
        var got = Integrations.FullOrder(All, page, Order(order)).Select(id => id.Value).ToList();
        Assert.True(want.SequenceEqual(got), name + ": " + string.Join(",", got));
        // Every id exactly once.
        Assert.Equal(All.Length, got.Count);
        Assert.Equal(All.Length, got.Distinct().Count());
    }

    [Fact]
    public void FullOrderOfNoAccountsOrNoOrder()
    {
        Assert.Empty(Integrations.FullOrder([], Integrations.Page.Accounts, Order("a")));
        Assert.Equal(["m1", "m2", "m3", "j1", "c1", "j2"], Integrations.FullOrder(All, Integrations.Page.Integrations, null).Select(id => id.Value));
    }

    [Fact]
    public void Catalog()
    {
        var c = Integrations.Catalog();
        Assert.Equal(3, c.Count);
        Assert.Equal(new AccountKind(AccountKind.Jira), c[0].Kind);
        Assert.Equal(new AccountKind(AccountKind.CalDAV), c[1].Kind);
        Assert.Equal(new AccountKind(AccountKind.Eway), c[2].Kind);
        Assert.Equal("eWay-CRM", c[2].Name);
        Assert.Equal(Integrations.EwayIcon, c[2].Icon);
        Assert.Equal("Save messages to your CRM with their contacts, companies and deals", c[2].Description);
        Assert.Equal("Jira", c[0].Name);
        Assert.Equal(Integrations.JiraIcon, c[0].Icon);
        Assert.Equal("CalDAV Calendar", c[1].Name);
        Assert.Equal(Integrations.CalDAVIcon, c[1].Icon);
        Assert.All(c, s => Assert.NotEqual("", s.Description));
    }

    [Fact]
    public void RowForByKind()
    {
        var mail = Account("m", null);
        mail = mail with { Config = mail.Config with { Name = "Work" } };
        var r = Integrations.RowFor(mail);
        Assert.Equal(new AccountId("m"), r.Id);
        Assert.Equal(new AccountKind(AccountKind.Imap), r.Kind);
        Assert.Equal(("Work", "m@example.invalid"), (r.Title, r.Subtitle));
        Assert.True(r.OffersSwitch);
        Assert.Equal(Integrations.Editor.MailWizard, r.Editor);
        Assert.Equal("Edit Account", r.EditLabel);
        Assert.Equal(Integrations.MailIcon, r.Icon);
        Assert.Contains("Mail on the server", r.RemoveBody, System.StringComparison.Ordinal);
        Assert.StartsWith("m@example.invalid ", r.RemoveBody, System.StringComparison.Ordinal);

        var unnamed = Integrations.RowFor(Account("g", AccountKind.Graph));
        Assert.Equal(("g@example.invalid", "g@example.invalid"), (unnamed.Title, unnamed.Subtitle));
        Assert.Equal(Integrations.Editor.MailWizard, unnamed.Editor);

        var j = Account("j", AccountKind.Jira);
        r = Integrations.RowFor(j);
        Assert.Equal(("acme.atlassian.net", "j@example.invalid"), (r.Title, r.Subtitle));
        Assert.True(r.OffersSwitch);
        Assert.Equal(Integrations.Editor.Jira, r.Editor);
        Assert.Equal(Integrations.JiraIcon, r.Icon);
        Assert.Equal("Edit Account", r.EditLabel);
        Assert.Equal("acme.atlassian.net will be removed from Malachi Mail. Issues on the Jira site are not affected.", r.RemoveBody);
        j = j with { Config = j.Config with { Name = "Acme" } };
        r = Integrations.RowFor(j);
        Assert.Equal(("Acme", "acme.atlassian.net"), (r.Title, r.Subtitle));

        var c = Account("c", AccountKind.CalDAV);
        c = c with { Config = c.Config with { Name = "Nextcloud" } };
        r = Integrations.RowFor(c);
        Assert.Equal(("Nextcloud", "cloud.example.org"), (r.Title, r.Subtitle));
        Assert.False(r.OffersSwitch);
        Assert.Equal(Integrations.Editor.CalendarPage, r.Editor);
        Assert.Equal("Calendar Settings", r.EditLabel);
        Assert.Equal(Integrations.CalDAVIcon, r.Icon);
        Assert.Equal("Nextcloud will be removed from Malachi Mail. Calendars on the server are not affected.", r.RemoveBody);
        c = c with { Config = c.Config with { Name = "", CalDAV = null } };
        r = Integrations.RowFor(c);
        Assert.Equal(("c@example.invalid", "c@example.invalid"), (r.Title, r.Subtitle));

        // eWay-CRM (integrations_test.go TestRowForByKind "eway").
        var e = Account("e", AccountKind.Eway);
        r = Integrations.RowFor(e);
        Assert.Equal(("e@example.invalid", "hosting.eway-crm.com/acme"), (r.Title, r.Subtitle));
        Assert.True(r.OffersSwitch);
        Assert.Equal(Integrations.Editor.Eway, r.Editor);
        Assert.Equal("Edit Account", r.EditLabel);
        Assert.Equal(Integrations.EwayIcon, r.Icon);
        e = e with { Config = e.Config with { Name = "Acme CRM" } };
        r = Integrations.RowFor(e);
        Assert.Equal("Acme CRM", r.Title);
        Assert.Equal("Acme CRM will be removed from Malachi Mail. Messages saved in eWay-CRM stay there.", r.RemoveBody);
        Assert.True(Integrations.ShowsInIntegrations(e));
        Assert.False(Integrations.ShowsInAccounts(e));

        var f = Integrations.RowFor(Account("f", "future"));
        Assert.Equal(Integrations.Editor.None, f.Editor);
        Assert.True(f.OffersSwitch);
        Assert.Equal("f@example.invalid will be removed from Malachi Mail.", f.RemoveBody);
    }

    [Fact]
    public void Texts()
    {
        var x = Integrations.Texts();
        Assert.All(
            new[] { x.Title, x.Description, x.ConnectedTitle, x.AvailableTitle, x.EmptyTitle, x.EmptySubtitle, x.Add, x.CalendarSettings },
            s => Assert.NotEqual("", s));
        Assert.Equal("Integrations", x.Title);
        Assert.Equal("Add…", x.Add);
        Assert.Equal("Calendar Settings", x.CalendarSettings);
        Assert.Equal("Services beside your mail: issue trackers, calendars and CRM", x.Description);
    }

    // AccountsPage's helpers follow RowFor (accounts_page.go accountRowTitle,
    // accountRowSubtitle, accountIcon): a CalDAV row is the calendar's.
    [Fact]
    public void TheAccountsPageHelpersFollowRowFor()
    {
        var c = Account("c", AccountKind.CalDAV);
        Assert.Equal("c@example.invalid", AccountsPage.AccountRowTitle(c));
        Assert.Equal("cloud.example.org", AccountsPage.AccountRowSubtitle(c));
        Assert.Equal(Integrations.CalDAVIcon, AccountsPage.AccountIcon(c));
        Assert.Equal(Integrations.JiraIcon, AccountsPage.AccountIcon(Account("j", AccountKind.Jira)));
        Assert.Equal(Integrations.MailIcon, AccountsPage.AccountIcon(Account("m", AccountKind.Imap)));
    }
}
