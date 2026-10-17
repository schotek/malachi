// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarAccountControllerTests.swift.
// An account's Calendar and Invitations groups (CalendarAccountController)
// over the in-memory samples: the Graph switch with the consent question
// and the re-sign-in hand-off, the calendars' checkboxes and default by
// remote id (Graph from calendar.list, CalDAV also from the server), Keep
// Events, the invitation switches, an account without a calendar, a
// refusal undone, the CalDAV account's group. Swift's calWaitUntil polls
// are Quiescence.IdleAsync here. The last tests are Windows's: Remove
// Account and the calendar's sign-in page, which macOS calls from its pane
// (CalendarPaneViewController), against a fake daemon.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;
using Write = Malachi.Core.Calendars.InMemoryCalendarSource.Write;

namespace Malachi.Core.Tests.Controllers;

public sealed class CalendarAccountControllerTests
{
    /// <summary>Wednesday 14 October 2026, 10:17 in Prague (Swift <c>calNow</c>).</summary>
    private static readonly DateTimeOffset Now = F.At(2026, 10, 14, 10, 17);

    /// <summary>The samples with emulated writes (Swift <c>calSamples()</c>).</summary>
    private static InMemoryCalendarSource Samples()
    {
        var s = InMemoryCalendarSource.Samples(Now, F.Prague);
        s.EmulatesWrites = true;
        return s;
    }

    // Swift's account(_:_:): the section of the account, started.
    private static CalendarAccountController Account(
        InMemoryCalendarSource src, AccountId id, PendingWork pending, RpcClient? client = null)
    {
        var c = new CalendarAccountController(src, id, client, pending);
        c.Start();
        return c;
    }

    // Swift's lastConfig(_:): the calendar part of the last account.update.
    private static CalendarConfig? LastConfig(InMemoryCalendarSource src) =>
        src.Writes.Count > 0 && src.Writes[^1] is Write.UpdateAccount u ? u.Parameters.Config.Calendar : null;

    private static AccountUpdateParams LastUpdate(InMemoryCalendarSource src) =>
        Assert.IsType<Write.UpdateAccount>(src.Writes[^1]).Parameters;

    private static RpcException Daemon(int code, string message) => new(new RpcError { Code = code, Message = message });

