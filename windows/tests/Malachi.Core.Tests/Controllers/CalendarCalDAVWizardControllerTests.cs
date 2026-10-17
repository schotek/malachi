// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarCalDAVWizardControllerTests.swift.
// The Add CalDAV Calendar sheet's controller (CalDAVWizardController) over
// the in-memory source: discover → list → add with their params, the
// mail-account popup, a refused discovery, and the certificate trust that
// runs discovery again with the pin only after the confirmation. Swift's
// calWaitUntil polls and its 10 ms negative sleep are Quiescence.IdleAsync
// here; Swift's `src.update { _ in }` is a real change of the snapshot,
// because the C# source tells no one of an unchanged one. The last test is
// Windows's: a cancelled sheet sends no discovery, while an Add already
// started still reaches the source (its answer is dropped).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Wizard;
using Malachi.Core.Transport;
using Malachi.Core.Wizard;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;
using Phase = Malachi.Core.Calendars.Cal.CalDAVWizard.WizardPhase;
using Write = Malachi.Core.Calendars.InMemoryCalendarSource.Write;

namespace Malachi.Core.Tests.Controllers;

public sealed class CalendarCalDAVWizardControllerTests
{
    private static readonly string Sha = string.Concat(Enumerable.Repeat("cd", 32));

    /// <summary>Wednesday 14 October 2026, 10:17 in Prague (Swift <c>calNow</c>).</summary>
    private static readonly DateTimeOffset Now = F.At(2026, 10, 14, 10, 17);

    /// <summary>The samples with emulated writes (Swift <c>calSamples(writes:)</c>).</summary>
    private static InMemoryCalendarSource Samples(bool writes = true)
    {
        var s = InMemoryCalendarSource.Samples(Now, F.Prague);
        s.EmulatesWrites = writes;
        return s;
    }

    // Swift's wizard(_:): the source's answers, and the sheet with its fields typed.
    private static CalDAVWizardController Wizard(InMemoryCalendarSource src, PendingWork pending)
    {
        src.DiscoverResult = new AccountDiscoverCalDAVResult
        {
            ServerUrl = "https://dav.example.net/remote.php/dav",
            FoundVia = CalDAVFoundVia.WellKnown,
            NeedsCredentials = true,
        };
        src.ListResult = new AccountListCalDAVCalendarsResult
        {
            PrincipalUrl = "https://dav.example.net/p/",
            HomeSetUrl = "https://dav.example.net/c/",
            DisplayName = "Jana",
            Addresses = ["jana@example.org"],
            Scheduling = CalDAVScheduling.Client,
            Calendars =
            [
                new() { Href = "/c/personal/", Name = "Personal", Access = CalendarAccess.Owner, IsDefault = true },
                new() { Href = "/c/work/", Name = "Work", Access = CalendarAccess.Owner },
            ],
        };
        src.AddedAccount = new AccountId("acc_dav");
        var w = new CalDAVWizardController(src, pending: pending);
        w.Edit("", "dav.example.net", "jana", "secret");
        return w;
    }

    private static RpcException Daemon(int code, string message) => new(new RpcError { Code = code, Message = message });

