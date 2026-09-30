// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraWizardControllerTests.swift, the
// tests of ui/internal/accountwizard/jira_flow_test.go: the Jira account
// assistant against a fake daemon (account.detectSite, account.listSpaces,
// account.add and account.update). Fictional sites and people only.
//
// Swift waits with waitUntil and sleeps; here a test waits until nothing is
// left to happen (IdleAsync), and an answer that Swift delays is held until
// the test releases it (HeldAnswer), as in WizardControllerTests. Swift's
// onOpenURL is the ILauncher the assistant is given (RecordingLauncher).
// Added: a token page that does not open says why.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Controllers.DaemonHarness;
using Field = Malachi.Core.Controllers.JiraWizardController.Field;
using Page = Malachi.Core.IssueTrackers.JiraWizardPage;

namespace Malachi.Core.Tests.Controllers;

public sealed class JiraWizardControllerTests
{
    private const string CloudId = "0b9e3d2c-1a2b-4c3d-8e9f-001122334455";
    private const string CloudSiteJson = """{"kind":"jira","siteUrl":"https://acme.atlassian.net","deployment":"cloud","cloudId":"0b9e3d2c-1a2b-4c3d-8e9f-001122334455","title":"Acme"}""";
    private const string DcSiteJson = """{"kind":"jira","siteUrl":"https://jira.acme.example/jira","deployment":"datacenter","title":"Acme Jira","version":"9.12.4"}""";

    private const string SpacesJson =
        """{"user":{"name":"Jana Dvořáková","email":"jana@acme.example"},"spaces":["""
        + """{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":120},"""
        + """{"id":"10002","key":"WEB","name":"Website","issues":1},"""
        + """{"id":"10003","key":"MOB","name":"Mobile","issues":-1}],"""
        + """ "statuses":[{"id":"3","name":"In Progress","category":"inProgress"}]}""";

    private const string RecountedJson =
        """{"user":{"name":"Jana Dvořáková","email":"jana@acme.example"},"spaces":["""
        + """{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":300},"""
        + """{"id":"10002","key":"WEB","name":"Website","issues":4},"""
        + """{"id":"10003","key":"MOB","name":"Mobile","issues":0}]}""";

    // A Data Center user whose address the site does not reveal, one space.
    private const string DcSpacesJson = """{"user":{"name":"Jana Dvořáková"},"spaces":[{"id":"20001","key":"OPS","name":"Operations","issues":7}]}""";

    // The rows of SpacesJson on the spaces page.
    private static readonly JiraSpaceRow[] SpaceRows =
    [
        new() { Id = "10001", Title = "ITSD – IT Service Desk", Count = "about 120 issues", ServiceDesk = true },
        new() { Id = "10002", Title = "WEB – Website", Count = "about 1 issue" },
        new() { Id = "10003", Title = "MOB – Mobile", Count = "" },
    ];