    [Fact]
    public async Task GraphSyncAsksForConsent()
    {
        using var h = new Harness();
        var src = Samples();
        var questions = new List<Cal.Question>();
        var signIn = new List<AccountId>();
        var c = await h.Ui.RunAsync(() =>
        {
            // The Work account with sync off.
            src.Update(s => s with
            {
                Accounts = [s.Accounts[0] with { Config = s.Accounts[0].Config with { Calendar = new CalendarConfig { Sync = false } } }, .. s.Accounts.Skip(1)],
            });
            var c = Account(src, Cal.SampleWork, h.Pending);
            Assert.True(c.IsGraph && c.ShowsSyncSwitch && !c.SyncOn && c.HasCalendarCapability && !c.ShowsNoCalendar);
            c.AskQuestion = q =>
            {
                questions.Add(q);
                return Task.FromResult(Cal.Answer.Confirm);
            };
            c.SignInRequested += (_, id) => signIn.Add(id);
            c.SetSync(true);
            Assert.True(c.SyncOn && c.Saving);
            return c;
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.Saving);
            Assert.True(LastConfig(src)?.Sync);
            Assert.Equal(new Credentials(), LastUpdate(src).Credentials);
            // The daemon reports the missing scopes.
            src.Handle(new DaemonNotification.CalendarState(
                new CalendarSyncState { AccountId = Cal.SampleWork, Status = CalendarSyncStatus.ConsentRequired, Progress = -1 }));
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Cal.Prefs.SyncConsentNote, questions[0].Body);
            Assert.Equal([Cal.SampleWork], signIn);
            Assert.True(c.ConsentRequired);
        });
    }

    [Fact]
    public async Task GraphCalendarsToggleAndDefault()
    {
        using var h = new Harness();
        var src = Samples();
        var c = await h.Ui.RunAsync(() =>
        {
            src.Update(s => s with
            {
                CalendarAccounts =
                [
                    s.CalendarAccounts[0] with
                    {
                        Calendars =
                        [
                            .. s.CalendarAccounts[0].Calendars,
                            new()
                            {
                                Id = new CalendarId("cal_sample_old"), AccountId = Cal.SampleWork, RemoteId = "AAMkSampleOld",
                                Name = "Old projects", Color = "#888888", Visible = false, Access = CalendarAccess.Owner,
                                Kind = CalendarKind.Events, Disabled = true,
                            },
                        ],
                    },
                    .. s.CalendarAccounts.Skip(1),
                ],
                // As the daemon keeps it: the account's configuration names it.
                Accounts =
                [
                    s.Accounts[0] with
                    {
                        Config = s.Accounts[0].Config with { Calendar = new CalendarConfig { Sync = true, DisabledCalendars = ["AAMkSampleOld"] } },
                    },
                    .. s.Accounts.Skip(1),
                ],
            });
            var c = Account(src, Cal.SampleWork, h.Pending);
            Assert.Null(c.Listed);
            Assert.Equal(["Calendar", "Birthdays", "Czech holidays", "Old projects"], c.Rows.Select(r => r.Name));
            Assert.All(c.Rows, r => Assert.True(r.CanToggle));
            Assert.Equal([true, true, true, false], c.Rows.Select(r => r.Enabled));
            Assert.Equal("default", c.Rows[0].Note);
            c.SetEnabled(false, "AAMkSampleHolidays");
            return c;
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(["AAMkSampleOld", "AAMkSampleHolidays"], LastConfig(src)?.DisabledCalendars ?? []);
            Assert.False(c.Rows[2].Enabled);
            c.SetEnabled(true, "AAMkSampleOld");
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.True(c.Rows[3].Enabled);
            Assert.Equal(["AAMkSampleCalendar", "AAMkSampleOld"], c.DefaultChoices.Select(r => r.RemoteId));
            c.SetDefaultCalendar("AAMkSampleOld");
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal("AAMkSampleOld", LastConfig(src)?.DefaultCalendar);
            Assert.Equal("default", c.Rows[3].Note);
            Assert.Empty(c.Rows[0].Note);
            // Disabling the default drops it.
            c.SetEnabled(false, "AAMkSampleOld");
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.NotNull(LastConfig(src));
            Assert.Null(LastConfig(src)?.DefaultCalendar);
            Assert.DoesNotContain(src.Writes, w => w is Write.ListCalDAV);
        });
    }

    [Fact]
    public async Task CaldavCalendarsDefaultAndKeep()
    {
        using var h = new Harness();
        var src = Samples();
        src.ListResult = new AccountListCalDAVCalendarsResult
        {
            PrincipalUrl = "p",
            HomeSetUrl = "h",
            Scheduling = CalDAVScheduling.Client,
            Calendars =
            [
                new()
                {
                    Href = "/remote.php/dav/calendars/me/sport/", Name = "Sport", Color = "#0082c9", Access = CalendarAccess.Owner, IsDefault = true,
                },
                new() { Href = "/c/club/", Name = "Club", Access = CalendarAccess.Read },
            ],
        };
        var c = await h.Ui.RunAsync(() => Account(src, Cal.SampleNextcloud, h.Pending));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.NotNull(c.Listed);
            var l = Assert.IsType<Write.ListCalDAV>(src.Writes[0]).Parameters;
            Assert.Equal(Cal.SampleNextcloud, l.AccountId);
            Assert.Equal(new Credentials(), l.Credentials);
            Assert.True(c.IsCalDAV && !c.ShowsSyncSwitch && c.SyncOn);
            // The stored calendar once, then the server's calendar not stored.
            Assert.Equal(["Sport", "Club"], c.Rows.Select(r => r.Name));
            Assert.All(c.Rows, r => Assert.True(r.CanToggle));
            Assert.Equal("default", c.Rows[0].Note);
            Assert.Equal("read only", c.Rows[1].Note);
            c.SetEnabled(false, "/c/club/");
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(["/c/club/"], LastConfig(src)?.DisabledCalendars ?? []);
            Assert.False(c.Rows[1].Enabled);
            Assert.Equal(["/remote.php/dav/calendars/me/sport/"], c.DefaultChoices.Select(r => r.RemoteId));
            c.SetKeepDays(90);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(90, LastConfig(src)?.KeepDays);
            Assert.Equal(90, c.KeepDays);
            c.SetKeepDays(91);
            Assert.Equal(90, c.KeepDays);
            Assert.False(c.Saving);
        });
    }

    [Fact]
    public async Task InvitationSwitches()
    {
        using var h = new Harness();
        var src = Samples();
        var c = await h.Ui.RunAsync(() =>
        {
            var c = Account(src, Cal.SampleWork, h.Pending);
            Assert.True(c.InvitationCard && c.ShowUnanswered);
            Assert.Equal(AfterAnswer.Keep, c.AfterAnswer.Value);
            c.SetInvitationCard(false);
            return c;
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() => c.SetAfterAnswer(AfterAnswer.Archive));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => c.SetShowUnanswered(false));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            var cfg = LastConfig(src);
            Assert.NotNull(cfg);
            Assert.False(cfg.InvitationCard);
            Assert.Equal(AfterAnswer.Archive, cfg.AfterAnswer?.Value);
            Assert.False(cfg.ShowUnanswered);
        });
    }

    [Fact]
    public async Task AccountWithoutACalendar()
    {
        using var h = new Harness();
        var src = Samples();
        await h.Ui.RunAsync(() =>
        {
            src.Update(s => s with
            {
                Accounts =
                [
                    .. s.Accounts,
                    new()
                    {
                        Id = new AccountId("acc_imap"),
                        Config = new AccountConfig { Name = "Club", Email = "c@example.org", Kind = AccountKind.Imap },
                        Enabled = true,
                        State = new SyncState { AccountId = new AccountId("acc_imap"), Status = SyncStatus.Idle },
                        Capabilities = API.MailCapabilities,
                    },
                ],
            });
            var c = Account(src, new AccountId("acc_imap"), h.Pending);
            Assert.True(c.ShowsNoCalendar);
            var asked = 0;
            c.AddCalDAVRequested += (_, _) => asked++;
            c.AddCalDAV();
            Assert.Equal(1, asked);
        });
        await h.IdleAsync();
    }

    [Fact]
    public async Task RefusalIsUndone()
    {
        using var h = new Harness();
        var src = Samples();
        src.Failures[API.AccountUpdate.Name] = Daemon(ErrorCode.InvalidArgument, "no");
        var c = await h.Ui.RunAsync(() =>
        {
            var c = Account(src, Cal.SampleWork, h.Pending);
            c.SetAfterAnswer(AfterAnswer.Archive);
            Assert.Equal(AfterAnswer.Archive, c.AfterAnswer.Value);
            return c;
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.Saving);
            Assert.Equal(AfterAnswer.Keep, c.AfterAnswer.Value);
            Assert.NotEmpty(c.Error);
        });
    }

    [Fact]
    public async Task CaldavAccountGroup()
    {
        using var h = new Harness();
        var src = Samples();
        src.ListResult = new AccountListCalDAVCalendarsResult { PrincipalUrl = "p", HomeSetUrl = "h", Scheduling = CalDAVScheduling.Client };
        var c = await h.Ui.RunAsync(() => Account(src, Cal.SampleNextcloud, h.Pending));
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.NotNull(c.Listed);
            Assert.Equal(CalDAVScheduling.Client, c.Scheduling?.Value);
            Assert.Equal("cloud.example.com", c.ServerHost);
            Assert.Equal("me", c.Username);
            Assert.False(c.CertificatePinned);
            Assert.Equal("Verified", c.CertificateText);
            Assert.Null(c.MailAccountId);
            Assert.Equal([Cal.SampleWork, Cal.SampleGmail], c.MailAccounts.Select(a => a.Id));
            c.SetName("  Cloud  ");
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            var p = LastUpdate(src);
            Assert.Equal("Cloud", p.Config.Name);
            Assert.Equal(new Credentials(), p.Credentials);
            c.SetName(" ");
            c.SetMailAccount(Cal.SampleWork);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            var m = LastUpdate(src);
            Assert.Equal(Cal.SampleWork, m.Config.CalDAV?.MailAccountId);
            Assert.Equal("Cloud", m.Config.Name);
            c.ReplacePassword("new secret");
        });
        await h.IdleAsync();
        var pw = await h.Ui.RunAsync(() => LastUpdate(src));
        Assert.Equal("new secret", pw.Credentials.Password);
        await h.Ui.RunAsync(() =>
        {
            // A pin, then Forget.
            src.Update(s => s with
            {
                Accounts =
                [
                    .. s.Accounts.Select(a => a.Id == Cal.SampleNextcloud
                        ? a with { Config = pw.Config with { CalDAV = pw.Config.CalDAV! with { CertificateSha256 = string.Concat(Enumerable.Repeat("ab", 32)) } } }
                        : a),
                ],
            });
            Assert.True(c.CertificatePinned);
            Assert.Equal("Trusted by you", c.CertificateText);
            c.ForgetCertificate();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            var f = LastUpdate(src);
            Assert.Null(f.Config.CalDAV?.CertificateSha256);
            Assert.False(c.CertificatePinned);
        });
    }

    /// <summary>Windows: Remove Account asks the Accounts page's question and calls account.remove with its check box.</summary>
    [Fact]
    public async Task RemoveAsksTheAccountsPageQuestion()
    {
        await using var h = await DaemonHarness.StartAsync();
        h.On(API.AccountRemove.Name, _ => "{}");
        var client = await h.ConnectAsync();
        var src = Samples();
        var prompts = new List<AccountRemovalPrompt>();
        var toasts = new List<string>();
        var answer = (Confirmed: false, DeleteLocalData: false);
        var c = await h.Ui.RunAsync(() =>
        {
            var c = Account(src, Cal.SampleNextcloud, h.Pending, client);
            c.ToastRequested += (_, t) => toasts.Add(t);
            return c;
        });
        Task<(bool, bool)> Confirm(AccountRemovalPrompt p)
        {
            prompts.Add(p);
            return Task.FromResult(answer);
        }

        // Cancelled: nothing is removed.
        await h.Ui.RunAsync(() => c.Remove(Confirm));
        await h.IdleAsync();
        Assert.Equal(0, h.Params.Count(API.AccountRemove.Name));
        var prompt = Assert.Single(prompts);
        Assert.Equal("Remove this account?", prompt.Heading);
        Assert.Equal("Nextcloud will be removed from Malachi Mail. Mail on the server is not affected.", prompt.Body);
        Assert.True(prompt.ExtraDefault);

        // Confirmed without the local data.
        answer = (true, false);
        await h.Ui.RunAsync(() => c.Remove(Confirm));
        await h.IdleAsync();
        var sent = h.Params.Last<AccountRemoveParams>(API.AccountRemove.Name);
        Assert.NotNull(sent);
        Assert.Equal(Cal.SampleNextcloud, sent.AccountId);
        Assert.False(sent.DeleteLocalData);
        Assert.False(await h.Ui.RunAsync(() => c.Removing));
        Assert.Empty(toasts);

        // Refused: a toast says why.
        h.On(API.AccountRemove.Name, DaemonHarness.Fails(ErrorCode.StorageError, "disk"));
        await h.Ui.RunAsync(() => c.Remove(Confirm));
        await h.IdleAsync();
        Assert.Equal(["Removing the account failed"], await h.Ui.RunAsync(() => toasts.ToArray()));
        await h.Ui.RunAsync(c.Stop);
    }

    /// <summary>Windows: the samples cannot remove an account or start a sign-in.</summary>
    [Fact]
    public async Task WithoutTheDaemonRemoveAndSignInSayThePreviewCannot()
    {
        using var h = new Harness();
        var src = Samples();
        var toasts = new List<string>();
        var asked = 0;
        var c = await h.Ui.RunAsync(() =>
        {
            var c = Account(src, Cal.SampleNextcloud, h.Pending);
            c.ToastRequested += (_, t) => toasts.Add(t);
            c.Remove(_ =>
            {
                asked++;
                return Task.FromResult((true, true));
            });
            return c;
        });
        var url = await h.Ui.InvokeAsync(() => c.RequestSignInUrlAsync());
        await h.IdleAsync();
        Assert.Equal(new SignInUrl.Failed(Cal.Text.PreviewOnly), url);
        Assert.Equal([Cal.Text.PreviewOnly], toasts);
        Assert.Equal(0, asked);
    }

    /// <summary>Windows: the sign-in page asks account.oauthStart for the calendar scopes; only an https page is handed out.</summary>
    [Fact]
    public async Task SignInUrlAsksForTheCalendar()
    {
        await using var h = await DaemonHarness.StartAsync();
        var page = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize?x=1";
        h.On(API.AccountOAuthStart.Name, _ => $$"""{"sessionId":"s1","authUrl":"{{page}}","expiresAt":"2026-10-16T10:00:00Z"}""");
        var client = await h.ConnectAsync();
        var src = Samples();
        var c = await h.Ui.RunAsync(() => Account(src, Cal.SampleWork, h.Pending, client));
        var url = await h.Ui.InvokeAsync(() => c.RequestSignInUrlAsync());
        Assert.Equal(new SignInUrl.Open(page), url);
        var sent = h.Params.Last<AccountOAuthStartParams>(API.AccountOAuthStart.Name);
        Assert.NotNull(sent);
        Assert.Equal(Cal.SampleWork, sent.AccountId);
        Assert.True(sent.Calendar);
        Assert.NotNull(sent.BrowserPage);

        page = "http://login.example.com/";
        Assert.IsType<SignInUrl.Failed>(await h.Ui.InvokeAsync(() => c.RequestSignInUrlAsync()));
        await h.Ui.RunAsync(c.Stop);
    }

    private sealed class Harness : IDisposable
    {
        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending);

        public void Dispose() => Ui.Dispose();
    }
}
