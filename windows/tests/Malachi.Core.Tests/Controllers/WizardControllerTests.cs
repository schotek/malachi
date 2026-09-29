// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/WizardControllerTests.swift: the flow
// of ui/internal/accountwizard (wizard.go, linked.go, oauth.go, trust.go,
// which have no Go test of their own) against a fake daemon.
//
// Swift waits with waitUntil and sleeps; here a test waits until nothing is
// left to happen (IdleAsync), and an answer that Swift delays is held until
// the test releases it (HeldAnswer), so the order is the test's. Swift's
// onOpenURL is the ILauncher the wizard is given (RecordingLauncher). A
// close comes once the call it should outlive has reached the daemon: a
// Close in the same UI turn would keep the call from being sent at all
// (docs/windows-port.md §7.2), where Swift's deferred Task still sends it.
// Added: a launch that fails says why (GTK's launch), a sign-in closed
// before it went out is never sent, and a session that answers after the
// close is let go.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Platform;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Malachi.Core.Wizard;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using OAuthView = Malachi.Core.Controllers.WizardController.OAuthView;
using Page = Malachi.Core.Controllers.WizardController.WizardPage;
using TestingView = Malachi.Core.Controllers.WizardController.TestingView;
using WizardButtons = Malachi.Core.Controllers.WizardController.WizardButtons;
using WizardEndpointRow = Malachi.Core.Controllers.WizardController.EndpointRow;
using WizardOutcome = Malachi.Core.Wizard.Outcome;

namespace Malachi.Core.Tests.Controllers;

public sealed class WizardControllerTests
{
    // One line each: the transport is newline-delimited.
    private const string DiscoveredJson =
        """{"config":{"name":"Example","email":"me@example.com","imap":{"host":"imap.example.com","port":993,"security":"tls","username":"me@example.com","authMethod":"password"},"smtp":{"host":"smtp.example.com","port":587,"security":"starttls","username":"me@example.com","authMethod":"password"}},"source":"ispdb"}""";

    private const string TestOkJson = """{"imap":{"ok":true,"latencyMs":12},"smtp":{"ok":true,"latencyMs":5}}""";
    private const string TestAuthFailedJson = """{"imap":{"ok":false,"error":{"code":1201,"message":"bad"},"latencyMs":0},"smtp":{"ok":true,"latencyMs":5}}""";

    // The browser sign-in (oauth.go): a Google address without GNOME Online
    // Accounts, discovered as the daemon's own sign-in with the app password
    // as the alternative, and a Microsoft 365 one without any.
    private const string GmailOAuthConfigJson =
        """{"name":"me@gmail.com","email":"me@gmail.com","imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},"smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"oauth2"},"oauth2":{"source":"daemon","provider":"google"}}""";

    private const string GmailPasswordConfigJson =
        """{"name":"me@gmail.com","email":"me@gmail.com","imap":{"host":"imap.gmail.com","port":993,"security":"tls","username":"me@gmail.com","authMethod":"password"},"smtp":{"host":"smtp.gmail.com","port":465,"security":"tls","username":"me@gmail.com","authMethod":"password"}}""";

    private const string GmailDiscoveredJson =
        """{"source":"provider","providerName":"Google","config":""" + GmailOAuthConfigJson + ""","alternatives":[""" + GmailPasswordConfigJson + "]}";

    private const string GraphOAuthConfigJson =
        """{"name":"me@contoso.com","email":"me@contoso.com","kind":"graph","graph":{"source":"daemon"},"oauth2":{"source":"daemon","provider":"office365"}}""";

    private const string GraphDiscoveredJson = """{"source":"provider","providerName":"Microsoft 365","config":""" + GraphOAuthConfigJson + "}";
    private const string AuthUrl = "https://accounts.google.com/o/oauth2/v2/auth?state=x";
    private const string OAuthStartJson = """{"sessionId":"s_1","authUrl":"https://accounts.google.com/o/oauth2/v2/auth?state=x","expiresAt":"2026-09-25T10:10:00Z"}""";
    private const string OAuthCompleteJson = """{"status":"complete","config":""" + GmailOAuthConfigJson + "}";
    private const string PendingJson = """{"status":"pending"}""";

    // A server with its own certificate (a mail bridge): account.test refuses
    // it with the certificate in the details (docs/api.md §2).
    private static readonly string CertA = string.Concat(Enumerable.Repeat("ab", 32));
    private static readonly string CertB = string.Concat(Enumerable.Repeat("0f", 32));
    private static readonly string FingerprintA = string.Join(' ', Enumerable.Repeat("ABAB", 16));
    private static readonly string FingerprintB = string.Join(' ', Enumerable.Repeat("0F0F", 16));

    private static readonly OAuthView GooglePrompt = new OAuthView.Prompt(
        "Your browser will open so you can sign in to Google. Malachi Mail never sees your password; it only receives permission to read and send your mail.",
        "Sign In with Google");

    private static readonly OAuthView MicrosoftPrompt = new OAuthView.Prompt(
        "Your browser will open so you can sign in to Microsoft 365. Malachi Mail never sees your password; it only receives permission to read and send your mail.",
        "Sign In with Microsoft 365");

    [Fact]
    public async Task StartsOnTheIdentityPageAndLoadsLinkedAccounts()
    {
        await using var h = await Harness.StartAsync();
        var (w, rec) = await h.StartWizardAsync();
        Assert.Equal([Stack(Page.Identity)], rec.Stacks);
        Assert.Empty(Assert.Single(rec.Linked));
        Assert.Equal([new Identity()], rec.Identities);
        Assert.False(w.IsEditing);
        Assert.Equal("Add Account", w.Title);
        Assert.Equal("Password", w.PasswordTitle);
        Assert.Equal("Next", w.NextLabel);
        Assert.Equal("Add Account", w.AddLabel);
        Assert.Equal("Add Anyway", w.AddAnywayLabel);
        Assert.Equal("Adding Account…", w.ProgressTitle);
        Assert.True(w.EmailEditable && w.PasswordVisible);
    }

