// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/EWayWizardControllerTests.swift, the
// tests of ui/internal/accountwizard/eway_flow_test.go (TestEwayFlowAdds,
// TestEwayFlowRefusesHTTPAndMicrosoft, TestEwayFlowErrors,
// TestEwayFlowBackDropsReply, TestEwayFlowEdits, TestEwayFlowFollowThreads):
// the eWay-CRM account assistant against a fake daemon
// (account.detectEway, account.test, account.add, account.update).
// Fictional instances and people only.
//
// As JiraWizardControllerTests: a test waits until nothing is left to
// happen (IdleAsync), and an answer Go holds back with a channel is held
// until the test releases it (HeldAnswer). Go's errText is RpcErrorText's
// sentence here; Go's plain error ("boom") is the daemon's internalError.
// The pages' buttons come without their mnemonic (WizardController
// .WithoutMnemonic), as the Jira assistant's do.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Crm;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Controllers.DaemonHarness;

namespace Malachi.Core.Tests.Controllers;

public sealed class EwayWizardControllerTests
{
    private const string DetectedJson = """{"serviceUrl":"https://hosting.eway-crm.com/acme","microsoftSignIn":false,"version":"10.0"}""";
    private const string MicrosoftJson = """{"serviceUrl":"https://hosting.eway-crm.com/acme","microsoftSignIn":true}""";
    private const string TestOkJson = """{"eway":{"ok":true,"latencyMs":120}}""";
    private const string AddedJson = """{"accountId":"acc-e1"}""";

    private static Account EwayAccount() => new()
    {
        Id = "acc-e1",
        Enabled = true,
        State = new SyncState { AccountId = "acc-e1", Status = SyncStatus.Idle },
        Capabilities = [Capability.Crm],
        Config = new AccountConfig
        {
            Kind = AccountKind.Eway,
            Name = "Acme",
            Email = "jana@acme.example",
            Eway = new EwayConfig
            {
                ServiceUrl = "https://hosting.eway-crm.com/acme",
                Username = "jana",
                UserGuid = "0b9e3d2c-1a2b-4c3d-8e9f-001122334455",
            },
        },
    };

