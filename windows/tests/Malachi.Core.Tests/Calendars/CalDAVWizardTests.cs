// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalDAVWizardTests.swift; GTK:
// ui/internal/calendar/caldav_test.go (TestWizardFlow,
// TestWizardEditsAndErrors, TestWizardTrust), plus Swift's hosts.

using System;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Tests.Wizard;
using Xunit;
using W = Malachi.Core.Calendars.Cal.CalDAVWizard;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalDAVWizardTests
{
    private static readonly string TestSha = string.Concat(System.Linq.Enumerable.Repeat("ab", 32));

    private static RpcError TlsErr(TlsErrorReason reason, string sha) =>
        TlsErrors.Error(new TlsErrorData
        {
            Reason = reason,
            Certificate = sha.Length == 0 ? null : new CertificateInfo { Sha256 = sha, Issuer = "Home CA", SelfSigned = true },
        });

    private static RpcError Err(int code) => new() { Code = new ErrorCode(code), Message = "x" };

    private static AccountListCalDAVCalendarsResult Listed() => new()
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
            new() { Href = "/c/holidays/", Name = "Holidays", Access = CalendarAccess.Read },
        ],
    };

    private static AccountDiscoverCalDAVResult Found(string url, CalDAVFoundVia via, bool needs = false, CertificateInfo? cert = null) =>
        new() { ServerUrl = url, FoundVia = via, NeedsCredentials = needs, Certificate = cert };

    [Fact]
    public void WizardFlow()
    {
        var w = new W(new AccountId("acc_mail"));
        Assert.False(w.CanDiscover);
        Assert.False(w.BeginDiscover());
        Assert.False(w.CanAdd);
        w.Edit("", "cloud.example.org", "jana", "");
        Assert.False(w.CanDiscover); // without a password
        w.Edit("", "cloud.example.org", "jana", "secret");
        Assert.True(w.CanDiscover);
        Assert.True(w.BeginDiscover());
        Assert.Equal(W.WizardPhase.Discovering, w.Phase);
        Assert.Equal(W.WizardStep.Discover, w.Step);
        Assert.False(w.FieldsEnabled);
        Assert.Equal("cloud.example.org", w.DiscoverParams.Url);
        w.Edit("x", "other.example.org", "x", "x");
        Assert.Equal("cloud.example.org", w.Server); // fields changed while discovering
        Assert.Equal("Looking for calendars…", w.Progress);
        Assert.False(w.CanAdd);
        w.ListDone(Listed(), null); // out of order: ignored
        Assert.Null(w.Listed);
        w.DiscoverDone(
            Found("https://dav.example.net", CalDAVFoundVia.WellKnown, needs: true, cert: new CertificateInfo { Sha256 = TestSha, Issuer = "R11" }),
            null);
        Assert.Equal(W.WizardPhase.Discovering, w.Phase);
        Assert.Equal(W.WizardStep.List, w.Step);
        Assert.Equal("The calendars are at dav.example.net.", w.ServerChanged);
        Assert.Equal("Verified (R11)", w.CertificateText);
        var lp = w.ListParams;
        Assert.Equal(AccountKind.CalDAV, lp.Config.Kind);
        Assert.Equal("https://dav.example.net", lp.Config.CalDAV?.ServerUrl);
        Assert.Equal("jana", lp.Config.CalDAV?.Username);
        Assert.Equal("secret", lp.Credentials.Password);
        Assert.Equal("cloud.example.org", lp.Config.Name);
        w.ListDone(Listed(), null);
        Assert.Equal(W.WizardPhase.Listed, w.Phase);
        Assert.Equal(3, w.Selected.Count);
        Assert.Equal("Jana", w.Name);
        Assert.True(w.ShowsMailAccount);
        Assert.True(w.CanAdd);
        Assert.Equal("Found through /.well-known/caldav · 3 calendars", w.FoundLine);
        w.Toggle("/c/holidays/", false);
        w.Toggle("/nowhere/", true);
        Assert.False(w.IsSelected("/nowhere/")); // a calendar that was not listed
        Assert.False(w.Selected.ContainsKey("/nowhere/"));
        w.Toggle("/c/personal/", false);
        w.Toggle("/c/work/", false);
        Assert.False(w.CanAdd); // without a calendar
        Assert.False(w.BeginAdd());
        w.Toggle("/c/personal/", true);
        w.Toggle("/c/work/", true);
        Assert.True(w.BeginAdd());
        Assert.Equal(W.WizardPhase.Adding, w.Phase);
        Assert.Equal("Adding the account…", w.Progress);
        var ap = w.AddParams;
        Assert.Equal("Jana", ap.Config.Name);
        Assert.Equal("jana@example.org", ap.Config.Email);
        Assert.Equal(new AccountId("acc_mail"), ap.Config.CalDAV?.MailAccountId);
        Assert.Equal("https://dav.example.net/p/", ap.Config.CalDAV?.PrincipalUrl);
        Assert.NotNull(ap.Config.Calendar);
        Assert.True(ap.Config.Calendar.Sync);
        Assert.Equal(["/c/holidays/"], ap.Config.Calendar.DisabledCalendars!);
        Assert.Equal("/c/personal/", ap.Config.Calendar.DefaultCalendar);
        Assert.Equal("secret", ap.Credentials.Password);
        w.AddDone(null, Err(ErrorCode.AuthFailed));
        Assert.Equal(W.WizardPhase.Listed, w.Phase);
        Assert.Equal("The server did not accept the username or password.", w.ErrorText);
        w.BeginAdd();
        w.AddDone(new AccountId("acc_new"), null);
        Assert.Equal(W.WizardPhase.Added, w.Phase);
        Assert.Equal(new AccountId("acc_new"), w.AccountId);
        Assert.Equal("", w.Password);
    }

    [Fact]
    public void WizardEditsAndErrors()
    {
        var w = new W(null);
        w.Edit("Home", "cloud.example.org", "jana", "pw");
        w.BeginDiscover();
        w.DiscoverDone(null, Err(ErrorCode.NetworkError));
        Assert.Equal(W.WizardPhase.Entering, w.Phase);
        Assert.Equal("The server could not be reached.", w.ErrorText);
        Assert.True(w.CanDiscover);
        w.BeginDiscover();
        w.DiscoverDone(Found("https://cloud.example.org/dav", CalDAVFoundVia.Srv), null);
        w.ListDone(null, Err(ErrorCode.ServerError));
        Assert.Equal(W.WizardPhase.Entering, w.Phase);
        Assert.Null(w.Listed);
        Assert.Equal("The server answered, but it is not a CalDAV server.", w.ErrorText);
        w.BeginDiscover();
        w.DiscoverDone(Found("https://cloud.example.org/dav", CalDAVFoundVia.Srv), null);
        w.ListDone(Listed() with { Scheduling = CalDAVScheduling.Server }, null);
        Assert.Equal("Home", w.Name);
        Assert.False(w.ShowsMailAccount);
        Assert.Equal("Found through a DNS SRV record · 3 calendars", w.FoundLine);
        w.Edit("Home 2", "cloud.example.org", "jana", "pw");
        Assert.Equal(W.WizardPhase.Listed, w.Phase); // renaming keeps the listing
        Assert.NotNull(w.Listed);
        w.Edit("Home 2", "cloud.example.org", "jana2", "pw");
        Assert.Equal(W.WizardPhase.Entering, w.Phase); // a new username drops it
        Assert.Null(w.Listed);
        Assert.Null(w.Discovered);
        Assert.Empty(w.Selected);
        Assert.Null(w.AddParams.Config.CalDAV?.MailAccountId); // no mail account without client scheduling
    }

    public static TheoryData<string, RpcError, string, bool> Offers() => new()
    {
        { "handshake", TlsErr(TlsErrorReason.Handshake, TestSha), "cloud.example.org", false },
        { "no certificate", TlsErr(TlsErrorReason.Untrusted, ""), "cloud.example.org", false },
        { "bad fingerprint", TlsErr(TlsErrorReason.Untrusted, "xyz"), "cloud.example.org", false },
        { "pin mismatch", TlsErr(TlsErrorReason.PinMismatch, TestSha), "cloud.example.org", true },
        { "plain http", TlsErr(TlsErrorReason.Untrusted, TestSha), "http://127.0.0.1:5232", false },
        { "another error", Err(ErrorCode.NetworkError), "cloud.example.org", false },
    };

    [Fact]
    public void WizardTrust()
    {
        var w = new W(null);
        w.Edit("", "cloud.example.org", "jana", "pw");
        w.BeginDiscover();
        w.DiscoverDone(null, TlsErr(TlsErrorReason.Untrusted, TestSha));
        var p = w.TrustOffer;
        Assert.NotNull(p);
        Assert.Equal(TestSha, p.Cert?.Sha256);
        Assert.Equal("The server’s certificate is not trusted.", w.ErrorText);
        Assert.True(w.Trust());
        Assert.Equal(TestSha, w.Pin);
        Assert.Null(w.Error);
        Assert.Equal(W.WizardPhase.Discovering, w.Phase);
        Assert.Equal(W.WizardStep.Discover, w.Step);
        Assert.Equal("Trusted by you", w.CertificateText);
        Assert.Null(w.TrustOffer); // no offer after trusting
        // Discovery runs again with the pin, which goes on into the listing
        // and the account.
        Assert.Equal(TestSha, w.DiscoverParams.CertificateSha256);
        Assert.Equal("cloud.example.org", w.DiscoverParams.Url);
        w.DiscoverDone(Found("https://cloud.example.org/remote.php/dav", CalDAVFoundVia.WellKnown), null);
        var lp = w.ListParams;
        Assert.Equal(TestSha, lp.Config.CalDAV?.CertificateSha256);
        Assert.Equal("https://cloud.example.org/remote.php/dav", lp.Config.CalDAV?.ServerUrl);
        w.ListDone(Listed(), null);
        w.BeginAdd();
        Assert.Equal(TestSha, w.AddParams.Config.CalDAV?.CertificateSha256);
        w.AddDone(null, Err(ErrorCode.ServerError));
        // A redirect to another host does not take the pin along.
        w.Edit(w.Name, w.Server, w.Username, "pw2");
        w.BeginDiscover();
        w.DiscoverDone(Found("https://elsewhere.example.net/dav", CalDAVFoundVia.WellKnown), null);
        Assert.Null(w.ListParams.Config.CalDAV?.CertificateSha256);
        w.ListDone(null, TlsErr(TlsErrorReason.Untrusted, TestSha));
        // The same certificate refused again offers nothing.
        w.ListDone(null, TlsErr(TlsErrorReason.Untrusted, TestSha));
        Assert.Null(w.TrustOffer);
        // Another host forgets the pin; the same host keeps it.
        w.Edit("", "https://cloud.example.org/remote.php/dav", "jana", "pw");
        Assert.Equal(TestSha, w.Pin);
        w.Edit("", "evil.example.net", "jana", "pw");
        Assert.Equal("", w.Pin);
    }

    [Theory]
    [MemberData(nameof(Offers))]
    public void WizardTrustOffers(string name, RpcError error, string url, bool ok)
    {
        var w = new W(null);
        w.Edit("", url, "u", "p");
        w.BeginDiscover();
        w.DiscoverDone(null, error);
        Assert.True((w.TrustOffer is not null) == ok, name);
    }

    [Theory]
    [InlineData("cloud.example.org", "cloud.example.org")]
    [InlineData(" https://Cloud.example.org:8443/dav ", "Cloud.example.org")]
    [InlineData("http://[::1]:5232/", "::1")]
    [InlineData("", "")]
    [InlineData("a b", "")]
    [InlineData("https://user@dav.example.net/x?y#z", "dav.example.net")]
    [InlineData("cloud.example.org:abc", "")]
    public void Hosts(string typed, string want)
    {
        Assert.Equal(want, W.Host(typed));
    }

    [Fact]
    public void TypedUrls()
    {
        Assert.Equal("https://cloud.example.org/dav", W.TypedUrl("cloud.example.org/dav//"));
        Assert.Equal("http://127.0.0.1:5232", W.TypedUrl(" http://127.0.0.1:5232/ "));
    }

    // Windows-only: the texts the flow tests do not reach, and Clone.

    [Fact]
    public void Texts()
    {
        Assert.Equal("Add CalDAV Calendar", W.Title);
        Assert.Equal("Server Address", W.FieldLabel(W.Field.Server));
        Assert.Equal("Username", W.FieldLabel(W.Field.Username));
        Assert.Equal("Password", W.FieldLabel(W.Field.Password));
        Assert.Equal("Account Name", W.FieldLabel(W.Field.Name));
        Assert.Equal("the address you entered", W.FoundVia(CalDAVFoundVia.Url));
        Assert.Equal("default", W.ListedNote(new CalDAVCalendar { Href = "/a/", Name = "A", Access = CalendarAccess.Owner, IsDefault = true }));
        Assert.Equal("read only", W.ListedNote(new CalDAVCalendar { Href = "/a/", Name = "A", Access = CalendarAccess.Read }));
        Assert.Equal("Add", W.AddButton);
        Assert.Equal("The calendars could not be found.", WithError(Err(ErrorCode.InternalError)).ErrorText);
        Assert.Equal("The password could not be stored in the system keyring.", WithError(Err(ErrorCode.KeyringError)).ErrorText);
        Assert.Equal("This is not a server address Malachi Mail can use.", WithError(Err(ErrorCode.InvalidArgument)).ErrorText);
        Assert.Equal("", new W(null).ErrorText);
        Assert.Equal("", new W(null).CertificateText);
        // A plain http server that discovery found is said to be unencrypted.
        var local = new W(null);
        local.Edit("", "http://127.0.0.1:5232", "u", "p");
        local.BeginDiscover();
        local.DiscoverDone(Found("http://127.0.0.1:5232/", CalDAVFoundVia.Url), null);
        Assert.Equal("Not encrypted (local server)", local.CertificateText);
        Assert.Equal("", local.ServerChanged);
    }

    [Fact]
    public void CloneKeepsItsOwnSelection()
    {
        var w = new W(null);
        w.Edit("", "cloud.example.org", "jana", "pw");
        w.BeginDiscover();
        w.DiscoverDone(Found("https://cloud.example.org/dav", CalDAVFoundVia.Url), null);
        w.ListDone(Listed(), null);
        var copy = w.Clone();
        w.Toggle("/c/work/", false);
        Assert.True(copy.IsSelected("/c/work/"));
        Assert.False(w.IsSelected("/c/work/"));
    }

    private static W WithError(RpcError e)
    {
        var w = new W(null);
        w.Edit("", "cloud.example.org", "u", "p");
        w.BeginDiscover();
        w.DiscoverDone(null, e);
        return w;
    }
}