    [Fact]
    public async Task InvalidAddressFlagsTheEmailRow()
    {
        await using var h = await Harness.StartAsync();
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "not an address", "pw");
            w.Next();
        });
        Assert.Equal([new IdentityProblems { Email = true }], rec.IdentityProblems);
        Assert.Equal(new string?[] { null }, rec.Banners);
        Assert.Equal([WizardController.IdentityField.Email], rec.Focus);
        Assert.Empty(rec.Busy);
        await h.IdleAsync();
        Assert.Equal([API.AccountLinked.Name], h.Fake.Calls);
        // Typing clears the flag.
        await h.Ui.RunAsync(() => w.SetIdentity("", "me@example.com", "pw"));
        Assert.Equal(new IdentityProblems(), rec.IdentityProblems[^1]);
        Assert.Equal(2, rec.Banners.Count);
    }

    [Fact]
    public async Task DiscoverySuccessFillsTheServersAndStartsTheTest()
    {
        await using var h = await Harness.StartAsync();
        var test = h.Hold();
        h.On(API.AccountDiscover.Name, _ => DiscoveredJson);
        h.On(API.AccountTest.Name, async _ =>
        {
            await test.WaitAsync();
            return TestOkJson;
        });
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", " me@example.com ", "pw");
            w.Next();
        });
        await test.ArrivedAsync();
        await h.Ui.DrainAsync();
        Assert.Equal(Stack(Page.Identity, Page.Servers, Page.Testing), rec.LastStack);
        Assert.Equal([true, false], rec.Busy);
        Assert.Equal([new TestingView.Progress("Testing Connection…")], rec.Testing);
        var cfg = rec.Applied[^1];
        Assert.Equal("imap.example.com", cfg.Imap?.Host);
        Assert.Equal(587, cfg.Smtp?.Port);
        Assert.Equal("me@example.com", cfg.Email);
        Assert.Equal("Me", cfg.DisplayName);
        Assert.Equal("imap.example.com", w.Imap.Host);
        Assert.Equal("Example", w.AccountName);
        Assert.Empty(rec.IdentityProblems);
    }

    [Fact]
    public async Task DiscoveryFailureOpensTheServersPageWithAGuess()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, Fails(ErrorCode.NetworkError, "no route"));
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@example.com", "pw");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.Servers), rec.LastStack);
        Assert.Equal([true, false], rec.Busy);
        var cfg = rec.Applied[^1];
        Assert.Equal("example.com", cfg.Name);
        Assert.Equal("imap.example.com", cfg.Imap?.Host);
        Assert.Equal(993, cfg.Imap?.Port);
        Assert.Equal("smtp.example.com", cfg.Smtp?.Host);
        Assert.Equal(Security.Starttls, cfg.Smtp?.Security);
        Assert.Equal("me@example.com", cfg.Imap?.Username);
        Assert.Equal(587, w.Smtp.Port);
        Assert.Empty(rec.Testing);
        Assert.DoesNotContain(API.AccountTest.Name, h.Fake.Calls);
    }

    [Fact]
    public async Task NothingFoundOpensTheServersPageWithAGuess()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => """{"source":"none"}""");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@example.com", "pw");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.Servers), rec.LastStack);
        Assert.Equal("imap.example.com", rec.Applied[^1].Imap?.Host);
        Assert.Null(rec.Applied[^1].DisplayName);
    }

    [Fact]
    public async Task EmptyPasswordIsAskedForAfterDiscovery()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => DiscoveredJson);
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@example.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal([new IdentityProblems { Password = true }], rec.IdentityProblems);
        Assert.Equal(["Enter the password for this account"], rec.Banners);
        Assert.Equal([WizardController.IdentityField.Password], rec.Focus);
        Assert.Equal([Stack(Page.Identity)], rec.Stacks);
        Assert.Equal([true, false], rec.Busy);
        Assert.Empty(rec.Applied);
        Assert.DoesNotContain(API.AccountTest.Name, h.Fake.Calls);
        // The banner goes away as soon as the password changes.
        await h.Ui.RunAsync(() => w.SetIdentity("Me", "me@example.com", "p"));
        Assert.Equal(new IdentityProblems(), rec.IdentityProblems[^1]);
        Assert.Null(rec.Banners[^1]);
    }

    [Fact]
    public async Task AuthFailureReturnsToTheIdentityPage()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => DiscoveredJson);
        h.On(API.AccountTest.Name, _ => TestAuthFailedJson);
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@example.com", "wrong");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity), rec.LastStack);
        Assert.Equal(new IdentityProblems { Password = true }, rec.IdentityProblems[^1]);
        Assert.Equal("The server rejected the user name or password", rec.Banners[^1]);
        Assert.Equal(WizardController.IdentityField.Password, rec.Focus[^1]);
        var v = rec.LastResults!;
        Assert.Equal("dialog-warning-symbolic", v.Icon);
        Assert.Equal("Connection Failed", v.Title);
        Assert.Null(v.Description);
        Assert.Equal(new WizardEndpointRow("dialog-error-symbolic", "The server rejected the user name or password"), v.Imap);
        Assert.Equal(new WizardEndpointRow("emblem-ok-symbolic", "Connected in 5 ms"), v.Smtp);
        Assert.Null(v.Graph);
        Assert.Equal(new WizardButtons(Edit: true), v.Buttons);
        Assert.Equal(WizardOutcome.AuthFailed, w.LastOutcome);
    }

    [Fact]
    public async Task TestFailureOffersRetryAndAddAnyway()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => DiscoveredJson);
        h.On(API.AccountTest.Name, Fails(ErrorCode.ServerTimeout, "slow"));
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@example.com", "pw");
            w.Next();
        });
        await h.IdleAsync();
        var v = rec.LastResults!;
        Assert.Equal("Connection Failed", v.Title);
        Assert.Equal("dialog-warning-symbolic", v.Imap?.Icon);
        Assert.Equal("Testing the connection failed: the server did not respond in time", v.Imap?.Text);
        Assert.Equal(v.Imap, v.Smtp);
        Assert.Equal(new WizardButtons(Edit: true, Retry: true, AddAnyway: true, Add: false), v.Buttons);
        Assert.Equal(Stack(Page.Identity, Page.Servers, Page.Testing), rec.LastStack);
        Assert.Equal(WizardOutcome.Failed, w.LastOutcome);

        // Edit Servers pops to the Servers page; Test Connection pushes back.
        await h.Ui.RunAsync(w.Edit);
        Assert.Equal(Stack(Page.Identity, Page.Servers), rec.LastStack);
        await h.Ui.RunAsync(w.TestServers);
        Assert.Equal(Stack(Page.Identity, Page.Servers, Page.Testing), rec.LastStack);
        await h.IdleAsync();
        Assert.True(rec.Testing.Count >= 4);
    }

    [Fact]
    public async Task ServerValidationFlagsEmptyRows()
    {
        await using var h = await Harness.StartAsync();
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetServers(
                "x",
                new ServerFields { Host = " ", Port = 993, Security = Security.Tls, Username = "me" },
                new ServerFields { Host = "smtp.example.com", Port = 587, Security = Security.Starttls, Username = "" });
            w.TestServers();
        });
        Assert.Equal([new ServerProblems { ImapHost = true, SmtpUser = true }], rec.ServerProblems);
        Assert.Equal([Stack(Page.Identity)], rec.Stacks);
        Assert.Empty(rec.Testing);
    }

    [Fact]
    public async Task TestSuccessOffersAdd()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => DiscoveredJson);
        h.On(API.AccountTest.Name, _ => TestOkJson);
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@example.com", "pw");
            w.Next();
        });
        await h.IdleAsync();
        var v = rec.LastResults!;
        Assert.Equal("emblem-ok-symbolic", v.Icon);
        Assert.Equal("Ready to Add", v.Title);
        Assert.Equal(new WizardEndpointRow("emblem-ok-symbolic", "Connected in 12 ms"), v.Imap);
        Assert.Equal(new WizardButtons(Edit: true, Retry: false, AddAnyway: false, Add: true), v.Buttons);
        Assert.Equal(WizardOutcome.Ok, w.LastOutcome);
        Assert.Equal(Stack(Page.Identity, Page.Servers, Page.Testing), rec.LastStack);
    }

    [Fact]
    public async Task AddConflictShowsAToastAndKeepsTheResults()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => DiscoveredJson);
        h.On(API.AccountTest.Name, _ => TestOkJson);
        h.On(API.AccountAdd.Name, Fails(ErrorCode.Conflict, "exists"));
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@example.com", "pw");
            w.Next();
        });
        await h.IdleAsync();
        var before = rec.Testing.Count;
        await h.Ui.RunAsync(w.Add);
        Assert.Equal(new TestingView.Progress("Adding Account…"), rec.Testing[before]);
        await h.IdleAsync();
        Assert.Equal(["An account with this e-mail address already exists"], rec.Toasts);
        Assert.Equal(new WizardButtons(Edit: true, Add: true), rec.LastResults?.Buttons);
        Assert.Empty(rec.Done);
    }

    [Fact]
    public async Task AddSuccessReportsDone()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => DiscoveredJson);
        h.On(API.AccountTest.Name, _ => TestOkJson);
        h.On(API.AccountAdd.Name, _ => """{"accountId":"acc-1"}""");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@example.com", "pw");
            w.Next();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(w.Add);
        await h.IdleAsync();
        Assert.Equal([(AccountId)"acc-1"], rec.Done);
        Assert.Equal("me@example.com", rec.DoneConfigs[^1].Email);
        var sent = h.Params.Last<AccountAddParams>(API.AccountAdd.Name)!;
        Assert.Equal("pw", sent.Credentials.Password);
        Assert.Equal("imap.example.com", sent.Config.Imap?.Host);
        Assert.Equal(AuthMethod.Password, sent.Config.Imap?.AuthMethod);
        Assert.Equal("Me", sent.Config.DisplayName);
    }

    [Fact]
    public async Task EditModeStartsOnTheServersPageAndUpdates()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountTest.Name, _ => TestOkJson);
        h.On(API.AccountUpdate.Name, _ => "{}");
        var account = ImapAccount();
        var (w, rec) = await h.StartWizardAsync(editing: account);
        Assert.True(w.IsEditing);
        Assert.Equal([Stack(Page.Identity, Page.Servers)], rec.Stacks);
        Assert.Equal([account.Config], rec.Applied);
        Assert.Equal([new Identity { DisplayName = "Me", Email = "me@example.com", Password = "" }], rec.Identities);
        Assert.Equal("Edit Account", w.Title);
        Assert.Equal("New Password (leave empty to keep)", w.PasswordTitle);
        Assert.Equal("Next", w.NextLabel);
        Assert.Equal("Save", w.AddLabel);
        Assert.Equal("Save Anyway", w.AddAnywayLabel);
        Assert.Equal("Saving Account…", w.ProgressTitle);

        // Back to the identity, Next pushes the Servers page again without discovery.
        await h.Ui.RunAsync(w.Back);
        Assert.Equal(Stack(Page.Identity), rec.LastStack);
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("New Me", "me@example.com", "");
            w.Next();
        });
        Assert.Equal(Stack(Page.Identity, Page.Servers), rec.LastStack);
        Assert.Empty(rec.Busy);

        await h.Ui.RunAsync(() =>
        {
            w.SetServers("Work", Fields("imap.example.com", 993, Security.Tls, "me"), Fields("smtp.example.com", 465, Security.Tls, "me"));
            w.TestServers();
        });
        await h.IdleAsync();
        Assert.Equal("Ready to Save", rec.LastResults?.Title);
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.Equal((AccountId)"acc-9", tested.AccountId);
        Assert.Null(tested.Credentials.Password);

        await h.Ui.RunAsync(w.Add);
        await h.IdleAsync();
        Assert.Equal([(AccountId)"acc-9"], rec.Done);
        var updated = h.Params.Last<AccountUpdateParams>(API.AccountUpdate.Name)!;
        Assert.Equal((AccountId)"acc-9", updated.AccountId);
        Assert.Equal("New Me", updated.Config.DisplayName);
        Assert.Null(updated.Credentials.Password);
        Assert.DoesNotContain(API.AccountDiscover.Name, h.Fake.Calls);
        Assert.DoesNotContain(API.AccountAdd.Name, h.Fake.Calls);
    }

    [Fact]
    public async Task ProviderWithoutSignInShowsTheHintPage()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ =>
            """{"config":{"name":"me@outlook.com","email":"me@outlook.com","kind":"graph","graph":{"source":"goa"}},"source":"provider","providerName":"Microsoft 365"}""");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@outlook.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.Goa), rec.LastStack);
        Assert.Equal(["This address belongs to a Microsoft 365 account. Add it under Settings → Online Accounts, then come back here."], rec.GoaHints);
        Assert.Equal([false], rec.GoaHintBrowser); // no browser sign-in offered
        Assert.Empty(rec.IdentityProblems);
        Assert.Null(w.LinkedCfg);
        Assert.DoesNotContain(API.AccountTest.Name, h.Fake.Calls);
        // Back leads to the identity page.
        await h.Ui.RunAsync(w.Back);
        Assert.Equal(Stack(Page.Identity), rec.LastStack);
    }

    [Fact]
    public async Task LinkedDiscoveryTestsTheGraphAccount()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ =>
            """{"config":{"name":"me@outlook.com","email":"me@outlook.com","kind":"graph","graph":{"source":"goa","goaAccountId":"goa-1"}},"source":"goa"}""");
        h.On(API.AccountTest.Name, _ => """{"graph":{"ok":true,"latencyMs":3}}""");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@outlook.com", "typed");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.Testing), rec.LastStack);
        Assert.Equal("outlook.com", w.LinkedCfg?.Name);
        Assert.Equal("", w.Identity.Password);
        Assert.Equal("", rec.Identities[^1].Password);
        var v = rec.LastResults!;
        Assert.Equal("Ready to Add", v.Title);
        Assert.Equal(new WizardEndpointRow("emblem-ok-symbolic", "Connected in 3 ms"), v.Graph);
        Assert.True(v.Imap is null && v.Smtp is null);
        Assert.Equal(new WizardButtons(Edit: false, Add: true), v.Buttons);
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.Null(tested.Credentials.Password);
        Assert.Equal("Me", tested.Config.DisplayName);
    }

    [Fact]
    public async Task LinkedAuthFailureIsAFailureWithADescription()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ =>
            """{"config":{"name":"x","email":"me@outlook.com","kind":"graph","graph":{"source":"goa","goaAccountId":"goa-1"}},"source":"goa"}""");
        h.On(API.AccountTest.Name, _ => """{"graph":{"ok":false,"error":{"code":1201,"message":"denied"},"latencyMs":0}}""");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@outlook.com", "");
            w.Next();
        });
        await h.IdleAsync();
        var v = rec.LastResults!;
        Assert.Equal("Connection Failed", v.Title);
        Assert.Equal(
            "The server refused the sign-in. Sign in to the account again in Settings → Online Accounts and make sure access to mail is allowed.",
            v.Description);
        Assert.Equal(new WizardButtons(Edit: false, Retry: true, AddAnyway: true, Add: false), v.Buttons);
        Assert.Equal(WizardOutcome.Failed, w.LastOutcome);
        Assert.Equal(Stack(Page.Identity, Page.Testing), rec.LastStack);
        Assert.Empty(rec.IdentityProblems);
    }

    [Fact]
    public async Task ClosedControllerIgnoresLateReplies()
    {
        await using var h = await Harness.StartAsync();
        var discover = h.Hold();
        h.On(API.AccountDiscover.Name, async _ =>
        {
            await discover.WaitAsync();
            return DiscoveredJson;
        });
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@example.com", "pw");
            w.Next();
            Assert.Equal([true], rec.Busy);
        });
        await discover.ArrivedAsync();
        await h.Ui.RunAsync(() =>
        {
            w.Close();
            Assert.True(w.IsClosed);
        });
        discover.Release();
        await h.IdleAsync();
        Assert.Equal([true], rec.Busy);
        Assert.Equal([Stack(Page.Identity)], rec.Stacks);
        Assert.Empty(rec.Applied);
        Assert.Empty(rec.Testing);
        Assert.Contains(API.AccountDiscover.Name, h.Fake.Calls);
    }

    [Fact]
    public async Task StaleDiscoverReplyIsDropped()
    {
        await using var h = await Harness.StartAsync();
        var first = h.Hold();
        var test = h.Hold();
        h.On(API.AccountDiscover.Name, async p =>
        {
            // The first address answers late, the second at once.
            if (p.Contains("first@", StringComparison.Ordinal))
            {
                await first.WaitAsync();
                return """{"source":"none"}""";
            }
            return DiscoveredJson;
        });
        h.On(API.AccountTest.Name, async _ =>
        {
            await test.WaitAsync();
            return TestOkJson;
        });
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "first@example.com", "pw");
            w.Next();
            w.SetIdentity("Me", "me@example.com", "pw");
            w.Next();
        });
        await test.ArrivedAsync();
        await first.ArrivedAsync();
        await h.Ui.DrainAsync();
        Assert.Equal(Stack(Page.Identity, Page.Servers, Page.Testing), rec.LastStack);
        // The late reply arrives and is dropped.
        first.Release();
        test.Release();
        await h.IdleAsync();
        Assert.Single(rec.Applied);
        Assert.Equal("imap.example.com", rec.Applied[^1].Imap?.Host);
        Assert.Equal(Stack(Page.Identity, Page.Servers, Page.Testing), rec.LastStack);
        Assert.Equal([true, true, false], rec.Busy);
    }

    [Fact]
    public async Task RecheckWithoutASignInToasts()
    {
        await using var h = await Harness.StartAsync();
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@outlook.com", "");
            w.RecheckLinked();
        });
        await h.IdleAsync();
        Assert.Equal(["This address is not signed in yet"], rec.Toasts);
        Assert.Equal(2, rec.Linked.Count);
    }

    [Fact]
    public async Task UseLinkedWithoutAConfigToasts()
    {
        await using var h = await Harness.StartAsync();
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() => w.UseLinked(new LinkedAccount
        {
            Provider = LinkedProvider.Google,
            Email = "me@gmail.com",
            GoaAccountId = "g",
            Configured = false,
            AttentionNeeded = false,
        }));
        Assert.Equal(["The mail service does not describe this account; update it and try again"], rec.Toasts);
        Assert.Equal([Stack(Page.Identity)], rec.Stacks);
    }

    [Fact]
    public void SaveErrorTexts()
    {
        Assert.Equal("An account with this e-mail address already exists", WizardController.SaveErrorText(Daemon(ErrorCode.Conflict, "x"), editing: false));
        Assert.Equal("Adding the account failed: the system keyring is unavailable", WizardController.SaveErrorText(Daemon(ErrorCode.KeyringError, "x"), editing: false));
        Assert.Equal("Saving the account failed: the system keyring is unavailable", WizardController.SaveErrorText(Daemon(ErrorCode.KeyringError, "x"), editing: true));
        Assert.Equal("Saving the account needs a running mail backend", WizardController.SaveErrorText(new RpcClientException(ClientError.Disconnected), editing: true));
    }

    // Browser sign-in (oauth.go)

    [Fact]
    public async Task DaemonDiscoveryShowsTheBrowserPrompt()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
        Assert.Equal([GooglePrompt], rec.OAuth);
        Assert.Empty(rec.IdentityProblems); // no password is asked for
        Assert.True(w.OAuth?.Provider == LinkedProvider.Google && w.OAuth?.Name == "Google");
        Assert.Equal(OAuth2Source.Daemon, w.OAuth?.Config?.OAuth2?.Source);
        Assert.Equal(AuthMethod.Password, w.OAuth?.PasswordAlt?.Imap?.AuthMethod);
        Assert.True(w.CanGoBack);
        Assert.DoesNotContain(API.AccountTest.Name, h.Fake.Calls);
    }

    [Fact]
    public async Task BrowserSignInTestsAndAddsWithTheSession()
    {
        await using var h = await Harness.StartAsync();
        var waits = 0;
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, _ => OAuthStartJson);
        // The browser comes back on the second call.
        h.On(API.AccountOAuthWait.Name, _ => Interlocked.Increment(ref waits) == 1 ? PendingJson : OAuthCompleteJson);
        h.On(API.AccountTest.Name, _ => TestOkJson);
        h.On(API.AccountAdd.Name, _ => """{"accountId":"acc-7"}""");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
        await h.Ui.RunAsync(w.SignInWithProvider);
        await h.IdleAsync();

        Assert.Equal([AuthUrl], h.Launcher.Opened);
        Assert.Equal([GooglePrompt, new OAuthView.Waiting(), GooglePrompt], rec.OAuth);
        Assert.Equal(Stack(Page.Identity, Page.OAuth, Page.Testing), rec.LastStack);
        Assert.Equal(2, waits);
        var started = h.Params.Last<AccountOAuthStartParams>(API.AccountOAuthStart.Name)!;
        Assert.Null(started.AccountId);
        Assert.Equal(new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google }, started.Config?.OAuth2);
        Assert.True(started.Config?.DisplayName == "Me" && started.Config?.Name == "gmail.com", "the identity is added at the start");
        Assert.Equal(Malachi.Core.Wizard.OAuth.BrowserPage(), started.BrowserPage);
        Assert.Equal("s_1", h.Params.Last<AccountOAuthWaitParams>(API.AccountOAuthWait.Name)?.SessionId);
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.Equal(new Credentials { OAuthSession = "s_1" }, tested.Credentials);
        Assert.Null(tested.AccountId);
        Assert.True(tested.Config.OAuth2?.Source == OAuth2Source.Daemon && tested.Config.DisplayName == "Me");
        var v = rec.LastResults!;
        Assert.Equal("Ready to Add", v.Title);
        Assert.Equal(new WizardButtons(Edit: false, Add: true), v.Buttons);
        Assert.Equal("Edit Servers", w.EditLabel); // Sign In Again only after a refused sign-in

        await h.Ui.RunAsync(w.Add);
        await h.IdleAsync();
        Assert.Equal([(AccountId)"acc-7"], rec.Done);
        var added = h.Params.Last<AccountAddParams>(API.AccountAdd.Name)!;
        Assert.Equal(new Credentials { OAuthSession = "s_1" }, added.Credentials);
        Assert.Equal(OAuth2Source.Daemon, added.Config.OAuth2?.Source);
        // The daemon consumed the session: closing does not cancel it.
        await h.Ui.RunAsync(w.Close);
        await h.IdleAsync();
        Assert.DoesNotContain(API.AccountOAuthCancel.Name, h.Fake.Calls);
    }

    [Fact]
    public async Task WaitErrorReturnsToThePromptWithTheReason()
    {
        await using var h = await Harness.StartAsync();
        var waits = 0;
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, _ => OAuthStartJson);
        Func<string, string> wait = _ =>
        {
            if (Interlocked.Increment(ref waits) == 1)
            {
                throw Daemon(ErrorCode.AuthFailed, "invalid_grant");
            }
            throw Daemon(ErrorCode.InvalidArgument, "other mailbox", """{"signedInAs":"other@gmail.com"}""");
        };
        h.On(API.AccountOAuthWait.Name, wait);
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
        await h.Ui.RunAsync(w.SignInWithProvider);
        await h.IdleAsync();
        Assert.Equal(["The sign-in with Google was refused"], rec.Toasts);
        Assert.Equal([GooglePrompt, new OAuthView.Waiting(), GooglePrompt], rec.OAuth);
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
        Assert.True(w.OAuth?.SessionId is null && w.CanGoBack);
        Assert.Null(w.LinkedCfg);
        // A session the daemon may still hold is let go.
        Assert.Contains(API.AccountOAuthCancel.Name, h.Fake.Calls);

        // Another mailbox in the browser.
        await h.Ui.RunAsync(w.SignInWithProvider);
        await h.IdleAsync();
        Assert.Equal(2, rec.Toasts.Count);
        Assert.Equal("The browser signed in to other@gmail.com, not to this address", rec.Toasts[^1]);
        Assert.Equal(GooglePrompt, rec.OAuth[^1]);
        Assert.DoesNotContain(API.AccountTest.Name, h.Fake.Calls);
    }

    [Fact]
    public async Task StartErrorStaysOnThePrompt()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, Fails(ErrorCode.Unavailable, "too many"));
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(w.SignInWithProvider);
        await h.IdleAsync();
        Assert.Equal(["Starting the sign-in failed: try again in a moment"], rec.Toasts);
        Assert.Equal([GooglePrompt], rec.OAuth);
        Assert.Empty(h.Launcher.Opened);
        Assert.Equal([true, false], rec.Busy); // only the discovery kept the pages busy
        Assert.Equal([true, false], rec.Starting);
        Assert.DoesNotContain(API.AccountOAuthWait.Name, h.Fake.Calls);
    }

    [Fact]
    public async Task OnlyTheSignInButtonWaitsForTheStart()
    {
        await using var h = await Harness.StartAsync();
        var start = h.Hold();
        var wait = h.Hold();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, async _ =>
        {
            await start.WaitAsync();
            return OAuthStartJson;
        });
        h.On(API.AccountOAuthWait.Name, async _ =>
        {
            await wait.WaitAsync();
            return PendingJson;
        });
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
        Assert.Equal([true, false], rec.Busy);
        await h.Ui.RunAsync(() =>
        {
            w.SignInWithProvider();
            Assert.True(w.OAuthStarting && !w.Busy);
            Assert.Equal([true], rec.Starting);
            Assert.Equal([true, false], rec.Busy); // the identity and server pages stay usable
            Assert.True(w.CanGoBack, "Back stays while the sign-in starts");
            // A second click while it starts is ignored.
            w.SignInWithProvider();
        });
        start.Release();
        await wait.ArrivedAsync();
        await h.Ui.DrainAsync();
        Assert.Equal(new OAuthView.Waiting(), rec.OAuth[^1]);
        Assert.Equal([true, false], rec.Starting);
        Assert.False(w.OAuthStarting);
        Assert.Single(h.Fake.Calls, API.AccountOAuthStart.Name);
        await h.Ui.RunAsync(w.Close);
    }

    [Fact]
    public async Task BackDuringTheStartDropsItsAnswer()
    {
        await using var h = await Harness.StartAsync();
        var starts = 0;
        var firstStart = h.Hold();
        var secondStart = h.Hold();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, async _ =>
        {
            // The first start fails, the second opens a session.
            if (Interlocked.Increment(ref starts) == 1)
            {
                await firstStart.WaitAsync();
                throw Daemon(ErrorCode.OAuthClientMissing, "no client");
            }
            await secondStart.WaitAsync();
            return OAuthStartJson;
        });
        h.On(API.AccountOAuthCancel.Name, _ => "{}");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);

        // A failure after Back: neither the notice nor a toast.
        await h.Ui.RunAsync(() =>
        {
            w.SignInWithProvider();
            w.Back();
            Assert.Equal(Stack(Page.Identity), rec.LastStack);
        });
        firstStart.Release();
        await h.IdleAsync();
        Assert.Equal([true, false], rec.Starting);
        Assert.Equal([GooglePrompt], rec.OAuth); // the page does not switch to the missing client
        Assert.Empty(rec.Toasts);
        Assert.Equal(Stack(Page.Identity), rec.LastStack);

        // A session opened after Back is let go, never opened or waited on.
        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Equal(2, rec.OAuth.Count);
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
        await h.Ui.RunAsync(() =>
        {
            w.SignInWithProvider();
            w.Back();
        });
        secondStart.Release();
        await h.IdleAsync();
        Assert.Equal("s_1", h.Params.Last<AccountOAuthCancelParams>(API.AccountOAuthCancel.Name)?.SessionId);
        Assert.Equal([true, false, true, false], rec.Starting);
        Assert.Equal([GooglePrompt, GooglePrompt], rec.OAuth);
        Assert.True(h.Launcher.Opened.Count == 0 && rec.Toasts.Count == 0);
        Assert.Equal(Stack(Page.Identity), rec.LastStack);
        Assert.Null(w.OAuth?.SessionId);
        Assert.Equal([true, false, true, false], rec.Busy); // only the discoveries kept the pages busy
        Assert.DoesNotContain(API.AccountOAuthWait.Name, h.Fake.Calls);
    }

    [Fact]
    public async Task CancelWhileWaitingCancelsTheSession()
    {
        await using var h = await Harness.StartAsync();
        var wait = h.Hold();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, _ => OAuthStartJson);
        h.On(API.AccountOAuthWait.Name, async _ =>
        {
            await wait.WaitAsync();
            return PendingJson;
        });
        h.On(API.AccountOAuthCancel.Name, _ => "{}");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(w.SignInWithProvider);
        await wait.ArrivedAsync();
        await h.Ui.DrainAsync();
        Assert.Equal(new OAuthView.Waiting(), rec.OAuth[^1]);
        await h.Ui.RunAsync(() =>
        {
            Assert.False(w.CanGoBack, "no Back while the browser is out");
            w.Back();
            Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
            w.ReopenBrowser();
        });
        await h.Ui.DrainAsync();
        Assert.Equal([AuthUrl, AuthUrl], h.Launcher.Opened);

        await h.Ui.RunAsync(() =>
        {
            w.CancelOAuth();
            Assert.Equal(GooglePrompt, rec.OAuth[^1]);
            Assert.True(w.OAuth?.SessionId is null && w.CanGoBack);
        });
        // The pending wait answers and is dropped: nothing asks again.
        wait.Release();
        await h.IdleAsync();
        Assert.Equal("s_1", h.Params.Last<AccountOAuthCancelParams>(API.AccountOAuthCancel.Name)?.SessionId);
        Assert.Empty(rec.Toasts); // the user's own cancel says nothing
        Assert.Single(h.Fake.Calls, API.AccountOAuthWait.Name);
        // Reopening is for the wait only.
        await h.Ui.RunAsync(w.ReopenBrowser);
        await h.IdleAsync();
        Assert.Equal(2, h.Launcher.Opened.Count);
    }

    [Fact]
    public async Task ClosingWhileWaitingCancelsTheSession()
    {
        await using var h = await Harness.StartAsync();
        var wait = h.Hold();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, _ => OAuthStartJson);
        h.On(API.AccountOAuthWait.Name, async _ =>
        {
            await wait.WaitAsync();
            return PendingJson;
        });
        h.On(API.AccountOAuthCancel.Name, _ => "{}");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(w.SignInWithProvider);
        await wait.ArrivedAsync();
        await h.Ui.DrainAsync();
        Assert.Equal(new OAuthView.Waiting(), rec.OAuth[^1]);
        await h.Ui.RunAsync(w.Close);
        wait.Release();
        await h.IdleAsync();
        Assert.Contains(API.AccountOAuthCancel.Name, h.Fake.Calls);
        Assert.True(w.IsClosed);
    }

    [Fact]
    public async Task MissingClientOffersTheAppPasswordForGoogle()
    {
        await using var h = await Harness.StartAsync();
        var test = h.Hold();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, Fails(ErrorCode.OAuthClientMissing, "no client"));
        h.On(API.AccountTest.Name, async _ =>
        {
            await test.WaitAsync();
            return TestOkJson;
        });
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@gmail.com", "app-pw");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
        await h.Ui.RunAsync(w.SignInWithProvider);
        await h.IdleAsync();
        Assert.Equal(2, rec.OAuth.Count);
        Assert.Equal(
            new OAuthView.Unavailable(
                "No OAuth client is configured for Google on this computer. Add a client ID to the mail backend's configuration and try again.",
                PasswordAlternative: true),
            rec.OAuth[^1]);
        Assert.Empty(rec.Toasts);

        await h.Ui.RunAsync(w.UseAppPassword);
        Assert.Equal(Stack(Page.Identity, Page.Servers, Page.Testing), rec.LastStack);
        Assert.True(w.LinkedCfg is null && w.OAuth is null);
        var cfg = rec.Applied[^1];
        Assert.True(cfg.Imap?.Host == "imap.gmail.com" && cfg.Imap?.AuthMethod == AuthMethod.Password);
        Assert.True(cfg.Smtp?.Port == 465 && cfg.DisplayName == "Me");
        Assert.Equal("imap.gmail.com", w.Imap.Host);
        await test.ArrivedAsync();
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.Equal(new Credentials { Password = "app-pw" }, tested.Credentials);
        Assert.True(tested.Config.Imap?.AuthMethod == AuthMethod.Password && tested.Config.OAuth2 is null);
        Assert.Equal("Edit Servers", w.EditLabel);
    }

    [Fact]
    public async Task AppPasswordWithoutAPasswordAsksForItAndSkipsDiscovery()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, Fails(ErrorCode.OAuthClientMissing, "no client"));
        h.On(API.AccountTest.Name, _ => TestOkJson);
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(w.SignInWithProvider);
        await h.IdleAsync();
        Assert.Equal(2, rec.OAuth.Count);
        await h.Ui.RunAsync(w.UseAppPassword);
        Assert.Equal(Stack(Page.Identity), rec.LastStack);
        Assert.Equal(new IdentityProblems { Password = true }, rec.IdentityProblems[^1]);
        Assert.Equal("Enter the app password for this account", rec.Banners[^1]);
        Assert.Equal(WizardController.IdentityField.Password, rec.Focus[^1]);

        // Next with the password goes the password way without asking again.
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "app-pw");
            w.Next();
        });
        Assert.Equal(Stack(Page.Identity, Page.Servers, Page.Testing), rec.LastStack);
        await h.IdleAsync();
        Assert.Single(h.Fake.Calls, API.AccountDiscover.Name);
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.Equal(new Credentials { Password = "app-pw" }, tested.Credentials);
        Assert.Equal("imap.gmail.com", tested.Config.Imap?.Host);
        Assert.Equal("Ready to Add", rec.LastResults?.Title);

        // Another address forgets the choice and discovers again.
        await h.Ui.RunAsync(() =>
        {
            w.Back();
            w.Back();
            w.SetIdentity("", "you@gmail.com", "pw");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(2, h.Fake.Calls.Count(c => c == API.AccountDiscover.Name));
    }

    [Fact]
    public async Task MissingClientForMicrosoftOffersNoPassword()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => GraphDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, Fails(ErrorCode.OAuthClientMissing, "no client"));
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@contoso.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
        Assert.Equal([MicrosoftPrompt], rec.OAuth);
        await h.Ui.RunAsync(w.SignInWithProvider);
        await h.IdleAsync();
        Assert.Equal(
            new OAuthView.Unavailable(
                "No OAuth client is configured for Microsoft 365 on this computer. Add a client ID to the mail backend's configuration and try again.",
                PasswordAlternative: false),
            rec.OAuth[^1]);
        await h.Ui.RunAsync(w.UseAppPassword);
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack); // nothing to fall back to
        Assert.True(w.CanGoBack);
        await h.Ui.RunAsync(w.Back);
        Assert.Equal(Stack(Page.Identity), rec.LastStack);
    }

    [Fact]
    public async Task GoaHintOffersTheBrowser()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ =>
            """{"config":{"name":"me@contoso.com","email":"me@contoso.com","kind":"graph","graph":{"source":"goa"}},"source":"provider","providerName":"Microsoft 365","alternatives":[""" + GraphOAuthConfigJson + "]}");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@contoso.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.Goa), rec.LastStack);
        Assert.Equal([true], rec.GoaHintBrowser);
        await h.Ui.RunAsync(w.UseBrowser);
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
        Assert.Equal([MicrosoftPrompt], rec.OAuth);
        Assert.Equal(GraphSource.Daemon, w.OAuth?.Config?.Graph?.Source);
        Assert.Null(w.OAuth?.PasswordAlt);
    }

    [Fact]
    public async Task SignInModeStartsOnTheBrowserAndUpdates()
    {
        await using var h = await Harness.StartAsync();
        var account = OAuthAccount();
        h.On(API.AccountOAuthStart.Name, _ => OAuthStartJson);
        h.On(API.AccountOAuthWait.Name, _ => OAuthCompleteJson);
        h.On(API.AccountTest.Name, _ => TestOkJson);
        h.On(API.AccountUpdate.Name, _ => "{}");
        var (w, rec) = await h.StartWizardAsync(editing: account, signIn: true);
        Assert.True(w.SignInMode && w.IsEditing);
        Assert.Equal("Sign In", w.Title);
        Assert.True(w.OAuth?.Config is null && w.OAuth?.Name == "Google");
        Assert.Equal([Stack(Page.OAuth)], rec.Stacks);
        Assert.Equal([GooglePrompt], rec.OAuth);
        Assert.False(w.CanGoBack);
        Assert.True(!w.EmailEditable && !w.PasswordVisible);

        await h.Ui.RunAsync(w.SignInWithProvider);
        await h.IdleAsync();
        Assert.Equal(Stack(Page.OAuth, Page.Testing), rec.LastStack);
        Assert.True(w.CanGoBack);
        var started = h.Params.Last<AccountOAuthStartParams>(API.AccountOAuthStart.Name)!;
        Assert.True(started.AccountId == "acc-9" && started.Config is null);
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.Equal((AccountId)"acc-9", tested.AccountId);
        Assert.Equal(new Credentials { OAuthSession = "s_1" }, tested.Credentials);
        Assert.Equal("Ready to Save", rec.LastResults?.Title);

        await h.Ui.RunAsync(w.Add);
        await h.IdleAsync();
        Assert.Equal([(AccountId)"acc-9"], rec.Done);
        var updated = h.Params.Last<AccountUpdateParams>(API.AccountUpdate.Name)!;
        Assert.Equal((AccountId)"acc-9", updated.AccountId);
        Assert.Equal(new Credentials { OAuthSession = "s_1" }, updated.Credentials);
        Assert.Equal("Me", updated.Config.DisplayName);
        Assert.DoesNotContain(API.AccountAdd.Name, h.Fake.Calls);
        Assert.DoesNotContain(API.AccountDiscover.Name, h.Fake.Calls);
    }

    [Fact]
    public async Task SignInIsIgnoredForAPasswordAccount()
    {
        await using var h = await Harness.StartAsync();
        var (w, rec) = await h.StartWizardAsync(editing: ImapAccount(), signIn: true);
        Assert.False(w.SignInMode);
        Assert.Equal([Stack(Page.Identity, Page.Servers)], rec.Stacks);
        Assert.Empty(rec.OAuth);
    }

    [Fact]
    public async Task EditingABrowserAccountOffersSignInAgain()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountTest.Name, _ =>
            """{"imap":{"ok":false,"error":{"code":1200,"message":"sign in"},"latencyMs":0},"smtp":{"ok":false,"error":{"code":1200,"message":"sign in"},"latencyMs":0}}""");
        var (w, rec) = await h.StartWizardAsync(editing: OAuthAccount());
        Assert.False(w.SignInMode);
        Assert.Equal([Stack(Page.Identity)], rec.Stacks);
        Assert.Equal("Test Connection", w.NextLabel);
        Assert.True(!w.EmailEditable && !w.PasswordVisible);
        Assert.Equal("Edit Account", w.Title);
        Assert.Equal("Edit Servers", w.EditLabel);

        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity, Page.Testing), rec.LastStack);
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.Equal((AccountId)"acc-9", tested.AccountId);
        Assert.Equal(new Credentials(), tested.Credentials); // the stored sign-in is tested
        var v = rec.LastResults!;
        Assert.Equal("Connection Failed", v.Title);
        Assert.Equal("The server refused the sign-in. Sign in again and make sure access to mail is allowed.", v.Description);
        Assert.Equal(new WizardButtons(Edit: true, Retry: true, AddAnyway: true), v.Buttons);
        Assert.Equal("Sign In Again", w.EditLabel);
        Assert.Equal(WizardOutcome.Failed, w.LastOutcome);
        Assert.Empty(rec.IdentityProblems);

        // Sign In Again: the prompt, then a sign-in by the account's id.
        await h.Ui.RunAsync(w.Edit);
        Assert.Equal(Stack(Page.Identity, Page.OAuth), rec.LastStack);
        Assert.Equal([GooglePrompt], rec.OAuth);
        Assert.True(w.OAuth?.Config is null && w.OAuth?.Provider == LinkedProvider.Google);
    }

    [Fact]
    public async Task AStartWithoutAnHttpsAddressIsNotOpened()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, _ => """{"sessionId":"s_1","authUrl":"http://accounts.google.com/x","expiresAt":"2026-09-25T10:10:00Z"}""");
        h.On(API.AccountOAuthCancel.Name, _ => "{}");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(w.SignInWithProvider);
        await h.IdleAsync();
        Assert.Equal(["The link could not be opened: not an https address"], rec.Toasts);
        Assert.Empty(h.Launcher.Opened);
        Assert.Equal([GooglePrompt], rec.OAuth); // the prompt stays: nothing could come back from the browser
        Assert.Equal([true, false], rec.Starting);
        Assert.True(w.OAuth?.SessionId is null && w.OAuth?.AuthUrl is null && w.CanGoBack);
        // The session is let go, and nothing waits on it.
        Assert.Equal("s_1", h.Params.Last<AccountOAuthCancelParams>(API.AccountOAuthCancel.Name)?.SessionId);
        Assert.DoesNotContain(API.AccountOAuthWait.Name, h.Fake.Calls);
    }

    [Fact]
    public async Task BrowserAccountWithoutASignInProblemHidesEdit()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountTest.Name, _ =>
            """{"imap":{"ok":false,"error":{"code":1301,"message":"no route"},"latencyMs":0},"smtp":{"ok":true,"latencyMs":5}}""");
        var (w, rec) = await h.StartWizardAsync(editing: OAuthAccount(status: SyncStatus.Idle));
        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        var v = rec.LastResults!;
        Assert.Null(v.Description);
        Assert.Equal(new WizardButtons(Edit: false, Retry: true, AddAnyway: true), v.Buttons);
    }

    // Trusting a certificate (trust.go)

    [Fact]
    public async Task OneConfirmationTrustsTheSameCertificateOnBothEndpoints()
    {
        var (h, w, rec) = await StartRefusedAsync(TestRefusedJson(CertA, CertA));
        await using var _ = h;
        var v = rec.LastResults!;
        Assert.Equal("Connection Failed", v.Title);
        Assert.Equal(new WizardEndpointRow("dialog-error-symbolic", "The server's certificate is not from a trusted authority", Trust: true), v.Imap);
        Assert.True(v.Smtp?.Trust);
        Assert.Equal(new WizardButtons(Edit: true, Retry: true, AddAnyway: true), v.Buttons);
        Assert.True(w.PinnedFingerprints.Imap.Length == 0 && w.PinnedFingerprints.Smtp.Length == 0);
        Assert.Equal([("", "")], rec.Pins);

        await h.Ui.RunAsync(() => w.TrustCertificate(Endpoint.Smtp));
        await h.IdleAsync();
        Assert.Equal("Ready to Add", rec.LastResults?.Title);
        var prompt = Assert.Single(rec.Prompts);
        Assert.True(prompt.Heading == "Trust This Certificate?" && prompt.ConfirmLabel == "_Trust");
        Assert.Contains("for imap.example.com:993 and smtp.example.com:587 and no other", prompt.Body, StringComparison.Ordinal); // IMAP first
        Assert.Equal(new CertificateDetail("SHA-256 fingerprint", FingerprintA, Monospaced: true), prompt.Details[0]);
        Assert.Equal(new CertificateDetail("Issued to", "127.0.0.1"), prompt.Details[1]);
        Assert.DoesNotContain(prompt.Details, d => d.Label == "Previously trusted");
        Assert.Equal((FingerprintA, FingerprintA), rec.Pins[^1]);
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.True(tested.Config.Imap?.CertificateSha256 == CertA && tested.Config.Smtp?.CertificateSha256 == CertA);
        Assert.Equal("pw", tested.Credentials.Password);

        await h.Ui.RunAsync(w.Add);
        await h.IdleAsync();
        var added = h.Params.Last<AccountAddParams>(API.AccountAdd.Name)!;
        Assert.True(added.Config.Imap?.CertificateSha256 == CertA && added.Config.Smtp?.CertificateSha256 == CertA);
    }

    [Fact]
    public async Task DifferentCertificatesTrustOnlyTheClickedEndpoint()
    {
        var (h, w, rec) = await StartRefusedAsync(TestRefusedJson(CertA, CertB));
        await using var _ = h;
        Assert.True(rec.LastResults?.Imap?.Trust == true && rec.LastResults?.Smtp?.Trust == true);
        await h.Ui.RunAsync(() => w.TrustCertificate(Endpoint.Smtp));
        await h.IdleAsync();
        Assert.Equal("Ready to Add", rec.LastResults?.Title);
        var prompt = rec.Prompts[^1];
        Assert.Contains("for smtp.example.com:587 and no other", prompt.Body, StringComparison.Ordinal);
        Assert.Equal(FingerprintB, prompt.Details[0].Value);
        Assert.Equal(("", FingerprintB), rec.Pins[^1]);
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.True(tested.Config.Imap?.CertificateSha256 is null && tested.Config.Smtp?.CertificateSha256 == CertB);
    }

    [Fact]
    public async Task DeclinedConfirmationChangesNothing()
    {
        var (h, w, rec) = await StartRefusedAsync(TestRefusedJson(CertA, null));
        await using var _ = h;
        Assert.True(rec.LastResults?.Imap?.Trust);
        Assert.Equal(new WizardEndpointRow("emblem-ok-symbolic", "Connected in 5 ms"), rec.LastResults?.Smtp);
        await h.Ui.RunAsync(() => rec.TrustAnswer = false);
        var testing = rec.Testing.Count;
        await h.Ui.RunAsync(() => w.TrustCertificate(Endpoint.Imap));
        await h.IdleAsync();
        Assert.Single(rec.Prompts);
        Assert.Equal(testing, rec.Testing.Count); // no new test
        Assert.Equal([("", "")], rec.Pins);
        Assert.Empty(w.Imap.CertificateSha256);
        Assert.Single(h.Fake.Calls, API.AccountTest.Name);
        // A row without an offer asks nothing.
        await h.Ui.RunAsync(() => w.TrustCertificate(Endpoint.Smtp));
        await h.IdleAsync();
        Assert.Single(rec.Prompts);
    }

    [Fact]
    public async Task ConnectionFailuresOfferNoTrust()
    {
        var handshake = """{"imap":{"ok":false,"latencyMs":1,"error":{"code":1303,"message":"eof","data":{"reason":"handshake"}}},"smtp":"""
            + RefusedJson(CertA, reason: "starttlsUnavailable") + "}";
        var (h, w, rec) = await StartRefusedAsync(handshake);
        await using var _ = h;
        Assert.Equal(new WizardEndpointRow("dialog-error-symbolic", "The secure connection could not be established"), rec.LastResults?.Imap);
        Assert.Equal(new WizardEndpointRow("dialog-error-symbolic", "The server does not offer STARTTLS"), rec.LastResults?.Smtp);
        await h.Ui.RunAsync(() =>
        {
            w.TrustCertificate(Endpoint.Imap);
            w.TrustCertificate(Endpoint.Smtp);
        });
        await h.IdleAsync();
        Assert.Empty(rec.Prompts);
    }

    [Fact]
    public async Task AChangedCertificateOffersTheNewOne()
    {
        await using var h = await Harness.StartAsync();
        var count = 0;
        var changed = """{"imap":""" + RefusedJson(CertB, reason: "pinMismatch", expected: CertA) + ""","smtp":{"ok":true,"latencyMs":5}}""";
        h.On(API.AccountTest.Name, _ => Interlocked.Increment(ref count) == 1 ? changed : TestOkJson);
        var (w, rec) = await h.StartWizardAsync(editing: PinnedAccount());
        await h.Ui.RunAsync(w.TestServers);
        await h.IdleAsync();
        Assert.Equal("The server presented a different certificate than the one you trust", rec.LastResults?.Imap?.Text);
        Assert.True(rec.LastResults?.Imap?.Trust);
        await h.Ui.RunAsync(() => w.TrustCertificate(Endpoint.Imap));
        await h.IdleAsync();
        Assert.Equal("Ready to Save", rec.LastResults?.Title);
        // Its own warning, with the fingerprint trusted before.
        var prompt = rec.Prompts[0];
        Assert.True(prompt.Heading == "Trust the New Certificate?" && prompt.ConfirmLabel == "_Trust");
        Assert.StartsWith("The certificate of imap.example.com:993 has changed since you trusted it.", prompt.Body, StringComparison.Ordinal);
        Assert.Equal(
            [
                new CertificateDetail("SHA-256 fingerprint", FingerprintB, Monospaced: true),
                new CertificateDetail("Previously trusted", FingerprintA, Monospaced: true),
            ],
            prompt.Details.Take(2));
        Assert.Equal((FingerprintB, ""), rec.Pins[^1]);
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.True(tested.Config.Imap?.CertificateSha256 == CertB && tested.AccountId == "acc-9");
    }

    [Fact]
    public async Task EditingKeepsThePinWhileTheServerStays()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountTest.Name, _ => TestOkJson);
        h.On(API.AccountUpdate.Name, _ => "{}");
        var (w, rec) = await h.StartWizardAsync(editing: PinnedAccount());
        Assert.Equal([(FingerprintA, "")], rec.Pins);
        Assert.Equal(CertA, w.Imap.CertificateSha256);

        // The Servers page reads its rows without the pin: another user name
        // and the host in another case keep it.
        await h.Ui.RunAsync(() => w.SetServers("Work", Fields(" IMAP.example.com", 993, Security.Tls, "me2"), Fields("smtp.example.com", 465, Security.Tls, "me")));
        Assert.True(w.Imap.CertificateSha256 == CertA && rec.Pins.Count == 1);
        await h.Ui.RunAsync(w.TestServers);
        await h.IdleAsync();
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.True(tested.Config.Imap?.CertificateSha256 == CertA && tested.Config.Smtp?.CertificateSha256 is null);

        // Another host drops it; the host it was trusted for restores it.
        await h.Ui.RunAsync(() => w.SetServers("Work", Fields("imap2.example.com", 993, Security.Tls, "me"), Fields("smtp.example.com", 465, Security.Tls, "me")));
        Assert.True(w.Imap.CertificateSha256.Length == 0 && rec.Pins[^1] == ("", ""));
        await h.Ui.RunAsync(() => w.SetServers("Work", Fields("imap.example.com", 143, Security.Starttls, "me"), Fields("smtp.example.com", 465, Security.Tls, "me")));
        Assert.Empty(w.Imap.CertificateSha256); // another port
        await h.Ui.RunAsync(() => w.SetServers("Work", Fields("imap.example.com", 993, Security.None, "me"), Fields("smtp.example.com", 465, Security.Tls, "me")));
        Assert.Empty(w.Imap.CertificateSha256); // no pin without TLS
        await h.Ui.RunAsync(() => w.SetServers("Work", Fields("imap.example.com", 993, Security.Tls, "me"), Fields("smtp.example.com", 465, Security.Tls, "me")));
        Assert.True(w.Imap.CertificateSha256 == CertA && rec.Pins[^1] == (FingerprintA, ""));

        await h.Ui.RunAsync(w.Add);
        await h.IdleAsync();
        var updated = h.Params.Last<AccountUpdateParams>(API.AccountUpdate.Name)!;
        Assert.True(updated.Config.Imap?.CertificateSha256 == CertA && updated.Config.Smtp?.CertificateSha256 is null);
        Assert.Equal(CertA, rec.DoneConfigs[^1].Imap?.CertificateSha256);
    }

    [Fact]
    public async Task ForgetDropsThePin()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountTest.Name, _ => TestOkJson);
        h.On(API.AccountUpdate.Name, _ => "{}");
        var (w, rec) = await h.StartWizardAsync(editing: PinnedAccount());
        await h.Ui.RunAsync(() => w.ForgetPin(Endpoint.Imap));
        Assert.True(rec.Pins[^1] == ("", "") && w.Imap.CertificateSha256.Length == 0);
        // For good: the same server does not bring it back.
        await h.Ui.RunAsync(() => w.SetServers("Work", Fields("imap.example.com", 993, Security.Tls, "me"), Fields("smtp.example.com", 465, Security.Tls, "me")));
        Assert.Empty(w.Imap.CertificateSha256);
        await h.Ui.RunAsync(w.TestServers);
        await h.IdleAsync();
        var tested = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.Null(tested.Config.Imap?.CertificateSha256);
        await h.Ui.RunAsync(w.Add);
        await h.IdleAsync();
        var updated = h.Params.Last<AccountUpdateParams>(API.AccountUpdate.Name)!;
        Assert.Null(updated.Config.Imap?.CertificateSha256);
    }

    [Fact]
    public async Task ABrowserAccountIsNeverOfferedTrust()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountTest.Name, _ => TestRefusedJson(CertA, CertA));
        var (w, rec) = await h.StartWizardAsync(editing: OAuthAccount(status: SyncStatus.Idle));
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.True(rec.LastResults?.Imap?.Trust == false && rec.LastResults?.Smtp?.Trust == false);
        await h.Ui.RunAsync(() => w.TrustCertificate(Endpoint.Imap));
        await h.IdleAsync();
        Assert.Empty(rec.Prompts);
    }

    // A missing or refused password (wizard.go RequestPassword)

    [Fact]
    public async Task AMissingPasswordReturnsToTheIdentity()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountTest.Name, Fails(ErrorCode.AuthRequired, "no stored password"));
        var (w, rec) = await h.StartWizardAsync(editing: ImapAccount());
        await h.Ui.RunAsync(w.TestServers);
        await h.IdleAsync();
        Assert.Equal(Stack(Page.Identity), rec.LastStack);
        Assert.Equal(new IdentityProblems { Password = true }, rec.IdentityProblems[^1]);
        Assert.Equal("No password is stored for this account. Enter it to continue.", rec.Banners[^1]);
        Assert.Equal(WizardController.IdentityField.Password, rec.Focus[^1]);
        var v = rec.LastResults!;
        Assert.Equal("Connection Failed", v.Title);
        Assert.True(v.Imap?.Text == "Testing the connection failed: sign-in required" && v.Smtp == v.Imap);
        // No Save Anyway: it would store the account without a password again.
        Assert.Equal(new WizardButtons(Edit: true), v.Buttons);
        Assert.Equal(WizardOutcome.AuthFailed, w.LastOutcome);
        // Typing the password takes the banner away.
        await h.Ui.RunAsync(() => w.SetIdentity("Me", "me@example.com", "p"));
        Assert.Null(rec.Banners[^1]);
    }

    [Fact]
    public async Task ABrowserAccountWithoutASignInIsNotAskedForAPassword()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountTest.Name, Fails(ErrorCode.AuthRequired, "sign in"));
        var (w, rec) = await h.StartWizardAsync(editing: OAuthAccount());
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Empty(rec.IdentityProblems);
        Assert.Equal("The server refused the sign-in. Sign in again and make sure access to mail is allowed.", rec.LastResults?.Description);
        Assert.Equal(WizardOutcome.Failed, w.LastOutcome);
    }

    [Fact]
    public async Task RequestPasswordOpensOnTheIdentity()
    {
        await using var h = await Harness.StartAsync();
        var (w, rec) = await h.StartWizardAsync(editing: ImapAccount(), requestPassword: ErrorCode.AuthRequired);
        Assert.Equal([Page.Identity], w.Pages);
        Assert.Equal([Stack(Page.Identity)], rec.Stacks);
        Assert.Equal([new IdentityProblems { Password = true }], rec.IdentityProblems);
        Assert.Equal(["No password is stored for this account. Enter it to continue."], rec.Banners);
        Assert.Equal([WizardController.IdentityField.Password], rec.Focus);
        Assert.Equal("Edit Account", w.Title);
        // The password typed, Next goes on to the servers as when editing.
        await h.Ui.RunAsync(() => w.SetIdentity("Me", "me@example.com", "secret"));
        Assert.Null(rec.Banners[^1]);
        await h.Ui.RunAsync(w.Next);
        Assert.Equal(Stack(Page.Identity, Page.Servers), rec.LastStack);

        var (_, refused) = await h.StartWizardAsync(editing: ImapAccount(), requestPassword: ErrorCode.AuthFailed);
        Assert.Equal(["The server rejected the user name or password"], refused.Banners);
        Assert.Equal([WizardController.IdentityField.Password], refused.Focus);
    }

    [Fact]
    public async Task RequestPasswordIsIgnoredWithoutAPasswordAccount()
    {
        await using var h = await Harness.StartAsync();
        var (browser, rec) = await h.StartWizardAsync(editing: OAuthAccount(), requestPassword: ErrorCode.AuthRequired);
        Assert.True(rec.IdentityProblems.Count == 0 && rec.Focus.Count == 0);
        Assert.Equal([Page.Identity], browser.Pages);
        var (added, addRec) = await h.StartWizardAsync(requestPassword: ErrorCode.AuthRequired);
        Assert.Empty(addRec.IdentityProblems);
        Assert.Equal([Page.Identity], added.Pages);
        // Without the request the edit wizard opens on the servers.
        var (plain, _) = await h.StartWizardAsync(editing: ImapAccount());
        Assert.Equal([Page.Identity, Page.Servers], plain.Pages);
    }

    // Windows additions

    /// <summary>
    /// GTK's launch: a browser that cannot be started says why, and the page
    /// keeps waiting for a sign-in the user may finish with "Open the Browser
    /// Again".
    /// </summary>
    [Fact]
    public async Task ALaunchThatFailsSaysWhy()
    {
        await using var h = await Harness.StartAsync();
        var wait = h.Hold();
        h.Launcher.Failure = new InvalidOperationException("No application is associated with the specified file for this operation");
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, _ => OAuthStartJson);
        h.On(API.AccountOAuthWait.Name, async _ =>
        {
            await wait.WaitAsync();
            return PendingJson;
        });
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(w.SignInWithProvider);
        await wait.ArrivedAsync();
        await rec.Conditions.WhenAsync(h.Ui, () => rec.Toasts.Count == 1);
        Assert.Equal(["The link could not be opened: No application is associated with the specified file for this operation"], rec.Toasts);
        Assert.Equal(new OAuthView.Waiting(), rec.OAuth[^1]);
        Assert.Equal([AuthUrl], h.Launcher.Opened);

        // The launcher's own refusal reads as the wizard's.
        h.Launcher.Failure = new ArgumentException("not an https address", "url");
        await h.Ui.RunAsync(w.ReopenBrowser);
        await rec.Conditions.WhenAsync(h.Ui, () => rec.Toasts.Count == 2);
        Assert.Equal("The link could not be opened: not an https address", rec.Toasts[^1]);
        await h.Ui.RunAsync(w.Close);
    }

    /// <summary>
    /// A sign-in started and closed in the same turn is never sent
    /// (docs/windows-port.md §7.2), so there is no session to let go.
    /// </summary>
    [Fact]
    public async Task ASignInClosedBeforeItWentOutIsNeverSent()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, _ => OAuthStartJson);
        var (w, _) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SignInWithProvider();
            w.Close();
        });
        await h.IdleAsync();
        Assert.DoesNotContain(API.AccountOAuthStart.Name, h.Fake.Calls);
        Assert.DoesNotContain(API.AccountOAuthCancel.Name, h.Fake.Calls);
    }

    /// <summary>
    /// A session that answers after the wizard closed has nobody to wait for
    /// it: it is cancelled, as Swift cancels it in the closed guard.
    /// </summary>
    [Fact]
    public async Task ASessionThatAnswersAfterCloseIsLetGo()
    {
        await using var h = await Harness.StartAsync();
        var start = h.Hold();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        h.On(API.AccountOAuthStart.Name, async _ =>
        {
            await start.WaitAsync();
            return OAuthStartJson;
        });
        h.On(API.AccountOAuthCancel.Name, _ => "{}");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(w.SignInWithProvider);
        await start.ArrivedAsync();
        await h.Ui.RunAsync(w.Close);
        start.Release();
        await h.IdleAsync();
        Assert.Equal("s_1", h.Params.Last<AccountOAuthCancelParams>(API.AccountOAuthCancel.Name)?.SessionId);
        Assert.Empty(h.Launcher.Opened);
        Assert.DoesNotContain(API.AccountOAuthWait.Name, h.Fake.Calls);
        Assert.Equal([true], rec.Starting); // nothing reported after the close
    }

    /// <summary>The observable state follows the events, for the window's bindings.</summary>
    [Fact]
    public async Task ThePropertiesFollowThePages()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDiscover.Name, _ => GmailDiscoveredJson);
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("", "me@gmail.com", "");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Contains(nameof(WizardController.Pages), rec.Properties);
        Assert.Contains(nameof(WizardController.CanGoBack), rec.Properties);
        Assert.Contains(nameof(WizardController.CurrentOAuthView), rec.Properties);
        Assert.Equal(GooglePrompt, w.CurrentOAuthView);
        Assert.Equal([Page.Identity, Page.OAuth], w.Pages);
        Assert.Equal(3, WizardController.Rank(Page.OAuth));
        Assert.Equal("Next", WizardController.WithoutMnemonic("_Next"));
        Assert.Equal("a_b", WizardController.WithoutMnemonic("a__b"));
    }

    private static string Stack(params Page[] pages) => string.Join(",", pages);

    private static ServerFields Fields(string host, int port, Security security, string username) =>
        new() { Host = host, Port = port, Security = security, Username = username };

    private static RpcException Daemon(int code, string message, string? data = null)
    {
        JsonElement? element = null;
        if (data is not null)
        {
            using var document = JsonDocument.Parse(data);
            element = document.RootElement.Clone();
        }
        return new RpcException(new RpcError { Code = code, Message = message, Data = element });
    }

    /// <summary>A handler that fails as the daemon does (Swift's <c>throw RPCError(...)</c>).</summary>
    private static Func<string, string> Fails(int code, string message) => _ => throw Daemon(code, message);

    // The refusal of an endpoint, with the certificate in the details; the
    // JSON is written with ' for " to stay readable.
    private static string RefusedJson(string sha, string reason = "untrusted", string? expected = null)
    {
        var expectedMember = expected is null ? "" : "'expectedSha256':'" + expected + "',";
        return ("{'ok':false,'latencyMs':3,'error':{'code':1303,'message':'x509: certificate signed by unknown authority',"
            + "'data':{'reason':'" + reason + "'," + expectedMember
            + "'certificate':{'sha256':'" + sha + "','subject':'127.0.0.1','issuer':'127.0.0.1',"
            + "'ipAddresses':['127.0.0.1'],'notBefore':'2024-01-02T03:04:05Z','notAfter':'2044-01-02T03:04:05Z','selfSigned':true}}}}")
            .Replace('\'', '"');
    }

    private static string TestRefusedJson(string? imap, string? smtp)
    {
        const string ok = """{"ok":true,"latencyMs":5}""";
        return "{\"imap\":" + (imap is null ? ok : RefusedJson(imap)) + ",\"smtp\":" + (smtp is null ? ok : RefusedJson(smtp)) + "}";
    }

    /// <summary>An account of the browser sign-in, as account.list returns it.</summary>
    private static Account OAuthAccount(string id = "acc-9", string status = SyncStatus.AuthRequired)
    {
        var imap = new ServerConfig { Host = "imap.gmail.com", Port = 993, Security = Security.Tls, Username = "me@gmail.com", AuthMethod = AuthMethod.OAuth2 };
        var smtp = new ServerConfig { Host = "smtp.gmail.com", Port = 465, Security = Security.Tls, Username = "me@gmail.com", AuthMethod = AuthMethod.OAuth2 };
        return new Account
        {
            Id = id,
            Config = new AccountConfig
            {
                Name = "Gmail",
                Email = "me@gmail.com",
                DisplayName = "Me",
                Imap = imap,
                Smtp = smtp,
                OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google },
            },
            Enabled = true,
            State = new SyncState { AccountId = id, Status = status },
        };
    }

    private static Account ImapAccount(string id = "acc-9") => new()
    {
        Id = id,
        Config = new AccountConfig
        {
            Name = "Work",
            Email = "me@example.com",
            DisplayName = "Me",
            Kind = AccountKind.Imap,
            Imap = new ServerConfig { Host = "imap.example.com", Port = 993, Security = Security.Tls, Username = "me", AuthMethod = AuthMethod.Password },
            Smtp = new ServerConfig { Host = "smtp.example.com", Port = 465, Security = Security.Tls, Username = "me", AuthMethod = AuthMethod.Password },
        },
        Enabled = true,
        State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
    };

    /// <summary>A password account whose IMAP endpoint pins <see cref="CertA"/>.</summary>
    private static Account PinnedAccount(string id = "acc-9")
    {
        var a = ImapAccount(id);
        return a with { Config = a.Config with { Imap = a.Config.Imap! with { CertificateSha256 = CertA } } };
    }

    /// <summary>
    /// A discovered account whose test answers <paramref name="first"/>,
    /// then OK; the params of every call are recorded.
    /// </summary>
    private static async Task<(Harness, WizardController, Recorder)> StartRefusedAsync(string first)
    {
        var h = await Harness.StartAsync();
        var count = 0;
        h.On(API.AccountDiscover.Name, _ => DiscoveredJson);
        h.On(API.AccountTest.Name, _ => Interlocked.Increment(ref count) == 1 ? first : TestOkJson);
        h.On(API.AccountAdd.Name, _ => """{"accountId":"acc-1"}""");
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetIdentity("Me", "me@example.com", "pw");
            w.Next();
        });
        await h.IdleAsync();
        Assert.NotNull(rec.LastResults);
        return (h, w, rec);
    }

    /// <summary>Records the parameters each method was called with (the Swift suite's ParamsLog).</summary>
    private sealed class ParamsLog
    {
        private readonly Lock gate = new();
        private readonly Dictionary<string, List<string>> byMethod = [];

        public void Record(string method, string json)
        {
            lock (gate)
            {
                if (!byMethod.TryGetValue(method, out var list))
                {
                    byMethod[method] = list = [];
                }
                list.Add(json);
            }
        }

        public T? Last<T>(string method)
            where T : class
        {
            string? json;
            lock (gate)
            {
                json = byMethod.TryGetValue(method, out var list) && list.Count > 0 ? list[^1] : null;
            }
            return json is null ? null : JsonCoding.Decode<T>(json);
        }
    }

    /// <summary>Swift's onOpenURL: the addresses handed to the browser; fails with <see cref="Failure"/> when set.</summary>
    private sealed class RecordingLauncher : ILauncher
    {
        private readonly Lock gate = new();
        private readonly List<string> opened = [];

        public Exception? Failure { get; set; }

        public IReadOnlyList<string> Opened
        {
            get
            {
                lock (gate)
                {
                    return [.. opened];
                }
            }
        }

        public Task<bool> OpenUrlAsync(string url, nint owner, CancellationToken cancellationToken = default)
        {
            lock (gate)
            {
                opened.Add(url);
            }
            return Failure is { } failure ? Task.FromException<bool>(failure) : Task.FromResult(true);
        }

        public Task<bool> OpenLinkAsync(string url, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();

        public string? LinkTarget(string? url) => throw new NotSupportedException();

        public Task<bool> OpenAssistantLinkAsync(string link, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();

        public Task<bool> OpenFileAsync(string path, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();

        public Task<bool> OpenWithAsync(string path, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();
    }

    /// <summary>Collects what the controller reports (the Swift suite's Recorder).</summary>
    private sealed class Recorder
    {
        public List<string> Stacks { get; } = [];

        public List<bool> Busy { get; } = [];

        public List<IdentityProblems> IdentityProblems { get; } = [];

        public List<string?> Banners { get; } = [];

        public List<ServerProblems> ServerProblems { get; } = [];

        public List<Identity> Identities { get; } = [];

        public List<AccountConfig> Applied { get; } = [];

        public List<IReadOnlyList<LinkedAccount>> Linked { get; } = [];

        public List<string> GoaHints { get; } = [];

        public List<bool> GoaHintBrowser { get; } = [];

        public List<OAuthView> OAuth { get; } = [];

        public List<bool> Starting { get; } = [];

        public List<TestingView> Testing { get; } = [];

        public List<WizardController.IdentityField> Focus { get; } = [];

        public List<string> Toasts { get; } = [];

        public List<AccountId> Done { get; } = [];

        public List<AccountConfig> DoneConfigs { get; } = [];

        public List<(string Imap, string Smtp)> Pins { get; } = [];

        public List<TrustPrompt> Prompts { get; } = [];

        public HashSet<string> Properties { get; } = [];

        /// <summary>What the trust confirmation answers.</summary>
        public bool TrustAnswer { get; set; } = true;

        public UiConditions Conditions { get; } = new();

        public string? LastStack => Stacks.Count == 0 ? null : Stacks[^1];

        public WizardController.ResultsView? LastResults => Testing.Count > 0 && Testing[^1] is TestingView.Results r ? r.View : null;

        public void Attach(WizardController w)
        {
            w.PagesChanged += (_, pages) => Note(() => Stacks.Add(string.Join(",", pages)));
            w.BusyChanged += (_, on) => Note(() => Busy.Add(on));
            w.IdentityProblemsShown += (_, e) => Note(() =>
            {
                IdentityProblems.Add(e.Problems);
                Banners.Add(e.Banner);
            });
            w.ServerProblemsShown += (_, p) => Note(() => ServerProblems.Add(p));
            w.IdentityChanged += (_, id) => Note(() => Identities.Add(id));
            w.ConfigApplied += (_, cfg) => Note(() => Applied.Add(cfg));
            w.LinkedChanged += (_, linked) => Note(() => Linked.Add(linked));
            w.GoaHintShown += (_, e) => Note(() =>
            {
                GoaHints.Add(e.Text);
                GoaHintBrowser.Add(e.Browser);
            });
            w.OAuthViewChanged += (_, view) => Note(() => OAuth.Add(view));
            w.OAuthStartingChanged += (_, on) => Note(() => Starting.Add(on));
            w.TestingChanged += (_, view) => Note(() => Testing.Add(view));
            w.FocusRequested += (_, field) => Note(() => Focus.Add(field));
            w.ToastRequested += (_, text) => Note(() => Toasts.Add(text));
            w.Done += (_, e) => Note(() =>
            {
                Done.Add(e.Id);
                DoneConfigs.Add(e.Config);
            });
            w.PinsChanged += (_, pins) => Note(() => Pins.Add(pins));
            w.PropertyChanged += (_, e) => Note(() => Properties.Add(e.PropertyName ?? ""));
            w.ConfirmTrust = prompt =>
            {
                Note(() => Prompts.Add(prompt));
                return Task.FromResult(TrustAnswer);
            };
        }

        private void Note(Action record)
        {
            record();
            Conditions.Changed();
        }
    }

    /// <summary>
    /// A fake daemon with linked accounts answered (empty, as on Windows),
    /// the UI thread, the recorded params, the launcher and the answers held
    /// back, all let go at the end.
    /// </summary>
    private sealed class Harness : IAsyncDisposable
    {
        private readonly List<HeldAnswer> holds = [];
        private readonly List<RpcClient> clients = [];
        private readonly List<WizardController> wizards = [];

        private Harness()
        {
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeDaemon Fake { get; } = new();

        public ParamsLog Params { get; } = new();

        public RecordingLauncher Launcher { get; } = new();

        public FakeTimeProvider Time { get; } = new(new DateTimeOffset(2026, 9, 27, 10, 0, 0, TimeSpan.Zero));

        public static async Task<Harness> StartAsync()
        {
            var h = new Harness();
            h.Fake.On(API.AccountLinked.Name, _ => """{"accounts":[]}""");
            await h.Fake.StartAsync();
            return h;
        }

        /// <summary>An answer the test lets go of later; released at the end at the latest.</summary>
        public HeldAnswer Hold()
        {
            var held = new HeldAnswer();
            holds.Add(held);
            return held;
        }

        /// <summary>Answers <paramref name="method"/>, recording its params first.</summary>
        public void On(string method, Func<string, string> answer) =>
            Fake.On(method, (FakeDaemon.MethodHandler)(p =>
            {
                Params.Record(method, p);
                return Task.FromResult(answer(p));
            }));

        /// <summary>Answers <paramref name="method"/> asynchronously, recording its params first.</summary>
        public void On(string method, Func<string, Task<string>> answer) =>
            Fake.On(method, (FakeDaemon.MethodHandler)(p =>
            {
                Params.Record(method, p);
                return answer(p);
            }));

        /// <summary>A started wizard whose linked accounts have loaded.</summary>
        public async Task<(WizardController, Recorder)> StartWizardAsync(
            Account? editing = null, bool signIn = false, ErrorCode? requestPassword = null)
        {
            var client = new RpcClient(Fake.Path, PortableKeyFilePolicy.Instance);
            clients.Add(client);
            await client.ConnectAsync(TestContext.Current.CancellationToken);
            var (w, rec) = await Ui.RunAsync(() =>
            {
                var w = new WizardController(client, Launcher, editing, signIn, Time, pending: Pending);
                if (requestPassword is { } reason)
                {
                    w.RequestPassword(reason);
                }
                var rec = new Recorder();
                rec.Attach(w);
                w.Start();
                wizards.Add(w);
                return (w, rec);
            });
            await IdleAsync();
            Assert.NotEmpty(rec.Linked);
            return (w, rec);
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() =>
            {
                foreach (var w in wizards)
                {
                    w.Close();
                }
            });
            foreach (var held in holds)
            {
                held.Release();
            }
            try
            {
                await Pending.IdleAsync().WaitAsync(TimeSpan.FromSeconds(10));
                await Fake.IdleAsync().WaitAsync(TimeSpan.FromSeconds(10));
            }
            catch (TimeoutException)
            {
                // Torn down all the same; the test's own asserts said what went wrong.
            }
            foreach (var client in clients)
            {
                client.Dispose();
            }
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