    [Fact]
    public async Task DiscoverListAndAdd()
    {
        using var h = new Harness();
        var src = Samples();
        var w = await h.Ui.RunAsync(() => Wizard(src, h.Pending));
        await h.Ui.RunAsync(() =>
        {
            // The mail accounts that can send: Work and Gmail, not Nextcloud.
            Assert.Equal([Cal.SampleWork, Cal.SampleGmail], w.MailAccounts.Select(a => a.Id));
            Assert.Equal(Cal.SampleWork, w.Wizard.MailAccount);
            Assert.True(w.Wizard.CanDiscover);
            w.Discover();
        });
        await h.IdleAsync();
        var done = new List<AccountId>();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Phase.Listed, w.Wizard.Phase);
            Assert.Equal("Found through /.well-known/caldav · 2 calendars", w.Wizard.FoundLine);
            Assert.True(w.Wizard.ShowsMailAccount);
            w.SetMailAccount(Cal.SampleGmail);
            w.Toggle("/c/work/", false);
            w.Done += (_, id) => done.Add(id);
            w.Add();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal([new AccountId("acc_dav")], done);
            Assert.Equal(Phase.Added, w.Wizard.Phase);
            Assert.Equal(3, src.Writes.Count);
            var d = Assert.IsType<Write.Discover>(src.Writes[0]).Parameters;
            var l = Assert.IsType<Write.ListCalDAV>(src.Writes[1]).Parameters;
            var a = Assert.IsType<Write.AddAccount>(src.Writes[2]).Parameters;
            Assert.Equal("dav.example.net", d.Url);
            Assert.Null(d.CertificateSha256);
            Assert.Equal("https://dav.example.net/remote.php/dav", l.Config.CalDAV?.ServerUrl);
            Assert.Equal("secret", l.Credentials.Password);
            Assert.Equal(AccountKind.CalDAV, a.Config.Kind);
            Assert.Equal("Jana", a.Config.Name);
            Assert.Equal("jana@example.org", a.Config.Email);
            Assert.Equal(Cal.SampleGmail, a.Config.CalDAV?.MailAccountId);
            Assert.Equal(["/c/work/"], a.Config.Calendar?.DisabledCalendars ?? []);
            Assert.Equal("/c/personal/", a.Config.Calendar?.DefaultCalendar);
        });
    }

    [Fact]
    public async Task RefusedDiscoveryGoesBackToEntering()
    {
        using var h = new Harness();
        var src = Samples();
        src.Failures[API.AccountDiscoverCalDAV.Name] = Daemon(ErrorCode.ServerError, "not caldav");
        var w = await h.Ui.RunAsync(() => Wizard(src, h.Pending));
        await h.Ui.RunAsync(w.Discover);
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Phase.Entering, w.Wizard.Phase);
            Assert.NotNull(w.Wizard.Error);
            Assert.Equal("The server answered, but it is not a CalDAV server.", w.ErrorText);
        });
        var preview = await h.Ui.RunAsync(() => Wizard(Samples(writes: false), h.Pending));
        await h.Ui.RunAsync(preview.Discover);
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Phase.Entering, preview.Wizard.Phase);
            Assert.True(preview.PreviewRefused);
            Assert.Equal(Cal.Text.PreviewOnly, preview.ErrorText);
        });
    }

    [Fact]
    public async Task TrustRunsDiscoveryAgainWithThePin()
    {
        using var h = new Harness();
        var src = Samples();
        var d = new TlsErrorData
        {
            Reason = TlsErrorReason.Untrusted,
            Certificate = new CertificateInfo
            {
                Sha256 = Sha,
                Issuer = "Home CA",
                NotBefore = DateTimeOffset.UnixEpoch,
                NotAfter = DateTimeOffset.UnixEpoch,
                SelfSigned = true,
            },
        };
        src.Failures[API.AccountDiscoverCalDAV.Name] = new RpcException(TlsErrors.Error(d));
        var w = await h.Ui.RunAsync(() => Wizard(src, h.Pending));
        await h.Ui.RunAsync(w.Discover);
        await h.IdleAsync();
        Assert.NotNull(await h.Ui.RunAsync(() => w.Wizard.TrustOffer));
        // Refused: nothing pinned.
        var prompts = new List<TrustPrompt>();
        var answer = false;
        await h.Ui.RunAsync(() =>
        {
            w.ConfirmTrust = p =>
            {
                prompts.Add(p);
                return Task.FromResult(answer);
            };
            w.Trust();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Single(prompts);
            Assert.Single(src.Writes);
            Assert.Empty(w.Wizard.Pin);
            // Confirmed: discovery again with the pin, then the list.
            src.Failures.Remove(API.AccountDiscoverCalDAV.Name);
            answer = true;
            w.Trust();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Phase.Listed, w.Wizard.Phase);
            Assert.Equal(2, prompts.Count);
            var again = Assert.IsType<Write.Discover>(src.Writes[1]).Parameters;
            Assert.Equal(Sha, again.CertificateSha256);
            Assert.Equal("Trusted by you", w.Wizard.CertificateText);
        });
    }

    [Fact]
    public async Task ListedCalendarsAndTheMailAccountDefault()
    {
        using var h = new Harness();
        var src = Samples();
        var w = await h.Ui.RunAsync(() => Wizard(src, h.Pending));
        await h.Ui.RunAsync(() =>
        {
            w.Start();
            w.Discover();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Phase.Listed, w.Wizard.Phase);
            Assert.Equal(["Personal", "Work"], w.ListedCalendars.Select(c => c.Name));
            Assert.Equal(["default", ""], w.ListedCalendars.Select(c => c.Note));
            Assert.All(w.ListedCalendars, c => Assert.True(c.Selected));
            w.Toggle("/c/work/", false);
            Assert.Equal([true, false], w.ListedCalendars.Select(c => c.Selected));
            // None chosen stays None when the snapshot changes.
            w.SetMailAccount(null);
            src.Update(s => s with { Unanswered = s.Unanswered + 1 });
            Assert.Null(w.Wizard.MailAccount);
            w.Cancel();
        });
        await h.IdleAsync();
    }

    /// <summary>Windows: a cancelled sheet sends no discovery; an Add already started still reaches the source, its answer dropped.</summary>
    [Fact]
    public async Task CancelSendsNoDiscoveryButKeepsAnAdd()
    {
        using var h = new Harness();
        var src = Samples();
        var w = await h.Ui.RunAsync(() => Wizard(src, h.Pending));
        await h.Ui.RunAsync(() =>
        {
            w.Discover();
            w.Cancel();
        });
        await h.IdleAsync();
        Assert.Empty(await h.Ui.RunAsync(() => src.Writes.ToList()));

        var other = await h.Ui.RunAsync(() => Wizard(src, h.Pending));
        var changes = 0;
        var done = 0;
        await h.Ui.RunAsync(() => other.Discover());
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(Phase.Listed, other.Wizard.Phase);
            other.Changed += (_, _) => changes++;
            other.Done += (_, _) => done++;
            other.Add();
            changes = 0;
            other.Cancel();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.IsType<Write.AddAccount>(src.Writes[^1]);
            Assert.Equal(0, changes);
            Assert.Equal(0, done);
            Assert.True(other.IsClosed);
        });
    }

    private sealed class Harness : IDisposable
    {
        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending);

        public void Dispose() => Ui.Dispose();
    }
}
