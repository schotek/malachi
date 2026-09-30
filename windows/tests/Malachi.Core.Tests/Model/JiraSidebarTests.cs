// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Jira cases of macos/Tests/MalachiCoreTests/FolderTreeTests.swift
// (jiraViewsSortAboveTheSpaces, jiraViewsHaveTheirIconAndTitle,
// jiraAccountLabelFallsBackToTheSite, accountHeaderBadgeSaysTheKind,
// initialFolderPrefersTheMailInbox), the counterpart of
// ui/internal/window/jira_list_test.go (TestJiraSidebar): the sidebar of a
// Jira account. The fixed views sort below the role folders and above the
// spaces, have a saved search's icon and a localised title, an unnamed
// account is named after its site, and the heading's capsule says what kind
// of account it is. The Czech titles of the views are in
// JiraTranslationTests; the Windows-only case is the sidebar row
// (SidebarRow's capsule and icon).

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Xunit;
using static Malachi.Core.Tests.Model.FolderTreeTests;
using static Malachi.Core.Tests.Model.MailModelTests;
using FolderMap = System.Collections.Generic.Dictionary<Malachi.Core.Api.AccountId, System.Collections.Generic.IReadOnlyList<Malachi.Core.Api.Folder>>;

namespace Malachi.Core.Tests.Model;

public sealed class JiraSidebarTests
{
    // A virtual folder of a Jira account (role none, Virtual set).
    private static Folder View(string id, VirtualFolder v, string? name = null, string? path = null) =>
        TestFolder(id, path ?? id, name: name) with { Virtual = v };

    // A Jira Cloud account of the sidebar tests.
    internal static Account JiraAccount(string id, string name = "") => new()
    {
        Id = id,
        Config = new AccountConfig
        {
            Name = name,
            Email = "jana@acme.example",
            Kind = AccountKind.Jira,
            Jira = new JiraConfig { SiteUrl = "https://Acme.Atlassian.net:443", Deployment = JiraDeployment.Cloud, Login = "jana@acme.example" },
        },
        Enabled = true,
        State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
        Capabilities = [],
    };

    private static string[] Order(IEnumerable<Folder> list) => [.. FolderTree.SortSiblings(list).Select(f => f.Id.Value)];

