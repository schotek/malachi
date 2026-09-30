// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraAccountsTests.swift, the
// counterpart of ui/internal/window/accounts_page_test.go
// (TestJiraAccountRowTitleAndSubtitle, TestAccountEditorByKind,
// TestJiraAuthBannerNamesTheToken): Jira accounts in Preferences → Accounts
// (AccountsPage, AccountRow) and in the sign-in banner (SyncStatusTexts,
// SyncController).

using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;

namespace Malachi.Core.Tests.Model;

public sealed class JiraAccountsTests
{
    // A Jira account of these tests.
    private static Account Jira(string id = "j1", string name = "Acme", string site = "https://acme.atlassian.net") => new()
    {
        Id = id,
        Config = new AccountConfig
        {
            Name = name,
            Email = "jana@acme.example",
            Kind = AccountKind.Jira,
            Jira = new JiraConfig { SiteUrl = site, Deployment = JiraDeployment.Cloud, Login = "jana@acme.example" },
        },
        Enabled = true,
        State = new SyncState { AccountId = id, Status = SyncStatus.AuthRequired },
        Capabilities = [],
    };

    private static (string, string) Row(Account a) => (AccountsPage.AccountRowTitle(a), AccountsPage.AccountRowSubtitle(a));

    [Fact]
    public void RowTitleAndSubtitle()
    {
        Assert.Equal(("Acme", "acme.atlassian.net"), Row(Jira()));
        // Unnamed: the host is the title, the address under it.
        Assert.Equal(("acme.atlassian.net", "jana@acme.example"), Row(Jira(name: "")));
        // A site that is no URL: the address.
        Assert.Equal(("jana@acme.example", "jana@acme.example"), Row(Jira(name: "", site: "::")));

        // Mail accounts as before.
        Assert.Equal(("Work", "me@example.invalid"), Row(TestAccount("m", name: "Work", email: "me@example.invalid")));
        Assert.Equal(("me@example.invalid", "me@example.invalid"), Row(TestAccount("m", email: "me@example.invalid")));

        // The row of the Accounts page shows both, and the Jira icon.
        var row = AccountRow.For(Jira());
        Assert.Equal(("Acme", "acme.atlassian.net", "checkbox-checked-symbolic"), (row.Title, row.Subtitle, row.Icon));
        Assert.Equal("mail-unread-symbolic", AccountRow.For(TestAccount("m", email: "me@example.invalid")).Icon);
    }

    [Fact]
    public void EditorByKind()
    {
        Assert.Equal(AccountEditor.Jira, AccountsPage.EditorOf(Jira()));
        Assert.Equal(AccountEditor.MailWizard, AccountsPage.EditorOf(TestAccount("m")));
        var graph = TestAccount("g");
        graph = graph with { Config = graph.Config with { Kind = AccountKind.Graph } };
        Assert.Equal(AccountEditor.MailWizard, AccountsPage.EditorOf(graph));
        Assert.Equal(JiraEditor.Settings, AccountsPage.JiraEditorFor(null));
        Assert.Equal(JiraEditor.Token, AccountsPage.JiraEditorFor(ErrorCode.AuthFailed));
        Assert.Equal(JiraEditor.Token, AccountsPage.JiraEditorFor(ErrorCode.AuthRequired));
        Assert.Equal(AccountsPage.JiraAccountIcon, AccountsPage.AccountIcon(Jira()));
        // The row offers no browser sign-in for a Jira account.
        Assert.False(AccountsPage.AccountRowOffersSignIn(Jira()));
    }

    [Theory]
    [InlineData(ErrorCode.AuthRequired, "No API token is stored for Acme")]
    [InlineData(ErrorCode.AuthFailed, "The Jira site rejected the token of Acme")]
    [InlineData(ErrorCode.KeyringError, "The system keyring is unavailable; Acme cannot sign in")]
    [InlineData(ErrorCode.NetworkError, "Acme needs attention")]
    public void AuthBannerNamesTheToken(int reason, string want)
    {
        var a = Jira();
        Assert.Equal(want, SyncStatusTexts.AccountAuthBannerTitle(a, Provider.SignInKindOf(a.Config), new ErrorCode(reason), "Acme"));
    }

    [Fact]
    public void MailAccountsKeepThePasswordsSentences()
    {
        Assert.Equal("No password is stored for Work", SyncStatusTexts.AccountAuthBannerTitle(TestAccount("m"), SignInKind.Password, ErrorCode.AuthRequired, "Work"));
        // An account not listed yet reads as mail.
        Assert.Equal("The server rejected the password of Work", SyncStatusTexts.AccountAuthBannerTitle(null, SignInKind.Password, ErrorCode.AuthFailed, "Work"));
    }

    [Fact]
    public void SyncControllerBannerOfAJiraAccount()
    {
        var a = Jira();
        AuthRequiredNotification N(ErrorCode reason) => new() { AccountId = a.Id, Reason = reason, Message = "detail" };
        // The token is entered again in the account's own assistant.
        Assert.Equal(("No API token is stored for Acme", "Edit Account…"), SyncController.AuthBannerFor(N(ErrorCode.AuthRequired), a));
        Assert.Equal(("The Jira site rejected the token of Acme", "Edit Account…"), SyncController.AuthBannerFor(N(ErrorCode.AuthFailed), a));
        Assert.Equal(("The system keyring is unavailable; Acme cannot sign in", "Open Preferences"), SyncController.AuthBannerFor(N(ErrorCode.KeyringError), a));
        Assert.Equal(new AuthBannerAction.EditAccount(a.Id, ErrorCode.AuthFailed), SyncController.AuthBannerActionFor(N(ErrorCode.AuthFailed), a));
        Assert.Equal(new AuthBannerAction.EditAccount(a.Id, ErrorCode.AuthRequired), SyncController.AuthBannerActionFor(N(ErrorCode.AuthRequired), a));
    }
}