    [Fact]
    public async Task AddsACloudAccount()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectSite.Name, _ => CloudSiteJson);
        h.On(API.AccountListSpaces.Name, _ => SpacesJson);
        h.On(API.AccountAdd.Name, _ => """{"accountId":"acc-7"}""");
        var (w, rec) = await h.StartWizardAsync();
        Assert.Equal(["Site"], rec.Pages);
        Assert.Equal([(false, "")], rec.Checks);
        Assert.True(!w.IsEditing && !w.CanGoBack && w.LoginEditable);
        Assert.Equal("Add Jira Account", w.Title);
        Assert.Equal(("Jira Site", "Sign In", "Spaces"), (w.PageTitle(Page.Site), w.PageTitle(Page.Credentials), w.PageTitle(Page.Spaces)));
        Assert.Equal(("Next", "Next", "Add Account"), (w.NextLabel(Page.Site), w.NextLabel(Page.Credentials), w.NextLabel(Page.Spaces)));
        Assert.Equal(["1 week", "1 month", "3 months", "1 year"], JiraWizardController.OfflineLabels);
        Assert.Equal(1, w.OfflineIndex); // 30 days by default

        // Nothing typed: nothing asked.
        await h.Ui.RunAsync(w.Next);
        Assert.Equal(["Site"], rec.Problems);
        Assert.Empty(rec.Busy);

        await h.Ui.RunAsync(() => w.SetSite("  acme.atlassian.net "));
        Assert.Equal((true, ""), rec.Checks[^1]);
        Assert.Equal("", rec.Problems[^1]);
        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Equal("Site,Credentials", rec.Pages[^1]);
        Assert.Equal(["Looking up the Jira site", null], rec.Busy);
        Assert.Equal(["Found Acme"], rec.Detected);
        Assert.Equal(Jira.CredentialFields(JiraDeployment.Cloud), rec.CredentialPages[^1]);
        Assert.Equal(Field.Login, rec.Focus[^1]);
        Assert.True(w.CanGoBack);
        Assert.Equal("acme.atlassian.net", h.Params.Last<AccountDetectSiteParams>(API.AccountDetectSite.Name)?.Url); // sent trimmed

        // Missing credentials are flagged, nothing is asked.
        await h.Ui.RunAsync(w.Next);
        Assert.Equal("Login,Token", rec.Problems[^1]);
        Assert.Equal(Field.Login, rec.Focus[^1]);
        await h.Ui.RunAsync(() => w.SetCredentials("jana@acme.example", "tok"));
        Assert.Equal("", rec.Problems[^1]);
        await h.Ui.RunAsync(() =>
        {
            w.SetCredentials("jana@acme.example", " tok-123\n");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal("Site,Credentials,Spaces", rec.Pages[^1]);
        Assert.Equal(["Loading the spaces", null], rec.Busy[^2..]);
        var listed = h.Params.Last<AccountListSpacesParams>(API.AccountListSpaces.Name)!;
        Assert.True(listed.Counts);
        Assert.Null(listed.AccountId);
        Assert.Equal("tok-123", listed.Credentials.Password); // a pasted line break is trimmed
        Assert.Equal((AccountKind.Jira, "jana@acme.example", "Acme"), (listed.Config.Kind, listed.Config.Email, listed.Config.Name));
        Assert.Equal(("jana@acme.example", CloudId, 30), (listed.Config.Jira?.Login, listed.Config.Jira?.CloudId, listed.Config.Jira?.OfflineDays));
        Assert.Empty(listed.Config.Jira!.Spaces);
        Assert.Equal(SpaceRows, Assert.Single(rec.Spaces));
        Assert.Equal([""], rec.Selected);
        Assert.Equal("Select at least one space", rec.SpacesProblems[^1]);
        Assert.Equal((false, ""), rec.EmailFields[^1]);
        Assert.Equal(new SiteUser { Name = "Jana Dvořáková", Email = "jana@acme.example" }, w.User);

        // Add without a space: the page says why.
        await h.Ui.RunAsync(w.Next);
        Assert.Equal((Page.Spaces, "Select at least one space"), rec.Banners[^1]);
        Assert.Equal(0, h.Params.Count(API.AccountAdd.Name));

        await h.Ui.RunAsync(() =>
        {
            w.SetSpace("10002", true);
            w.SetSpace("10001", true);
            w.SetSpace("99999", true);
        });
        Assert.Equal(["10001", "10002"], w.Selected.Order(StringComparer.Ordinal)); // an unknown space is ignored
        Assert.Equal("", rec.SpacesProblems[^1]);
        await h.Ui.RunAsync(() =>
        {
            w.SetOnlyMine(true);
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(["Adding the account", null], rec.Busy[^2..]);
        var want = new AccountAddParams
        {
            Config = new AccountConfig
            {
                Name = "Acme",
                Email = "jana@acme.example",
                Kind = AccountKind.Jira,
                Jira = new JiraConfig
                {
                    SiteUrl = "https://acme.atlassian.net",
                    Deployment = JiraDeployment.Cloud,
                    CloudId = CloudId,
                    Login = "jana@acme.example",
                    Spaces = [new SpaceRef { Id = "10001", Key = "ITSD", Name = "IT Service Desk" }, new SpaceRef { Id = "10002", Key = "WEB", Name = "Website" }],
                    OfflineDays = 30,
                    OnlyMine = true,
                },
            },
            Credentials = new Credentials { Password = "tok-123" },
        };
        ApiJson.AssertSameValue(want, h.Params.Last<AccountAddParams>(API.AccountAdd.Name));
        // Exactly these members on the wire: nothing a mail account has, no
        // empty Jira list.
        var config = ApiJson.Parse(h.Params.LastRaw(API.AccountAdd.Name)!).GetProperty("config");
        Assert.Equal(["email", "jira", "kind", "name"], ApiJson.Keys(config));
        Assert.Equal(["cloudId", "deployment", "login", "offlineDays", "onlyMine", "siteUrl", "spaces"], ApiJson.Keys(config.GetProperty("jira")));
        Assert.Equal(["acc-7"], rec.Done);
        ApiJson.AssertSameValue(want.Config, Assert.Single(rec.DoneConfigs));
        Assert.Equal([API.AccountDetectSite.Name, API.AccountListSpaces.Name, API.AccountAdd.Name], h.Fake.Calls);
    }

    [Fact]
    public async Task AnAddressThatIsNotOneIsRefusedWithoutACall()
    {
        await using var h = await Harness.StartAsync();
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() => w.SetSite("acme atlassian"));
        Assert.Equal((false, "This is not a web address"), rec.Checks[^1]);
        await h.Ui.RunAsync(w.Next);
        Assert.Equal("Site", rec.Problems[^1]);
        Assert.Equal(Field.Site, rec.Focus[^1]);
        Assert.Empty(rec.Busy);
        await h.IdleAsync();
        Assert.Empty(h.Fake.Calls);
    }

    [Fact]
    public async Task AFailedLookupShowsTheBanner()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectSite.Name, Fails(ErrorCode.ServerError, "not jira"));
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetSite("example.org");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(2, rec.Busy.Count);
        Assert.Equal(["Site"], rec.Pages);
        Assert.Equal((Page.Site, "This address is not a Jira site"), rec.Banners[^1]);
        Assert.Equal("Site", rec.Problems[^1]);
        Assert.Equal(Field.Site, rec.Focus[^1]);
        Assert.Empty(rec.Detected);

        // A network failure: the client's sentence for the step.
        h.On(API.AccountDetectSite.Name, Fails(ErrorCode.NetworkError, "no route"));
        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Equal(4, rec.Busy.Count);
        Assert.Equal((Page.Site, RpcErrorText.Text("Looking up the Jira site", DaemonHarness.Daemon(ErrorCode.NetworkError, "no route"))), rec.Banners[^1]);
        Assert.Equal(["Site"], rec.Pages);

        // Typing clears the banner and the flag.
        await h.Ui.RunAsync(() => w.SetSite("jira.example.org"));
        Assert.Equal((Page.Site, null), rec.Banners[^1]);
        Assert.Equal("", rec.Problems[^1]);
    }

    [Fact]
    public async Task ASiteOfAnotherKindIsNotJira()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectSite.Name, _ => """{"kind":"tracker","siteUrl":"https://tracker.acme.example","deployment":"cloud"}""");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetSite("tracker.acme.example");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(2, rec.Busy.Count);
        Assert.Equal(["Site"], rec.Pages);
        Assert.Equal((Page.Site, "This address is not a Jira site"), rec.Banners[^1]);
        Assert.Equal("Site", rec.Problems[^1]);
        Assert.Null(w.Site);
    }

    [Fact]
    public async Task AChangedAddressForgetsTheSite()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectSite.Name, _ => CloudSiteJson);
        var (w, rec) = await h.StartWizardAsync();
        await ReachCredentialsAsync(h, w, rec);
        Assert.Equal("https://acme.atlassian.net", w.Site?.SiteUrl);
        await h.Ui.RunAsync(w.Back);
        Assert.Equal("Site", rec.Pages[^1]);
        await h.Ui.RunAsync(() => w.SetSite("acme.atlassian.net/"));
        Assert.Null(w.Site);
        Assert.Equal(["Found Acme", ""], rec.Detected);
    }

    [Fact]
    public async Task ARefusedTokenGoesBackToTheCredentials()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectSite.Name, _ => CloudSiteJson);
        h.On(API.AccountListSpaces.Name, Fails(ErrorCode.AuthFailed, "401"));
        var (w, rec) = await h.StartWizardAsync();
        await ReachCredentialsAsync(h, w, rec);
        await h.Ui.RunAsync(() =>
        {
            w.SetCredentials("jana@acme.example", "wrong");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(4, rec.Busy.Count);
        Assert.Equal("Site,Credentials", rec.Pages[^1]);
        Assert.Equal((Page.Credentials, "The Jira site rejected the token"), rec.Banners[^1]);
        Assert.Equal("Token", rec.Problems[^1]);
        Assert.Equal(Field.Token, rec.Focus[^1]);
        Assert.Empty(rec.Spaces);

        // The site lists the spaces for the next token, but refuses the add:
        // back to the credentials again.
        h.On(API.AccountListSpaces.Name, _ => SpacesJson);
        h.On(API.AccountAdd.Name, Fails(ErrorCode.AuthFailed, "401"));
        await h.Ui.RunAsync(() => w.SetCredentials("jana@acme.example", "tok-123"));
        Assert.Equal((Page.Credentials, null), rec.Banners[^1]);
        Assert.Equal("", rec.Problems[^1]);
        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Equal("Site,Credentials,Spaces", rec.Pages[^1]);
        await h.Ui.RunAsync(() =>
        {
            w.SetSpace("10001", true);
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal("Site,Credentials", rec.Pages[^1]);
        Assert.Equal((Page.Credentials, "The Jira site rejected the token"), rec.Banners[^1]);
        Assert.Equal("Token", rec.Problems[^1]);
        Assert.Empty(rec.Done);
    }

    [Fact]
    public async Task AReplyAfterBackIsDropped()
    {
        await using var h = await Harness.StartAsync();
        var spaces = h.Hold();
        h.On(API.AccountDetectSite.Name, _ => CloudSiteJson);
        h.On(API.AccountListSpaces.Name, async _ =>
        {
            await spaces.WaitAsync();
            return SpacesJson;
        });
        var (w, rec) = await h.StartWizardAsync();
        await ReachCredentialsAsync(h, w, rec);
        await h.Ui.RunAsync(() =>
        {
            w.SetCredentials("jana@acme.example", "tok-123");
            w.Next();
        });
        Assert.True(w.Busy);
        await spaces.ArrivedAsync();
        await h.Ui.RunAsync(w.Back);
        Assert.Equal("Site", rec.Pages[^1]);
        Assert.False(w.Busy);
        Assert.Equal(["Looking up the Jira site", null, "Loading the spaces", null], rec.Busy);
        spaces.Release();
        await h.IdleAsync();
        Assert.Equal("Site", rec.Pages[^1]); // the late spaces do not push their page
        Assert.Empty(rec.Spaces);
        Assert.Empty(w.Spaces);
        Assert.Equal(4, rec.Busy.Count);
    }

    [Fact]
    public async Task CloseDropsLateReplies()
    {
        await using var h = await Harness.StartAsync();
        var site = h.Hold();
        h.On(API.AccountDetectSite.Name, async _ =>
        {
            await site.WaitAsync();
            return CloudSiteJson;
        });
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetSite("acme.atlassian.net");
            w.Next();
        });
        await site.ArrivedAsync();
        await h.Ui.RunAsync(w.Close);
        Assert.True(w.IsClosed);
        site.Release();
        await h.IdleAsync();
        Assert.Equal(["Site"], rec.Pages);
        Assert.Empty(rec.Detected);
        Assert.Null(w.Site);
        // Nothing starts once closed.
        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Equal([API.AccountDetectSite.Name], h.Fake.Calls);
    }

    [Fact]
    public async Task AConflictStaysOnTheSpaces()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectSite.Name, _ => CloudSiteJson);
        h.On(API.AccountListSpaces.Name, _ => SpacesJson);
        h.On(API.AccountAdd.Name, Fails(ErrorCode.Conflict, "exists"));
        var (w, rec) = await h.StartWizardAsync();
        await ReachSpacesAsync(h, w, rec);
        await h.Ui.RunAsync(() =>
        {
            w.SetSpace("10003", true);
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(6, rec.Busy.Count);
        Assert.Null(rec.Busy[^1]);
        Assert.Equal("Site,Credentials,Spaces", rec.Pages[^1]);
        Assert.Equal((Page.Spaces, "An account for this Jira site already exists"), rec.Banners[^1]);
        Assert.Empty(rec.Done);
    }

    [Fact]
    public async Task ANewOfflineWindowCountsAgain()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectSite.Name, _ => CloudSiteJson);
        var recount = h.Hold();
        h.On(API.AccountListSpaces.Name, async _ =>
        {
            if (h.Params.Count(API.AccountListSpaces.Name) == 1)
            {
                return SpacesJson;
            }
            await recount.WaitAsync();
            return RecountedJson;
        });
        h.On(API.AccountAdd.Name, _ => """{"accountId":"acc-8"}""");
        var (w, rec) = await h.StartWizardAsync();
        await ReachSpacesAsync(h, w, rec);
        await h.Ui.RunAsync(() =>
        {
            w.SetSpace("10002", true);
            w.SetOfflineIndex(9);
            w.SetOfflineIndex(1);
        });
        Assert.Single(rec.Spaces); // no change, no new count

        await h.Ui.RunAsync(() => w.SetOfflineIndex(2));
        Assert.Equal(90, w.OfflineDays);
        Assert.Equal(["", "", ""], rec.Spaces[^1].Select(r => r.Count)); // the old estimates are gone meanwhile
        Assert.Equal("Loading the spaces", rec.Busy[^1]);
        await recount.ArrivedAsync();
        recount.Release();
        await h.IdleAsync();
        Assert.False(w.Busy);
        Assert.Equal("Site,Credentials,Spaces", rec.Pages[^1]);
        Assert.Equal(["about 300 issues", "about 4 issues", "about 0 issues"], rec.Spaces[^1].Select(r => r.Count));
        Assert.Equal("10002", rec.Selected[^1]); // the choice stays
        var recounted = h.Params.Last<AccountListSpacesParams>(API.AccountListSpaces.Name)!;
        Assert.Equal(90, recounted.Config.Jira?.OfflineDays);
        Assert.True(recounted.Counts);

        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Single(rec.Done);
        var sent = h.Params.Last<AccountAddParams>(API.AccountAdd.Name)!;
        Assert.Equal(90, sent.Config.Jira?.OfflineDays);
        ApiJson.AssertSameValue<IReadOnlyList<SpaceRef>>([new SpaceRef { Id = "10002", Key = "WEB", Name = "Website" }], sent.Config.Jira!.Spaces);
        Assert.Null(sent.Config.Jira.OnlyMine);
    }

    [Fact]
    public async Task ADataCenterSiteAsksForTheHiddenAddress()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectSite.Name, _ => DcSiteJson);
        h.On(API.AccountListSpaces.Name, _ => DcSpacesJson);
        h.On(API.AccountAdd.Name, _ => """{"accountId":"acc-9"}""");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetSite("https://jira.acme.example/jira");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal("Site,Credentials", rec.Pages[^1]);
        Assert.Equal(["Found Acme Jira, version 9.12.4"], rec.Detected);
        Assert.Equal(Jira.CredentialFields(JiraDeployment.Datacenter), rec.CredentialPages[^1]);
        Assert.Equal(Field.Token, rec.Focus[^1]);

        // No token: the page asks for one.
        await h.Ui.RunAsync(w.Next);
        Assert.Equal("Token", rec.Problems[^1]);
        Assert.Equal((Page.Credentials, "Enter the personal access token for this account"), rec.Banners[^1]);
        // Data Center has no token page to open.
        await h.Ui.RunAsync(w.OpenTokenHelp);
        await h.IdleAsync();
        Assert.Empty(h.Launcher.Opened);

        await h.Ui.RunAsync(() =>
        {
            w.SetCredentials("", "pat-1");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal("Site,Credentials,Spaces", rec.Pages[^1]);
        var listed = h.Params.Last<AccountListSpacesParams>(API.AccountListSpaces.Name)!;
        Assert.Null(listed.Config.Jira?.Login);
        Assert.Null(listed.Config.Jira?.CloudId);
        Assert.Equal(JiraDeployment.Datacenter, listed.Config.Jira?.Deployment);
        Assert.Equal("pat-1", listed.Credentials.Password);
        Assert.Equal((true, ""), rec.EmailFields[^1]);
        Assert.Equal("20001", rec.Selected[^1]); // a single space is chosen
        Assert.Equal("", rec.SpacesProblems[^1]);

        // The address is needed and must be one.
        await h.Ui.RunAsync(w.Next);
        Assert.Equal("Email", rec.Problems[^1]);
        Assert.Equal(Field.Email, rec.Focus[^1]);
        await h.Ui.RunAsync(() =>
        {
            w.SetEmail("jana");
            w.Next();
        });
        Assert.Equal("Email", rec.Problems[^1]);
        await h.IdleAsync();
        Assert.Equal(0, h.Params.Count(API.AccountAdd.Name));
        await h.Ui.RunAsync(() =>
        {
            w.SetEmail(" jana@acme.example ");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Single(rec.Done);
        var sent = h.Params.Last<AccountAddParams>(API.AccountAdd.Name)!;
        ApiJson.AssertSameValue(
            new AccountConfig
            {
                Name = "Acme Jira",
                Email = "jana@acme.example",
                Kind = AccountKind.Jira,
                Jira = new JiraConfig
                {
                    SiteUrl = "https://jira.acme.example/jira",
                    Deployment = JiraDeployment.Datacenter,
                    Spaces = [new SpaceRef { Id = "20001", Key = "OPS", Name = "Operations" }],
                    OfflineDays = 30,
                },
            },
            sent.Config);
        Assert.Equal("pat-1", sent.Credentials.Password);
    }

    [Fact]
    public async Task EditingReplacesTheTokenOnly()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountListSpaces.Name, _ => """{"user":{"name":"Jana Dvořáková"},"spaces":[],"statuses":[]}""");
        h.On(API.AccountUpdate.Name, _ => "{}");
        var account = CloudAccount();
        var (w, rec) = await h.StartWizardAsync(account);
        Assert.True(w.IsEditing);
        Assert.Equal(["Credentials"], rec.Pages);
        Assert.False(w.CanGoBack);
        Assert.Equal("Edit Account", w.Title);
        Assert.Equal("Save", w.NextLabel(Page.Credentials));
        Assert.False(w.LoginEditable);
        Assert.Equal("jana@acme.example", w.Login);
        Assert.Equal([Jira.CredentialFields(JiraDeployment.Cloud)], rec.CredentialPages);
        Assert.Empty(rec.Banners); // no reason, no banner

        // The token is required; the login cannot change here.
        await h.Ui.RunAsync(w.Next);
        Assert.Equal((Page.Credentials, "Enter the API token for this account"), rec.Banners[^1]);
        Assert.Equal("Token", rec.Problems[^1]);
        await h.Ui.RunAsync(() => w.SetCredentials("other@acme.example", " tok-new "));
        Assert.Equal("jana@acme.example", w.Login);
        await h.Ui.RunAsync(w.OpenTokenHelp);
        await h.IdleAsync();
        Assert.Equal([Jira.TokenHelpUrl], h.Launcher.Opened);
        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Equal(["Saving the account", null], rec.Busy);
        ApiJson.AssertSameValue(
            new AccountUpdateParams { AccountId = "acc-j1", Config = account.Config, Credentials = new Credentials { Password = "tok-new" } },
            h.Params.Last<AccountUpdateParams>(API.AccountUpdate.Name));
        Assert.Equal(["acc-j1"], rec.Done);
        ApiJson.AssertSameValue(account.Config, Assert.Single(rec.DoneConfigs));
        ApiJson.AssertSameValue(
            new AccountListSpacesParams { AccountId = "acc-j1", Config = account.Config, Credentials = new Credentials { Password = "tok-new" } },
            h.Params.Last<AccountListSpacesParams>(API.AccountListSpaces.Name));
        Assert.Equal([API.AccountListSpaces.Name, API.AccountUpdate.Name], h.Fake.Calls); // the token is tried before it is stored
    }

    [Fact]
    public async Task EditingKeepsTheStoredTokenWhenTheNewOneIsRefused()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountListSpaces.Name, Fails(ErrorCode.AuthFailed, "401"));
        var (w, rec) = await h.StartWizardAsync(CloudAccount());
        await h.Ui.RunAsync(() =>
        {
            w.SetCredentials("", "wrong");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(2, rec.Busy.Count);
        Assert.Equal((Page.Credentials, "The Jira site rejected the token"), rec.Banners[^1]);
        Assert.Equal("Token", rec.Problems[^1]);
        Assert.Empty(rec.Done);
        Assert.Equal([API.AccountListSpaces.Name], h.Fake.Calls); // account.update is never sent
    }

    [Fact]
    public async Task EditingAsksForTheTokenWithTheReason()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountListSpaces.Name, Fails(ErrorCode.AuthFailed, "401"));
        var (w, rec) = await h.StartWizardAsync(CloudAccount(), ErrorCode.AuthFailed);
        Assert.Equal([(Page.Credentials, "The Jira site rejected the token")], rec.Banners);
        Assert.Equal(["Token"], rec.Problems);
        Assert.Equal([Field.Token], rec.Focus);
        await h.Ui.RunAsync(() => w.RequestToken(ErrorCode.AuthRequired));
        Assert.Equal((Page.Credentials, "Enter the API token for this account"), rec.Banners[^1]);

        // A refused save stays on the page with the reason.
        await h.Ui.RunAsync(() =>
        {
            w.SetCredentials("", "still-wrong");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(2, rec.Busy.Count);
        Assert.Equal(["Credentials"], rec.Pages);
        Assert.Equal((Page.Credentials, "The Jira site rejected the token"), rec.Banners[^1]);
        Assert.Empty(rec.Done);
    }

    [Fact]
    public async Task RequestTokenIsIgnoredWhenAdding()
    {
        await using var h = await Harness.StartAsync();
        var (_, rec) = await h.StartWizardAsync(requestToken: ErrorCode.AuthFailed);
        Assert.Empty(rec.Banners);
        Assert.Equal(["Site"], rec.Pages);
    }

    // Windows-only: a token page that does not open says why (GTK's
    // openURL toast).
    [Fact]
    public async Task ATokenPageThatDoesNotOpenSaysWhy()
    {
        await using var h = await Harness.StartAsync();
        h.Launcher.Failure = new InvalidOperationException("no browser");
        var (w, rec) = await h.StartWizardAsync(CloudAccount());
        await h.Ui.RunAsync(w.OpenTokenHelp);
        await h.IdleAsync();
        Assert.Equal(["The link could not be opened: no browser"], rec.Toasts);
    }

    // The site of a Jira Cloud found and the credentials page shown.
    private static async Task ReachCredentialsAsync(Harness h, JiraWizardController w, Recorder rec)
    {
        await h.Ui.RunAsync(() =>
        {
            w.SetSite("acme.atlassian.net");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal("Site,Credentials", rec.Pages[^1]);
    }

    // The spaces page reached with valid Jira Cloud credentials.
    private static async Task ReachSpacesAsync(Harness h, JiraWizardController w, Recorder rec)
    {
        await ReachCredentialsAsync(h, w, rec);
        await h.Ui.RunAsync(() =>
        {
            w.SetCredentials("jana@acme.example", "tok-123");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal("Site,Credentials,Spaces", rec.Pages[^1]);
    }

    // A Jira Cloud account as account.list returns it.
    private static Account CloudAccount() => new()
    {
        Id = "acc-j1",
        Config = new AccountConfig
        {
            Name = "Acme",
            Email = "jana@acme.example",
            Kind = AccountKind.Jira,
            Jira = new JiraConfig
            {
                SiteUrl = "https://acme.atlassian.net",
                Deployment = JiraDeployment.Cloud,
                CloudId = CloudId,
                Login = "jana@acme.example",
                Spaces = [new SpaceRef { Id = "10001", Key = "ITSD", Name = "IT Service Desk" }, new SpaceRef { Id = "10002", Key = "WEB" }],
                OfflineDays = 90,
                OnlyMine = true,
                HideEvents = true,
                DisabledFolders = [VirtualFolder.Watching],
                BotNames = ["Issue Sync"],
            },
        },
        Enabled = true,
        State = new SyncState { AccountId = "acc-j1", Status = SyncStatus.AuthRequired },
        Capabilities = [],
    };


    // Collects what the controller reports (the Swift suite's Recorder). A
    // page stack is its pages joined by commas, a set of fields its names
    // in order, a set of spaces its sorted ids.
    private sealed class Recorder
    {
        public List<string> Pages { get; } = [];

        public List<string?> Busy { get; } = [];

        public List<(bool Ok, string Problem)> Checks { get; } = [];

        public List<string> Detected { get; } = [];

        public List<JiraCredentialPage> CredentialPages { get; } = [];

        public List<IReadOnlyList<JiraSpaceRow>> Spaces { get; } = [];

        public List<string> Selected { get; } = [];

        public List<string> SpacesProblems { get; } = [];

        public List<(bool Shown, string Email)> EmailFields { get; } = [];

        public List<(Page Page, string? Text)> Banners { get; } = [];

        public List<string> Problems { get; } = [];

        public List<Field> Focus { get; } = [];

        public List<string> Toasts { get; } = [];

        public List<AccountId> Done { get; } = [];

        public List<AccountConfig> DoneConfigs { get; } = [];

        public void Attach(JiraWizardController w)
        {
            w.PagesChanged += (_, pages) => Pages.Add(string.Join(",", pages));
            w.BusyChanged += (_, text) => Busy.Add(text);
            w.SiteChecked += (_, check) => Checks.Add(check);
            w.DetectedChanged += (_, text) => Detected.Add(text);
            w.CredentialPageChanged += (_, page) => CredentialPages.Add(page);
            w.SpacesChanged += (_, e) =>
            {
                Spaces.Add(e.Rows);
                Selected.Add(string.Join(",", e.Selected.Order(StringComparer.Ordinal)));
            };
            w.SpacesProblemChanged += (_, text) => SpacesProblems.Add(text);
            w.EmailFieldChanged += (_, e) => EmailFields.Add(e);
            w.BannerChanged += (_, e) => Banners.Add(e);
            w.ProblemsShown += (_, fields) => Problems.Add(string.Join(",", fields.Order()));
            w.FocusRequested += (_, field) => Focus.Add(field);
            w.ToastRequested += (_, text) => Toasts.Add(text);
            w.Done += (_, e) =>
            {
                Done.Add(e.Id);
                DoneConfigs.Add(e.Config);
            };
        }
    }

    // The fake daemon (DaemonHarness) and the launcher the assistant is given.
    private sealed class Harness(DaemonHarness daemon) : IAsyncDisposable
    {
        public TestUIContext Ui => daemon.Ui;

        public FakeDaemon Fake => daemon.Fake;

        public ParamsLog Params => daemon.Params;

        public RecordingLauncher Launcher { get; } = new();

        public static async Task<Harness> StartAsync() => new(await DaemonHarness.StartAsync());

        public HeldAnswer Hold() => daemon.Hold();

        public void On(string method, Func<string, string> answer) => daemon.On(method, answer);

        public void On(string method, Func<string, Task<string>> answer) => daemon.On(method, answer);

        public async Task<(JiraWizardController, Recorder)> StartWizardAsync(Account? editing = null, ErrorCode? requestToken = null)
        {
            var client = await daemon.ConnectAsync();
            return await Ui.RunAsync(() =>
            {
                var w = new JiraWizardController(client, Launcher, editing, pending: daemon.Pending);
                daemon.CloseAtEnd(w.Close);
                if (requestToken is { } reason)
                {
                    w.RequestToken(reason);
                }
                var rec = new Recorder();
                rec.Attach(w);
                w.Start();
                return (w, rec);
            });
        }

        public Task IdleAsync() => daemon.IdleAsync();

        public ValueTask DisposeAsync() => daemon.DisposeAsync();
    }
}