    [Fact]
    public void ViewsSortAboveTheSpaces()
    {
        Account[] accounts = [JiraAccount("j")];
        var folders = new FolderMap
        {
            ["j"] =
            [
                TestFolder("web", "Website"),
                TestFolder("itsd", "IT Service Desk"),
                View("open", VirtualFolder.Open),
                View("mine", VirtualFolder.AssignedToMe),
                View("watch", VirtualFolder.Watching),
                // A queued comment keeps the outbox above the views; an empty
                // one is not listed.
                TestFolder("out", "Outbox", FolderRole.Outbox, total: 1),
            ],
        };
        // One account: no heading.
        string[] want = ["out@0", "mine@0", "watch@0", "open@0", "itsd@0", "web@0"];
        Assert.Equal(want, Ids(FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState())));
        var empty = new FolderMap { ["j"] = [.. folders["j"].Take(5), TestFolder("out", "Outbox", FolderRole.Outbox)] };
        Assert.Equal(want.Where(id => id != "out@0"), Ids(FolderTree.SortFolders(accounts, empty, new CollapseState(), new FavouriteState())));
        // A view this client does not know sorts like a space.
        Assert.Equal(["open", "abc", "later"], Order([View("later", "later", path: "Later"), TestFolder("abc", "ABC"), View("open", VirtualFolder.Open)]));
        // Mail accounts keep their order.
        Assert.Equal(["in", "A", "b"], Order([TestFolder("b", "beta"), TestFolder("in", "INBOX", FolderRole.Inbox), TestFolder("A", "Alpha")]));
    }

    // jira_list_test.go TestJiraSidebar: the views after the role folders,
    // before the spaces, by their rank, whatever their paths.
    [Fact]
    public void ViewsSortByRank()
    {
        Assert.Equal(
            ["out", "assigned", "watching", "open", "space:2"],
            Order(
            [
                TestFolder("space:2", "Alpha"),
                View("open", VirtualFolder.Open, path: "open"),
                TestFolder("out", "Outbox", FolderRole.Outbox),
                View("watching", VirtualFolder.Watching, path: "watching"),
                View("assigned", VirtualFolder.AssignedToMe, path: "zzz"),
            ]));
    }

    [Fact]
    public void ViewsHaveTheirIconAndTitle()
    {
        Assert.Equal("folder-saved-search-symbolic", FolderTree.FolderIcon(View("mine", VirtualFolder.AssignedToMe)));
        Assert.Equal("folder-saved-search-symbolic", FolderTree.FolderIcon(View("open", VirtualFolder.Open)));
        Assert.Equal("mail-unread-symbolic", FolderTree.FolderIcon(TestFolder("in", "INBOX", FolderRole.Inbox)));
        Assert.Equal("folder-symbolic", FolderTree.FolderIcon(TestFolder("itsd", "IT Service Desk")));
        Assert.Equal("mail-send-symbolic", FolderTree.FolderIcon(TestFolder("out", "Outbox", FolderRole.Outbox)));

        // The daemon's English name is only a fallback: the code decides.
        Assert.Equal("Assigned to Me", FolderTree.FolderTitle(View("mine", VirtualFolder.AssignedToMe, name: "assigned")));
        Assert.Equal("Watching", FolderTree.FolderTitle(View("watch", VirtualFolder.Watching, name: "watching")));
        Assert.Equal("Open", FolderTree.FolderTitle(View("open", VirtualFolder.Open, name: "open")));
        Assert.Equal("Later", FolderTree.FolderTitle(View("later", "later", name: "Later")));
        Assert.Equal("IT Service Desk", FolderTree.FolderTitle(TestFolder("itsd", "IT Service Desk", name: "IT Service Desk")));
    }

    [Fact]
    public void AccountLabelFallsBackToTheSite()
    {
        var a = JiraAccount("j", name: "  Acme Jira ");
        Assert.Equal("Acme Jira", FolderTree.AccountLabel(a));
        a = a with { Config = a.Config with { Name = " " } };
        Assert.Equal("acme.atlassian.net", FolderTree.AccountLabel(a));
        a = a with { Config = a.Config with { Jira = a.Config.Jira! with { SiteUrl = "not a url" } } };
        Assert.Equal("jana@acme.example", FolderTree.AccountLabel(a));
        // A mail account is unchanged.
        Assert.Equal("me@example.invalid", FolderTree.AccountLabel(TestAccount("m", email: " me@example.invalid ")));

        Assert.Equal("JIRA", FolderTree.AccountHeaderBadge(JiraAccount("j")));
    }

    // jira_list_test.go TestJiraSidebar, the capsules: a mail account names
    // the provider it signs in with, else the protocol.
    [Fact]
    public void AccountHeaderBadgeSaysTheKind()
    {
        var mail = TestAccount("a", email: "a@example.invalid");
        Assert.Equal("IMAP", FolderTree.AccountHeaderBadge(mail));
        var google = mail with { Config = mail.Config with { OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google } } };
        var graph = mail with { Config = mail.Config with { Kind = AccountKind.Graph } };
        var office = mail with { Config = mail.Config with { OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Office365 } } };
        Assert.Equal("GOOGLE", FolderTree.AccountHeaderBadge(google));
        Assert.Equal("M365", FolderTree.AccountHeaderBadge(graph));
        Assert.Equal("M365", FolderTree.AccountHeaderBadge(office));
        // A provider this client does not name is a mail account like any.
        var custom = mail with { Config = mail.Config with { OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = "custom" } } };
        Assert.Equal("IMAP", FolderTree.AccountHeaderBadge(custom));
    }

    [Fact]
    public void InitialFolderPrefersTheMailInbox()
    {
        // The Jira account comes first and has no Inbox: the mail account's
        // Inbox still wins.
        Account[] accounts = [JiraAccount("j"), TestAccount("m")];
        var folders = new FolderMap
        {
            ["j"] = [View("mine", VirtualFolder.AssignedToMe), TestFolder("itsd", "IT Service Desk")],
            ["m"] = [TestFolder("arch", "Archive", FolderRole.Archive), TestFolder("in", "INBOX", FolderRole.Inbox)],
        };
        var m = new MailModel(accounts, folders);
        m.RebuildEntries();
        Assert.Equal(["#j", "mine@0", "itsd@0", "#m", "in@0", "arch@0"], Ids(m.Entries));
        Assert.Equal(new FolderKey("m", "in"), m.InitialFolder());
        // Without a mail account the first view is it.
        var jiraOnly = new MailModel([accounts[0]], folders);
        jiraOnly.RebuildEntries();
        Assert.Equal(new FolderKey("j", "mine"), jiraOnly.InitialFolder());
    }

    // Windows-only: the sidebar row shows the heading's capsule (after the
    // name, for a screen reader too) and a view's icon.
    [Fact]
    public void SidebarRowShowsTheCapsuleAndTheViewsIcon()
    {
        Account[] accounts = [JiraAccount("j"), TestAccount("m", email: "a@example.invalid")];
        var folders = new FolderMap
        {
            ["j"] = [View("mine", VirtualFolder.AssignedToMe)],
            ["m"] = [TestFolder("in", "INBOX", FolderRole.Inbox)],
        };
        var entries = FolderTree.SortFolders(accounts, folders, new CollapseState(), new FavouriteState());
        static SidebarRow Row(FolderEntry e)
        {
            var row = new SidebarRow(SidebarKey.Of(e));
            row.Update(e, several: true);
            return row;
        }
        var rows = entries.Select(Row).ToArray();
        Assert.Equal(("acme.atlassian.net", "JIRA"), (rows[0].Title, rows[0].Capsule));
        Assert.Equal("acme.atlassian.net, JIRA", rows[0].ToString());
        Assert.Equal(("Assigned to Me", "", "folder-saved-search-symbolic"), (rows[1].Title, rows[1].Capsule, rows[1].Icon));
        Assert.Equal(("a@example.invalid", "IMAP"), (rows[2].Title, rows[2].Capsule));
        Assert.Equal(("", "mail-unread-symbolic"), (rows[3].Capsule, rows[3].Icon));
    }
}