    [Fact]
    public async Task Adds()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectEway.Name, _ => DetectedJson);
        h.On(API.AccountTest.Name, _ => TestOkJson);
        h.On(API.AccountAdd.Name, _ => AddedJson);
        var (w, rec) = await h.StartWizardAsync();
        Assert.False(rec.Checks[^1].Ok);
        Assert.Equal("Add eWay-CRM Account", w.Title);
        Assert.Equal("Next", w.NextLabel(CrmWizardPage.Service));
        Assert.False(w.CanGoBack);
        await h.Ui.RunAsync(() => w.SetService(" hosting.eway-crm.com/acme/API.svc "));
        Assert.True(rec.Checks[^1].Ok);
        await h.Ui.RunAsync(w.Next);
        Assert.Equal("Contacting eWay-CRM", rec.Busy[0]);
        await h.IdleAsync();
        Assert.Equal([CrmWizardPage.Service, CrmWizardPage.Credentials], rec.Pages[^1]);
        Assert.Equal("hosting.eway-crm.com/acme/API.svc", h.Params.Last<AccountDetectEwayParams>(API.AccountDetectEway.Name)?.ServiceUrl);
        Assert.Equal(TimeSpan.FromSeconds(15), API.AccountDetectEway.Timeout);
        Assert.Equal("acme", rec.Names[^1]);
        Assert.Equal("https://hosting.eway-crm.com/acme", w.ServiceUrl);
        Assert.Equal(CrmWizardField.User, rec.Focus[^1]);
        Assert.Equal(("Add Account", "Password", "Sign In"), (w.NextLabel(CrmWizardPage.Credentials), w.PasswordLabel, w.PageTitle(CrmWizardPage.Credentials)));

        // No password: flagged, nothing asked.
        await h.Ui.RunAsync(() =>
        {
            w.SetCredentials("jana", "");
            w.Next();
        });
        Assert.Equal(CrmWizardField.Password, rec.Problems[^1]);
        Assert.Equal((CrmWizardPage.Credentials, (string?)"Enter the password for this account"), rec.Banners[^1]);
        Assert.Equal(0, h.Params.Count(API.AccountTest.Name));
        await h.Ui.RunAsync(() =>
        {
            w.SetName("Acme CRM");
            w.SetCredentials(" jana ", "s3cret pass");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Single(rec.Done);
        var tp = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
        Assert.True(tp.Config.Eway!.FollowThreads, "a new account follows saved conversations (D22)");
        Assert.Null(tp.AccountId);
        Assert.Equal(("s3cret pass", AccountKind.Eway, "https://hosting.eway-crm.com/acme", "jana"), (tp.Credentials.Password, tp.Config.Kind?.Value, tp.Config.Eway.ServiceUrl, tp.Config.Eway.Username));
        var ap = h.Params.Last<AccountAddParams>(API.AccountAdd.Name)!;
        Assert.Equal(("Acme CRM", AccountKind.Eway, "s3cret pass"), (ap.Config.Name, ap.Config.Kind?.Value, ap.Credentials.Password));
        ApiJson.AssertSameValue(new EwayConfig { ServiceUrl = "https://hosting.eway-crm.com/acme", Username = "jana", FollowThreads = true }, ap.Config.Eway);
        Assert.Equal("acc-e1", rec.Done[0].Value);
        Assert.Equal([API.AccountDetectEway.Name, API.AccountTest.Name, API.AccountAdd.Name], h.Fake.Calls);
        Assert.Contains("Signing in", rec.Busy);
        Assert.Contains("Adding the account", rec.Busy);
        Assert.Null(rec.Busy[^1]);
        Assert.Equal((TimeSpan.FromSeconds(50), TimeSpan.FromSeconds(60)), (CrmTimeouts.Test, CrmTimeouts.Store));
    }

    // http is refused before anything is asked; a Microsoft instance is told
    // on the address page, and no password page follows.
    [Fact]
    public async Task RefusesHttpAndMicrosoft()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectEway.Name, _ => MicrosoftJson);
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() => w.SetService("http://hosting.eway-crm.com/acme"));
        Assert.Equal((false, "Malachi Mail connects to eWay-CRM over https only."), rec.Checks[^1]);
        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Equal(0, h.Params.Count(API.AccountDetectEway.Name));
        Assert.Equal(CrmWizardField.Service, rec.Problems[^1]);
        await h.Ui.RunAsync(() =>
        {
            w.SetService("hosting.eway-crm.com/acme");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(1, h.Params.Count(API.AccountDetectEway.Name));
        Assert.Null(rec.Busy[^1]);
        Assert.Equal((CrmWizardPage.Service, (string?)"This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet."), rec.Banners[^1]);
        Assert.Equal(CrmWizardPage.Service, w.Top);
        Assert.Equal(CrmWizardField.Service, rec.Problems[^1]);
        Assert.Null(w.Service);
    }

    [Fact]
    public async Task Errors()
    {
        await using var h = await Harness.StartAsync();
        h.On(API.AccountDetectEway.Name, Fails(ErrorCode.NetworkError, "dial"));
        var (w, rec) = await h.StartWizardAsync();
        await h.Ui.RunAsync(() =>
        {
            w.SetService("crm.example.org");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal((CrmWizardPage.Service, (string?)RpcErrorText.Text("Contacting eWay-CRM", Daemon(ErrorCode.NetworkError, "dial"))), rec.Banners[^1]);

        // A wrong password (in the test's endpoint result) flags the password.
        h.On(API.AccountDetectEway.Name, _ => DetectedJson);
        h.On(API.AccountTest.Name, _ => """{"eway":{"ok":false,"latencyMs":0,"error":{"code":1201,"message":"eway: LogIn: rcLoginFailed"}}}""");
        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Equal([CrmWizardPage.Service, CrmWizardPage.Credentials], rec.Pages[^1]);
        await h.Ui.RunAsync(() =>
        {
            w.SetCredentials("jana", "wrong");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal(CrmWizardField.Password, rec.Problems[^1]);
        Assert.Equal((CrmWizardPage.Credentials, (string?)"The server rejected the user name or password"), rec.Banners[^1]);
        Assert.Equal(0, h.Params.Count(API.AccountAdd.Name));

        // A plan without API access, at add (the daemon signs in again).
        h.On(API.AccountTest.Name, _ => TestOkJson);
        h.On(API.AccountAdd.Name, _ => throw new RpcException(new RpcError { Code = ErrorCode.CrmSignInUnsupported, Message = "plan", Data = ApiJson.Parse("""{"reason":"plan"}""") }));
        await h.Ui.RunAsync(() =>
        {
            w.SetCredentials("jana", "right");
            w.Next();
        });
        await h.IdleAsync();
        Assert.Equal("This eWay-CRM does not allow access through its API.", rec.Banners[^1].Text);
        Assert.Equal(CrmWizardPage.Credentials, w.Top);
        Assert.Empty(rec.Done);

        // Microsoft found only by account.add: back to the address page.
        h.On(API.AccountAdd.Name, _ => throw new RpcException(new RpcError { Code = ErrorCode.CrmSignInUnsupported, Message = "ms", Data = ApiJson.Parse("""{"reason":"microsoft"}""") }));
        await h.Ui.RunAsync(w.Next);
        await h.IdleAsync();
        Assert.Equal([CrmWizardPage.Service], rec.Pages[^1]);
        Assert.Equal(CrmWizardField.Service, rec.Problems[^1]);
    }

    // Back drops the reply of the call under way; a changed address forgets
    // the instance; Next twice while busy asks once.
    [Fact]
    public async Task BackDropsReply()
    {
        await using (var h = await Harness.StartAsync())
        {
            var held = h.Hold();
            h.On(API.AccountDetectEway.Name, async _ =>
            {
                await held.WaitAsync();
                return DetectedJson;
            });
            var (w, rec) = await h.StartWizardAsync();
            await h.Ui.RunAsync(() =>
            {
                w.SetService("hosting.eway-crm.com/acme");
                w.Next();
                w.Next();
            });
            await held.ArrivedAsync();
            await h.Ui.RunAsync(w.Close);
            held.Release();
            await h.IdleAsync();
            Assert.Single(rec.Pages);
            Assert.Equal(1, h.Params.Count(API.AccountDetectEway.Name));
        }

        await using (var h = await Harness.StartAsync())
        {
            h.On(API.AccountDetectEway.Name, _ => DetectedJson);
            var held = h.Hold();
            h.On(API.AccountTest.Name, async _ =>
            {
                await held.WaitAsync();
                return TestOkJson;
            });
            var (w, rec) = await h.StartWizardAsync();
            await h.Ui.RunAsync(() =>
            {
                w.SetService("hosting.eway-crm.com/acme");
                w.Next();
            });
            await h.IdleAsync();
            Assert.Equal([CrmWizardPage.Service, CrmWizardPage.Credentials], rec.Pages[^1]);
            await h.Ui.RunAsync(() =>
            {
                w.SetCredentials("jana", "pw");
                w.Next();
            });
            await held.ArrivedAsync();
            await h.Ui.RunAsync(w.Back);
            held.Release();
            await h.IdleAsync();
            Assert.Equal(0, h.Params.Count(API.AccountAdd.Name));
            Assert.Equal(CrmWizardPage.Service, w.Top);
            Assert.False(w.Busy);
            await h.Ui.RunAsync(() => w.SetService("crm.example.org"));
            Assert.Null(w.Service);
        }
    }

    // Editing: on the sign-in page with the account's address, name and
    // user; an empty password keeps the stored one (account.test with
    // accountId), unless the user name changed.
    [Fact]
    public async Task Edits()
    {
        var acc = EwayAccount();
        await using (var h = await Harness.StartAsync())
        {
            h.On(API.AccountTest.Name, _ => TestOkJson);
            h.On(API.AccountUpdate.Name, _ => "{}");
            var (w, rec) = await h.StartWizardAsync(acc);
            Assert.Equal("Edit Account", w.Title);
            Assert.Equal(CrmWizardPage.Credentials, w.Top);
            Assert.False(w.CanGoBack);
            Assert.Equal("Save", w.NextLabel(CrmWizardPage.Credentials));
            Assert.Equal("New Password (leave empty to keep)", w.PasswordLabel);
            Assert.Equal("Acme", rec.Names[^1]);
            Assert.Equal("jana", w.User);
            Assert.Equal("https://hosting.eway-crm.com/acme", w.ServiceUrl);
            await h.Ui.RunAsync(() => w.SetService("https://other.example.org"));
            Assert.Equal("https://hosting.eway-crm.com/acme", w.ServiceUrl);
            await h.Ui.RunAsync(() =>
            {
                w.SetName("Acme Sales");
                w.Next();
            });
            await h.IdleAsync();
            Assert.Single(rec.Done);
            var tp = h.Params.Last<AccountTestParams>(API.AccountTest.Name)!;
            Assert.Equal(("acc-e1", (string?)null, "jana"), (tp.AccountId?.Value, tp.Credentials.Password, tp.Config.Eway?.Username));
            var up = h.Params.Last<AccountUpdateParams>(API.AccountUpdate.Name)!;
            Assert.Equal(("acc-e1", "Acme Sales", "jana@acme.example", acc.Config.Eway!.UserGuid), (up.AccountId.Value, up.Config.Name, up.Config.Email, up.Config.Eway?.UserGuid));
            Assert.Null(up.Credentials.Password);
            Assert.Contains("Saving the account", rec.Busy);
        }

        // Another user needs a password, and forgets the user GUID.
        await using (var h = await Harness.StartAsync())
        {
            h.On(API.AccountTest.Name, _ => TestOkJson);
            h.On(API.AccountUpdate.Name, _ => "{}");
            var (w, rec) = await h.StartWizardAsync(acc);
            await h.Ui.RunAsync(() =>
            {
                w.SetCredentials("petr", "");
                w.Next();
            });
            Assert.Equal(0, h.Params.Count(API.AccountTest.Name));
            Assert.Equal(CrmWizardField.Password, rec.Problems[^1]);
            await h.Ui.RunAsync(() =>
            {
                w.SetCredentials("petr", "pw2");
                w.Next();
            });
            await h.IdleAsync();
            Assert.Single(rec.Done);
            var up = h.Params.Last<AccountUpdateParams>(API.AccountUpdate.Name)!;
            Assert.Equal(("petr", (string?)null, "pw2"), (up.Config.Eway?.Username, up.Config.Eway?.UserGuid, up.Credentials.Password));
            Assert.Equal("jana", acc.Config.Eway!.Username); // the edited account's configuration is not changed in place
        }

        // A failure of the update: the client's sentence, on the sign-in page.
        await using (var h = await Harness.StartAsync())
        {
            h.On(API.AccountTest.Name, _ => TestOkJson);
            h.On(API.AccountUpdate.Name, Fails(ErrorCode.InternalError, "boom"));
            var (w, rec) = await h.StartWizardAsync(acc);
            await h.Ui.RunAsync(w.Next);
            await h.IdleAsync();
            Assert.Equal(RpcErrorText.Text("Saving the account", Daemon(ErrorCode.InternalError, "boom")), rec.Banners[^1].Text);
            Assert.Empty(rec.Done);
            Assert.Equal(CrmWizardPage.Credentials, w.Top);
        }
    }

    // D22: the check box "Also save later messages of saved conversations":
    // on for a new account, the account's value when editing, sent with
    // account.test and account.update.
    [Fact]
    public async Task FollowThreads()
    {
        await using (var h = await Harness.StartAsync())
        {
            h.On(API.AccountDetectEway.Name, _ => DetectedJson);
            h.On(API.AccountTest.Name, _ => TestOkJson);
            h.On(API.AccountAdd.Name, _ => AddedJson);
            var (w, rec) = await h.StartWizardAsync();
            Assert.True(w.FollowThreads);
            await h.Ui.RunAsync(() =>
            {
                w.SetService("hosting.eway-crm.com/acme");
                w.Next();
            });
            await h.IdleAsync();
            await h.Ui.RunAsync(() =>
            {
                w.SetFollowThreads(false);
                w.SetCredentials("jana", "pw");
                w.Next();
            });
            await h.IdleAsync();
            Assert.Single(rec.Done);
            Assert.False(h.Params.Last<AccountAddParams>(API.AccountAdd.Name)!.Config.Eway!.FollowThreads);
        }

        await using (var h = await Harness.StartAsync())
        {
            h.On(API.AccountTest.Name, _ => TestOkJson);
            h.On(API.AccountUpdate.Name, _ => "{}");
            var acc = EwayAccount();
            var (w, rec) = await h.StartWizardAsync(acc);
            Assert.False(w.FollowThreads, "the edited account's value (off) is shown");
            await h.Ui.RunAsync(() =>
            {
                w.SetFollowThreads(true);
                w.Next();
            });
            await h.IdleAsync();
            Assert.Single(rec.Done);
            Assert.True(h.Params.Last<AccountTestParams>(API.AccountTest.Name)!.Config.Eway!.FollowThreads);
            Assert.True(h.Params.Last<AccountUpdateParams>(API.AccountUpdate.Name)!.Config.Eway!.FollowThreads);
            Assert.False(acc.Config.Eway!.FollowThreads);
            // Always written, as Go writes it.
            var config = ApiJson.Parse(h.Params.LastRaw(API.AccountUpdate.Name)!).GetProperty("config").GetProperty("eway");
            Assert.True(config.GetProperty("followThreads").GetBoolean());
        }
    }

    // What the assistant reports (eway_flow_test.go ewayRecorder).
    private sealed class Recorder
    {
        public List<IReadOnlyList<CrmWizardPage>> Pages { get; } = [];

        public List<string?> Busy { get; } = [];

        public List<(bool Ok, string Problem)> Checks { get; } = [];

        public List<string> Names { get; } = [];

        public List<(CrmWizardPage Page, string? Text)> Banners { get; } = [];

        public List<CrmWizardField> Problems { get; } = [];

        public List<CrmWizardField> Focus { get; } = [];

        public List<AccountId> Done { get; } = [];

        public void Attach(EwayWizardController w)
        {
            w.PagesChanged += (_, p) => Pages.Add([.. p]);
            w.BusyChanged += (_, s) => Busy.Add(s);
            w.ServiceChecked += (_, c) => Checks.Add(c);
            w.NameChanged += (_, n) => Names.Add(n);
            w.BannerChanged += (_, b) => Banners.Add(b);
            w.ProblemShown += (_, f) => Problems.Add(f);
            w.FocusRequested += (_, f) => Focus.Add(f);
            w.Done += (_, d) => Done.Add(d.Id);
        }
    }

    private sealed class Harness(DaemonHarness daemon) : IAsyncDisposable
    {
        public TestUIContext Ui => daemon.Ui;

        public FakeDaemon Fake => daemon.Fake;

        public ParamsLog Params => daemon.Params;

        public static async Task<Harness> StartAsync() => new(await DaemonHarness.StartAsync());

        public HeldAnswer Hold() => daemon.Hold();

        public void On(string method, Func<string, string> answer) => daemon.On(method, answer);

        public void On(string method, Func<string, Task<string>> answer) => daemon.On(method, answer);

        public async Task<(EwayWizardController, Recorder)> StartWizardAsync(Account? editing = null)
        {
            var client = await daemon.ConnectAsync();
            return await Ui.RunAsync(() =>
            {
                var w = new EwayWizardController(client, editing, pending: daemon.Pending);
                daemon.CloseAtEnd(w.Close);
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
