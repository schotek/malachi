// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/SyncStatusTests.swift, the
// counterpart of ui/internal/window/sync_test.go (TestSyncStatusText,
// TestCertBanner, TestAuthBannerText, TestAuthBannerTitleByKind) and
// status_test.go (TestAccountStatuses, TestAccountStatusesAccounts,
// TestStatusButtonLabel, TestSameAccounts). CertBannerTest and
// AuthBannerTitleByKindTest carry the Go cases the Swift suite does not
// have; TestStatusLineFor tests the sync controller's statusLineFor and is
// ported with it. A date before today is rendered with the machine's
// culture (its month names), so the cases that show one expect
// Format.FormatDate's own output where Go spells out "1 Sep"; times of
// today are culture-free.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Malachi.Core.Wizard;
using Xunit;
using static Malachi.Core.Tests.Model.SyncStatusFixtures;

namespace Malachi.Core.Tests.Model;

public sealed class SyncStatusTests
{
    private static readonly Account[] Accounts =
    [
        TestAccount("a1", name: "Work", email: "w@example.invalid"),
        TestAccount("a2", email: "home@example.invalid"),
        TestAccount("a3", enabled: false, name: "Paused"),
    ];

    [Fact]
    public void SyncStatusTextTest()
    {
        static string FolderName(AccountId acc, FolderId id) => acc.Value == "a1" && id.Value == "f_inbox" ? "Inbox" : "";
        var now = StatusNow();
        var yesterday = At(new DateTime(2026, 9, 1, 15, 30, 0));
        var twoHoursAgo = now.AddHours(-2);
        var hourAgo = now.AddHours(-1);
        var halfHourAgo = now.AddMinutes(-30);
        var minuteAgo = now.AddMinutes(-1);
        // The date of yesterday in this machine's culture.
        var upToDateYesterday = "Up to date · " + Format.FormatDate(yesterday, now);
        SyncState Idle(string acc, DateTimeOffset last) => State(acc, SyncStatus.Idle, last: last);
        // single is the first account alone: a state never names it.
        Account[] single = [Accounts[0]];

        (string Name, Dictionary<AccountId, SyncState> States, Account[] Accounts, string Want, bool Spinning)[] cases =
        [
            ("no accounts", St(), [], "", false),
            ("only disabled", St(State("a3", SyncStatus.Syncing)), Accounts[2..], "Paused", false),
            ("no states, idle from account.list", St(), Accounts, "Up to date", false),
            ("idle", St(State("a1", SyncStatus.Idle), State("a2", SyncStatus.Idle)), Accounts, "Up to date", false),
            ("syncing folder with progress", St(State("a1", SyncStatus.Syncing, folder: "f_inbox", progress: 42)), Accounts, "Syncing Inbox… 42 %", true),
            ("syncing folder without progress", St(State("a1", SyncStatus.Syncing, folder: "f_inbox")), Accounts, "Syncing Inbox…", true),
            ("syncing unknown folder falls back to the account", St(State("a1", SyncStatus.Syncing, folder: "f_gone", progress: 0)), Accounts, "Syncing Work… 0 %", true),
            ("syncing whole account, unnamed account uses the address", St(State("a2", SyncStatus.Syncing)), Accounts, "Syncing home@example.invalid…", true),
            ("syncing beats authRequired", St(State("a1", SyncStatus.AuthRequired), State("a2", SyncStatus.Syncing)), Accounts, "Syncing home@example.invalid…", true),
            ("account.list state used when uncached", St(), [TestAccount("a9", state: State("a9", SyncStatus.Offline))], "Offline, retrying", false),
            ("disabled account state is ignored", St(State("a3", SyncStatus.Error), State("a1", SyncStatus.Idle)), Accounts, "Up to date", false),
            ("state of an unknown account is ignored", St(State("zzz", SyncStatus.Error)), Accounts, "Up to date", false),

            // Sign-in required, error and offline name the one account in that
            // state among several enabled ones, by name or else by address.
            // Precedence: syncing > authRequired > error > offline.
            ("authRequired beats error, one of two named", St(State("a1", SyncStatus.Error), State("a2", SyncStatus.AuthRequired)), Accounts, "Sign-in required: home@example.invalid", false),
            ("error beats offline, one of two named", St(State("a1", SyncStatus.Offline), State("a2", SyncStatus.Error)), Accounts, "Sync error: home@example.invalid", false),
            ("offline beats idle, one of two named", St(State("a1", SyncStatus.Idle), State("a2", SyncStatus.Offline)), Accounts, "Offline: home@example.invalid", false),
            ("offline named by account name", St(State("a1", SyncStatus.Offline)), Accounts, "Offline: Work", false),
            ("two signing in: no name", St(State("a1", SyncStatus.AuthRequired), State("a2", SyncStatus.AuthRequired)), Accounts, "Sign-in required", false),
            ("two in error: no name", St(State("a1", SyncStatus.Error), State("a2", SyncStatus.Error)), Accounts, "Sync error", false),
            ("two offline: no name", St(State("a1", SyncStatus.Offline), State("a2", SyncStatus.Offline)), Accounts, "Offline, retrying", false),
            ("single account signing in: no name", St(State("a1", SyncStatus.AuthRequired)), single, "Sign-in required", false),
            ("single account in error: no name", St(State("a1", SyncStatus.Error)), single, "Sync error", false),
            ("single account offline: no name", St(State("a1", SyncStatus.Offline)), single, "Offline, retrying", false),
            // A paused account neither counts as a second one nor gets named.
            ("paused account does not count", St(State("a1", SyncStatus.Error)), [Accounts[0], Accounts[2]], "Sync error", false),
            ("paused account in error is not named", St(State("a3", SyncStatus.Error), State("a2", SyncStatus.Error)), Accounts, "Sync error: home@example.invalid", false),

            // Sending: the outbox count, summed over the enabled accounts, with
            // the spinner on. Precedence: syncing > authRequired > sending >
            // failed > error > offline.
            ("sending one", St(State("a1", SyncStatus.Idle, pending: 1)), Accounts, "Sending 1 message…", true),
            ("sending several", St(State("a1", SyncStatus.Idle, pending: 3)), Accounts, "Sending 3 messages…", true),
            ("sending summed over accounts", St(State("a1", SyncStatus.Idle, pending: 1), State("a2", SyncStatus.Idle, pending: 1)), Accounts, "Sending 2 messages…", true),
            ("syncing beats sending", St(State("a1", SyncStatus.Syncing, folder: "f_inbox", pending: 2)), Accounts, "Syncing Inbox…", true),
            ("authRequired beats sending", St(State("a1", SyncStatus.AuthRequired), State("a2", SyncStatus.Idle, pending: 1)), Accounts, "Sign-in required: Work", false),
            ("sending beats error", St(State("a1", SyncStatus.Error, pending: 1)), Accounts, "Sending 1 message…", true),
            ("sending beats offline", St(State("a1", SyncStatus.Offline), State("a2", SyncStatus.Idle, pending: 1)), Accounts, "Sending 1 message…", true),
            ("disabled account outbox is ignored", St(State("a3", SyncStatus.Idle, pending: 4), State("a1", SyncStatus.Idle)), Accounts, "Up to date", false),
            ("uncached account outbox from account.list", St(), [TestAccount("a9", state: State("a9", SyncStatus.Idle, pending: 1))], "Sending 1 message…", true),

            // Not sent: the failed outbox messages, summed like the pending
            // ones, without the spinner.
            ("failed one", St(State("a1", SyncStatus.Idle, failed: 1)), Accounts, "1 message not sent", false),
            ("failed summed over accounts", St(State("a1", SyncStatus.Idle, failed: 2), State("a2", SyncStatus.Idle, failed: 1)), Accounts, "3 messages not sent", false),
            ("sending beats failed", St(State("a1", SyncStatus.Idle, pending: 1, failed: 2)), Accounts, "Sending 1 message…", true),
            ("authRequired beats failed", St(State("a1", SyncStatus.Idle, failed: 1), State("a2", SyncStatus.AuthRequired)), Accounts, "Sign-in required: home@example.invalid", false),
            ("certificate beats failed", St(State("a1", SyncStatus.Idle, failed: 1), TlsState("a2", SyncStatus.Offline, TlsErrorReason.Untrusted)), Accounts, "Certificate problem", false),
            ("failed beats error", St(State("a1", SyncStatus.Error, failed: 1)), Accounts, "1 message not sent", false),
            ("failed beats offline", St(State("a1", SyncStatus.Offline), State("a2", SyncStatus.Idle, failed: 1)), Accounts, "1 message not sent", false),
            ("disabled account failed is ignored", St(State("a3", SyncStatus.Disabled, failed: 4), State("a1", SyncStatus.Idle)), Accounts, "Up to date", false),
            ("uncached account failed from account.list", St(), [TestAccount("a9", state: State("a9", SyncStatus.Idle, failed: 2))], "2 messages not sent", false),

            // Idle names the newest last check of the enabled accounts, as a
            // time today and as a date before.
            ("last check today", St(Idle("a1", twoHoursAgo)), Accounts, "Up to date · 13:30", false),
            ("newest last check wins", St(Idle("a1", twoHoursAgo), Idle("a2", hourAgo)), Accounts, "Up to date · 14:30", false),
            ("last check yesterday", St(Idle("a1", yesterday)), Accounts, upToDateYesterday, false),
            ("disabled account last check is ignored", St(Idle("a1", twoHoursAgo), Idle("a3", minuteAgo)), Accounts, "Up to date · 13:30", false),
            ("last check from account.list", St(), [TestAccount("a9", state: Idle("a9", halfHourAgo))], "Up to date · 15:00", false),
            ("a last check is no excuse for offline", St(Idle("a1", hourAgo), State("a2", SyncStatus.Offline, last: now)), Accounts, "Offline: home@example.invalid", false),
            // Go's zero time is no last check.
            ("a zero last check is none", St(Idle("a1", DateTimeOffset.MinValue)), Accounts, "Up to date", false),

            // Certificates: a refused or changed certificate is named instead
            // of offline, right after authRequired; a handshake failure stays
            // offline. The account's name is the certificate banner's to say.
            // Precedence: authRequired > changed > problem > sending.
            ("refused certificate", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted), State("a2", SyncStatus.Idle)), Accounts, "Certificate problem", false),
            ("changed certificate", St(TlsState("a2", SyncStatus.Offline, TlsErrorReason.PinMismatch)), Accounts, "Certificate changed", false),
            ("changed beats refused", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Expired), TlsState("a2", SyncStatus.Offline, TlsErrorReason.PinMismatch)), Accounts, "Certificate changed", false),
            ("authRequired beats a certificate", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted), State("a2", SyncStatus.AuthRequired)), Accounts, "Sign-in required: home@example.invalid", false),
            ("certificate beats sending", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted), State("a2", SyncStatus.Idle, pending: 1)), Accounts, "Certificate problem", false),
            ("certificate beats error", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted), State("a2", SyncStatus.Error)), Accounts, "Certificate problem", false),
            ("single account certificate", St(TlsState("a1", SyncStatus.Error, TlsErrorReason.Expired)), single, "Certificate problem", false),
            ("handshake stays offline", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Handshake)), Accounts, "Offline: Work", false),
            ("tlsError without details stays offline", St(new SyncState { AccountId = new AccountId("a1"), Status = SyncStatus.Offline, Error = TlsError((TlsErrorData?)null) }), Accounts, "Offline: Work", false),
            ("disabled account certificate is ignored", St(TlsState("a3", SyncStatus.Offline, TlsErrorReason.Untrusted)), Accounts, "Up to date", false),
        ];
        foreach (var (name, states, accounts, want, spinning) in cases)
        {
            var got = SyncStatusTexts.SyncStatusText(states, accounts, FolderName, now);
            Assert.True(got.Text == want && got.Spinning == spinning, $"{name}: got {got.Text}/{got.Spinning}");
        }

        // A null folderName falls back to the account name.
        var noNames = SyncStatusTexts.SyncStatusText(St(State("a1", SyncStatus.Syncing, folder: "f_inbox")), Accounts, null, now);
        Assert.Equal("Syncing Work…", noNames.Text);
    }

    // More refused certificates (certtrust.FromSyncState) than the Go table
    // has: syncing > authRequired > certificate changed > certificate
    // problem > sending > error > offline.
    [Fact]
    public void CertificateStatuses()
    {
        var idle = State("a2", SyncStatus.Idle);
        (string Name, Dictionary<AccountId, SyncState> States, string Want, bool Spinning)[] cases =
        [
            ("untrusted instead of offline", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted), idle), "Certificate problem", false),
            ("error status too", St(TlsState("a1", SyncStatus.Error, TlsErrorReason.Expired), idle), "Certificate problem", false),
            ("changed", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.PinMismatch), idle), "Certificate changed", false),
            ("changed beats problem", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted), TlsState("a2", SyncStatus.Offline, TlsErrorReason.PinMismatch)), "Certificate changed", false),
            ("handshake stays offline", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Handshake), idle), "Offline: Work", false),
            ("starttls stays offline", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.StarttlsUnavailable), idle), "Offline: Work", false),
            ("no details stays offline", St(new SyncState { AccountId = new AccountId("a1"), Status = SyncStatus.Offline, Error = TlsError((TlsErrorData?)null) }, idle), "Offline: Work", false),
            ("authRequired beats changed", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.PinMismatch), State("a2", SyncStatus.AuthRequired)), "Sign-in required: home@example.invalid", false),
            ("syncing beats the certificate", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted), State("a2", SyncStatus.Syncing)), "Syncing home@example.invalid…", true),
            ("certificate beats sending", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted), State("a2", SyncStatus.Idle, pending: 1)), "Certificate problem", false),
            ("certificate beats error", St(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted), State("a2", SyncStatus.Error)), "Certificate problem", false),
            ("disabled account ignored", St(TlsState("a3", SyncStatus.Offline, TlsErrorReason.PinMismatch), idle), "Up to date", false),
        ];
        foreach (var (name, states, want, spinning) in cases)
        {
            var got = SyncStatusTexts.SyncStatusText(states, Accounts, null, StatusNow());
            Assert.True(got.Text == want && got.Spinning == spinning, $"{name}: got {got.Text}/{got.Spinning}");
        }
    }

    [Fact]
    public void EditsPasswordTest()
    {
        Assert.True(SyncStatusTexts.EditsPassword(SignInKind.Password, ErrorCode.AuthRequired) && SyncStatusTexts.EditsPassword(SignInKind.Password, ErrorCode.AuthFailed));
        Assert.True(!SyncStatusTexts.EditsPassword(SignInKind.Password, ErrorCode.KeyringError) && !SyncStatusTexts.EditsPassword(SignInKind.Password, ErrorCode.NetworkError));
        Assert.True(!SyncStatusTexts.EditsPassword(SignInKind.OAuth, ErrorCode.AuthRequired) && !SyncStatusTexts.EditsPassword(SignInKind.Goa, ErrorCode.AuthFailed));
    }

    [Fact]
    public void CertTextsTest()
    {
        Assert.Equal("The certificate of Work is not trusted", SyncStatusTexts.CertBannerText(CertTrust.Category.Certificate, "Work"));
        Assert.Equal("The certificate of Work has changed", SyncStatusTexts.CertBannerText(CertTrust.Category.Changed, "Work"));
        Assert.Equal("Certificate problem", SyncStatusTexts.CertStatusText(CertTrust.Category.Certificate));
        Assert.Equal("Certificate changed", SyncStatusTexts.CertStatusText(CertTrust.Category.Changed));
    }

    [Fact]
    public void CertProblemAccountTest()
    {
        var bad = new SyncState { AccountId = new AccountId("a2"), Status = SyncStatus.Offline, Error = TlsError(TlsErrorReason.PinMismatch) };
        Account[] accounts =
        [
            TestAccount("a0", enabled: false, state: new SyncState { AccountId = new AccountId("a0"), Status = SyncStatus.Offline, Error = bad.Error }),
            TestAccount("a1"),
            TestAccount("a2"),
            TestAccount("a3", state: new SyncState { AccountId = new AccountId("a3"), Status = SyncStatus.Offline, Error = TlsError(TlsErrorReason.Expired) }),
        ];
        // The first enabled account in account order; the cached state wins
        // over account.list's.
        var got = SyncStatusTexts.CertProblemAccount(St(bad), accounts);
        Assert.True(got?.Account.Id == new AccountId("a2") && got?.Problem.Category == CertTrust.Category.Changed);
        Assert.Equal(new AccountId("a3"), SyncStatusTexts.CertProblemAccount(St(), accounts)?.Account.Id); // account.list state when uncached
        Assert.Null(SyncStatusTexts.CertProblemAccount(St(State("a3", SyncStatus.Idle)), accounts));
    }

    // sync_test.go TestCertBanner: a handshake failure is no certificate
    // problem, a paused account none either, and an account connected again
    // has none.
    [Fact]
    public void CertBannerTest()
    {
        Account[] accounts =
        [
            new Account { Id = new AccountId("a1"), Config = new AccountConfig { Name = "Paused", Email = "" }, Enabled = false, State = State("a1", SyncStatus.Idle) },
            new Account { Id = new AccountId("a2"), Config = new AccountConfig { Name = "Work", Email = "" }, Enabled = true, State = State("a2", SyncStatus.Idle) },
            new Account { Id = new AccountId("a3"), Config = new AccountConfig { Name = "", Email = "bridge@example.invalid" }, Enabled = true, State = State("a3", SyncStatus.Idle) },
        ];
        var states = St(
            TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted),
            TlsState("a2", SyncStatus.Offline, TlsErrorReason.Handshake),
            TlsState("a3", SyncStatus.Offline, TlsErrorReason.PinMismatch));
        var got = SyncStatusTexts.CertProblemAccount(states, accounts);
        Assert.NotNull(got);
        Assert.Equal(new AccountId("a3"), got.Value.Account.Id);
        Assert.Equal(CertTrust.Category.Changed, got.Value.Problem.Category);
        Assert.Equal("The certificate of bridge@example.invalid has changed",
            SyncStatusTexts.CertBannerText(got.Value.Problem.Category, AccountsPage.AccountRowTitle(got.Value.Account)));
        Assert.Equal("The certificate of Work is not trusted", SyncStatusTexts.CertBannerText(CertTrust.Category.Certificate, "Work"));
        // The first account in order wins; an uncached one uses account.list.
        states[new AccountId("a2")] = TlsState("a2", SyncStatus.Error, TlsErrorReason.Expired);
        Assert.Equal(new AccountId("a2"), SyncStatusTexts.CertProblemAccount(states, accounts)?.Account.Id);
        states.Remove(new AccountId("a2"));
        accounts[1] = accounts[1] with { State = TlsState("a2", SyncStatus.Offline, TlsErrorReason.Untrusted) };
        Assert.Equal(new AccountId("a2"), SyncStatusTexts.CertProblemAccount(states, accounts)?.Account.Id);
        // Connected again: no banner.
        states[new AccountId("a2")] = new SyncState { AccountId = new AccountId("a2"), Status = SyncStatus.Syncing, Error = accounts[1].State.Error };
        states[new AccountId("a3")] = State("a3", SyncStatus.Idle);
        Assert.Null(SyncStatusTexts.CertProblemAccount(states, accounts));
    }

    [Fact]
    public void AuthBannerTextTest()
    {
        var cases = new Dictionary<int, string>
        {
            [ErrorCode.AuthRequired] = "No password is stored for Work",
            [ErrorCode.AuthFailed] = "The server rejected the password of Work",
            [ErrorCode.KeyringError] = "The system keyring is unavailable; Work cannot sign in",
            [ErrorCode.NetworkError] = "Work needs attention",
            [0] = "Work needs attention",
        };
        foreach (var (reason, want) in cases)
        {
            Assert.True(want == SyncStatusTexts.AuthBannerText(reason, "Work"), $"AuthBannerText({reason})");
        }
        Assert.Equal("GNOME Online Accounts is not available; Work cannot sign in", SyncStatusTexts.GoaAuthBannerText(ErrorCode.Unavailable, "Work"));
        Assert.Equal("Sign in to Work again in Settings → Online Accounts", SyncStatusTexts.GoaAuthBannerText(ErrorCode.AuthRequired, "Work"));
        Assert.Equal("Sign in to Work again in your browser", SyncStatusTexts.OAuthAuthBannerText(ErrorCode.AuthRequired, "Work"));
        Assert.Equal("Sign in to Work again in your browser", SyncStatusTexts.OAuthAuthBannerText(ErrorCode.AuthFailed, "Work"));
        Assert.Equal("The system keyring is unavailable; Work cannot sign in", SyncStatusTexts.OAuthAuthBannerText(ErrorCode.KeyringError, "Work"));
        (SignInKind Kind, int Reason, string Want)[] buttons =
        [
            (SignInKind.Password, ErrorCode.AuthRequired, "_Edit Account…"),
            (SignInKind.Password, ErrorCode.AuthFailed, "_Edit Account…"),
            (SignInKind.Password, ErrorCode.KeyringError, "Open Preferences"),
            (SignInKind.Password, ErrorCode.NetworkError, "Open Preferences"),
            (SignInKind.Password, 0, "Open Preferences"),
            (SignInKind.Goa, ErrorCode.AuthRequired, "Open Online Accounts"),
            (SignInKind.OAuth, ErrorCode.AuthFailed, "Sign In"),
        ];
        foreach (var (kind, reason, want) in buttons)
        {
            Assert.True(want == SyncStatusTexts.AuthBannerButton(kind, reason), $"AuthBannerButton({kind}, {reason})");
            Assert.True(SyncStatusTexts.EditsPassword(kind, reason) == (want == "_Edit Account…"), $"EditsPassword({kind}, {reason})");
        }
    }

    // sync_test.go TestAuthBannerTitleByKind: the banner's sentence and
    // button by how the account signs in.
    [Theory]
    [InlineData(SignInKind.Password, ErrorCode.AuthRequired, "No password is stored for Work", "_Edit Account…")]
    [InlineData(SignInKind.Password, ErrorCode.AuthFailed, "The server rejected the password of Work", "_Edit Account…")]
    [InlineData(SignInKind.Password, ErrorCode.KeyringError, "The system keyring is unavailable; Work cannot sign in", "Open Preferences")]
    [InlineData(SignInKind.Password, ErrorCode.NetworkError, "Work needs attention", "Open Preferences")]
    [InlineData(SignInKind.Goa, ErrorCode.AuthRequired, "Sign in to Work again in Settings → Online Accounts", "Open Online Accounts")]
    [InlineData(SignInKind.Goa, ErrorCode.Unavailable, "GNOME Online Accounts is not available; Work cannot sign in", "Open Online Accounts")]
    [InlineData(SignInKind.OAuth, ErrorCode.AuthRequired, "Sign in to Work again in your browser", "Sign In")]
    [InlineData(SignInKind.OAuth, ErrorCode.AuthFailed, "Sign in to Work again in your browser", "Sign In")]
    [InlineData(SignInKind.OAuth, ErrorCode.NetworkError, "Sign in to Work again in your browser", "Sign In")]
    [InlineData(SignInKind.OAuth, ErrorCode.KeyringError, "The system keyring is unavailable; Work cannot sign in", "Sign In")]
    public void AuthBannerTitleByKindTest(SignInKind kind, int reason, string title, string button)
    {
        Assert.Equal(title, SyncStatusTexts.AuthBannerTitle(kind, reason, "Work"));
        Assert.Equal(button, SyncStatusTexts.AuthBannerButton(kind, reason));
        Assert.Equal(button == "_Edit Account…", SyncStatusTexts.EditsPassword(kind, reason));
    }

    // status_test.go

    [Fact]
    public void AccountStatusesTest()
    {
        var now = StatusNow();
        var yesterday = At(new DateTime(2026, 9, 1, 15, 30, 0));
        var twoHoursAgo = now.AddHours(-2);
        // The date of yesterday in this machine's culture.
        var lastSyncedYesterday = "Last synced " + Format.FormatDate(yesterday, now);
        static string FolderName(AccountId acc, FolderId id) => acc.Value == "a1" && id.Value == "f_inbox" ? "Inbox" : "";
        var work = TestAccount("a1", name: "Work", email: "w@example.invalid");
        SyncState S(string status, string? folder = null, int progress = -1, int pending = 0, int failed = 0, RpcError? error = null, DateTimeOffset? last = null) =>
            State("a1", status, folder, progress, pending, failed, last) with { Error = error };
        static SyncState WithOutbox(SyncState s, int pending, int failed) => s with { PendingOutbox = pending, FailedOutbox = failed };
        (string Name, SyncState State, string Detail, StatusAction Action, int Failed)[] cases =
        [
            // Syncing names the folder and its progress, never the account,
            // whose name is the row's title.
            ("syncing folder with progress", S(SyncStatus.Syncing, folder: "f_inbox", progress: 42), "Syncing Inbox… 42 %", StatusAction.Check, 0),
            ("syncing folder without progress", S(SyncStatus.Syncing, folder: "f_inbox"), "Syncing Inbox…", StatusAction.Check, 0),
            ("syncing the whole account", S(SyncStatus.Syncing, progress: 30), "Syncing…", StatusAction.Check, 0),
            ("syncing an unknown folder", S(SyncStatus.Syncing, folder: "f_gone", progress: 30), "Syncing…", StatusAction.Check, 0),
            ("syncing beats sign-in", S(SyncStatus.Syncing, pending: 1), "Syncing…", StatusAction.Check, 0),
            ("sign-in required", S(SyncStatus.AuthRequired, pending: 1), "Sign-in required", StatusAction.SignIn, 0),
            ("sign-in required, password refused", S(SyncStatus.AuthRequired, error: Error(ErrorCode.AuthFailed, "x")), "Sign-in required", StatusAction.SignIn, 0),
            // A refused or changed certificate says why and leads to the
            // account's settings, where it can be trusted.
            ("refused certificate", TlsState("a1", SyncStatus.Offline, TlsErrorReason.Untrusted), "The server's certificate is not from a trusted authority", StatusAction.Edit, 0),
            ("changed certificate", TlsState("a1", SyncStatus.Error, TlsErrorReason.PinMismatch), "The server presented a different certificate than the one you trust", StatusAction.Edit, 0),
            ("certificate beats sending", WithOutbox(TlsState("a1", SyncStatus.Offline, TlsErrorReason.Expired), 1, 0), "The server's certificate has expired", StatusAction.Edit, 0),
            ("sending one", S(SyncStatus.Idle, pending: 1), "Sending 1 message…", StatusAction.Check, 0),
            ("sending beats error", S(SyncStatus.Error, pending: 2), "Sending 2 messages…", StatusAction.Check, 0),
            // Error and offline give the reason when there is one, and a retry.
            ("error with reason", S(SyncStatus.Error, error: Error(ErrorCode.ServerError, "x")), "The server returned an error", StatusAction.Retry, 0),
            ("error without reason", S(SyncStatus.Error), "Sync error", StatusAction.Retry, 0),
            ("offline with reason", S(SyncStatus.Offline, error: Error(ErrorCode.NetworkError, "dial")), "The server could not be reached", StatusAction.Retry, 0),
            ("offline without reason", S(SyncStatus.Offline), "Offline, retrying", StatusAction.Retry, 0),
            ("handshake failure is offline", TlsState("a1", SyncStatus.Offline, TlsErrorReason.Handshake), "The secure connection could not be established", StatusAction.Retry, 0),
            ("backend detail in a reason", S(SyncStatus.Error, error: Error(ErrorCode.StorageError, "<b>boom</b>")), "Failed: <b>boom</b>", StatusAction.Retry, 0),
            // Idle names the last check, as a time today and a date before.
            ("idle, checked today", S(SyncStatus.Idle, last: twoHoursAgo), "Last synced 13:30", StatusAction.Check, 0),
            ("idle, checked yesterday", S(SyncStatus.Idle, last: yesterday), lastSyncedYesterday, StatusAction.Check, 0),
            ("idle, never checked", S(SyncStatus.Idle), "Up to date", StatusAction.Check, 0),
            ("idle, zero last check", S(SyncStatus.Idle, last: DateTimeOffset.MinValue), "Up to date", StatusAction.Check, 0),
            // Unsent messages are counted whatever the state.
            ("failed while idle", S(SyncStatus.Idle, failed: 2), "Up to date", StatusAction.Check, 2),
            ("failed while offline", S(SyncStatus.Offline, failed: 1), "Offline, retrying", StatusAction.Retry, 1),
            ("failed while sending", S(SyncStatus.Idle, pending: 1, failed: 1), "Sending 1 message…", StatusAction.Check, 1),
        ];
        foreach (var (name, s, detail, action, failed) in cases)
        {
            var got = SyncStatusTexts.AccountStatuses(St(s), [work], FolderName, now);
            Assert.True(got.Count == 1, $"{name}: {got.Count} rows");
            var g = got[0];
            Assert.True(g.Account == new AccountId("a1") && g.Title == "Work", $"{name}: {g}");
            Assert.True(g.Detail == detail && g.Action == action && g.Failed == failed,
                $"{name}: got {g.Detail} {g.Action} {g.Failed}, want {detail} {action} {failed}");
        }
        // A null folderName must not crash.
        var noNames = SyncStatusTexts.AccountStatuses(St(S(SyncStatus.Syncing, folder: "f_inbox", progress: 5)), [work], null, now);
        Assert.Equal("Syncing…", noNames[0].Detail);
    }

    [Fact]
    public void AccountStatusesAccounts()
    {
        var now = StatusNow();
        var graph = TestAccount("a4", name: "Graph");
        graph = graph with { Config = graph.Config with { Kind = AccountKind.Graph } };
        var goa = TestAccount("a5", name: "GOA");
        goa = goa with { Config = goa.Config with { Kind = AccountKind.Graph, Graph = new GraphConfig { Source = GraphSource.Goa } } };
        Account[] accounts =
        [
            TestAccount("a1", name: "Work", state: new SyncState { AccountId = new AccountId("a1"), Status = SyncStatus.Offline, FailedOutbox = 3 }),
            TestAccount("a2", enabled: false, email: "home@example.invalid",
                state: new SyncState { AccountId = new AccountId("a2"), Status = SyncStatus.Disabled, FailedOutbox = 1 }),
            TestAccount("a3", enabled: false, name: "Paused, fresh",
                state: new SyncState { AccountId = new AccountId("a3"), Status = SyncStatus.Disabled, FailedOutbox = 5 }),
            graph,
            goa,
            TestAccount("a6", name: "Home"),
            TestAccount("a7", name: "Away"),
        ];
        var states = St(
            // a1 has no cached state: account.list's is used.
            // a2 was paused while in error; the state from before the pause is
            // stale and must not show.
            new SyncState { AccountId = new AccountId("a2"), Status = SyncStatus.Error, FailedOutbox = 7 },
            // a3 changed its outbox after the pause: that state is newer.
            new SyncState { AccountId = new AccountId("a3"), Status = SyncStatus.Disabled, FailedOutbox = 0 },
            new SyncState { AccountId = new AccountId("a4"), Status = SyncStatus.AuthRequired },
            new SyncState { AccountId = new AccountId("a5"), Status = SyncStatus.AuthRequired },
            // A password account whose password was refused: the reason goes
            // with the sign-in action.
            new SyncState { AccountId = new AccountId("a6"), Status = SyncStatus.AuthRequired, Error = Error(ErrorCode.AuthFailed, "x") },
            // The reason is only kept for the sign-in action.
            new SyncState { AccountId = new AccountId("a7"), Status = SyncStatus.Offline, Error = Error(ErrorCode.NetworkError, "x") },
            // A state for an account that is not listed makes no row.
            new SyncState { AccountId = new AccountId("zzz"), Status = SyncStatus.Error });
        var got = SyncStatusTexts.AccountStatuses(states, accounts, null, now);
        AccountStatus[] want =
        [
            Status("a1", "Work", "Offline, retrying", StatusAction.Retry, SignInKind.Password, failed: 3),
            Status("a2", "home@example.invalid", "Paused", StatusAction.NoAction, SignInKind.Password, failed: 1),
            Status("a3", "Paused, fresh", "Paused", StatusAction.NoAction, SignInKind.Password, failed: 0),
            Status("a4", "Graph", "Sign-in required", StatusAction.SignIn, SignInKind.OAuth),
            Status("a5", "GOA", "Sign-in required", StatusAction.SignIn, SignInKind.Goa),
            Status("a6", "Home", "Sign-in required", StatusAction.SignIn, SignInKind.Password, reason: ErrorCode.AuthFailed),
            Status("a7", "Away", "The server could not be reached", StatusAction.Retry, SignInKind.Password),
        ];
        Assert.Equal(want, got);
        Assert.Empty(SyncStatusTexts.AccountStatuses(states, [], null, now)); // no accounts
    }

    public static TheoryData<AccountStatus, string> ButtonLabelCases => new()
    {
        { Status(StatusAction.NoAction), "" },
        { Status(StatusAction.Check), "" }, // an icon of its own
        { Status(StatusAction.Retry), "Try Again" },
        { Status(StatusAction.Edit), "_Edit Account…" },
        { Status(StatusAction.SignIn, SignInKind.Password), "Open Preferences" },
        { Status(StatusAction.SignIn, SignInKind.Goa), "Open Online Accounts" },
        { Status(StatusAction.SignIn, SignInKind.OAuth), "Sign In" },
        // A password account whose password is missing or refused asks for
        // it in the edit wizard, as the sign-in banner does.
        { Status(StatusAction.SignIn, SignInKind.Password, ErrorCode.AuthRequired), "_Edit Account…" },
        { Status(StatusAction.SignIn, SignInKind.Password, ErrorCode.AuthFailed), "_Edit Account…" },
        { Status(StatusAction.SignIn, SignInKind.Password, ErrorCode.KeyringError), "Open Preferences" },
        { Status(StatusAction.SignIn, SignInKind.OAuth, ErrorCode.AuthFailed), "Sign In" },
        { Status(StatusAction.SignIn, SignInKind.Goa, ErrorCode.AuthRequired), "Open Online Accounts" },
    };

    [Theory]
    [MemberData(nameof(ButtonLabelCases))]
    public void StatusButtonLabelTest(AccountStatus st, string want)
    {
        Assert.Equal(want, SyncStatusTexts.StatusButtonLabel(st));
        Assert.Equal(want == "_Edit Account…", SyncStatusTexts.StatusButtonMnemonic(st));
    }

    [Fact]
    public void SameAccountsTest()
    {
        AccountStatus[] list = [Status("a1", "", ""), Status("a2", "", "")];
        (string[] Order, bool Want)[] cases =
        [
            (["a1", "a2"], true),
            (["a2", "a1"], false), // reordered
            (["a1"], false), // added
            (["a1", "a2", "a3"], false),
            ([], false),
        ];
        foreach (var (order, want) in cases)
        {
            Assert.True(want == SyncStatusTexts.SameAccounts(order.Select(o => new AccountId(o)).ToList(), list), $"SameAccounts({string.Join(",", order)})");
        }
        Assert.True(SyncStatusTexts.SameAccounts([], []), "no accounts, no rows: nothing to rebuild");
    }

    [Fact]
    public void OutboxTexts()
    {
        Assert.Equal("Sending 1 message…", SyncStatusTexts.SendingText(1));
        Assert.Equal("Sending 4 messages…", SyncStatusTexts.SendingText(4));
        Assert.Equal("1 message not sent", SyncStatusTexts.NotSentText(1));
        Assert.Equal("3 messages not sent", SyncStatusTexts.NotSentText(3));
    }

    private static Dictionary<AccountId, SyncState> St(params SyncState[] states) =>
        states.ToDictionary(s => s.AccountId);

    private static SyncState State(
        string acc, string status, string? folder = null, int progress = -1, int pending = 0, int failed = 0,
        DateTimeOffset? last = null) =>
        new()
        {
            AccountId = new AccountId(acc),
            Status = status,
            FolderId = folder is null ? (FolderId?)null : new FolderId(folder),
            Progress = progress,
            LastSync = last,
            PendingOutbox = pending,
            FailedOutbox = failed,
        };

    private static RpcError Error(int code, string message) => new() { Code = code, Message = message };

    private static AccountStatus Status(
        string account, string title, string detail, StatusAction action = StatusAction.NoAction,
        SignInKind signIn = SignInKind.Password, int reason = 0, int failed = 0) =>
        new()
        {
            Account = new AccountId(account),
            Title = title,
            Detail = detail,
            Action = action,
            SignIn = signIn,
            Reason = reason,
            Failed = failed,
        };

    private static AccountStatus Status(StatusAction action, SignInKind signIn = SignInKind.Password, int reason = 0) =>
        Status("a", "", "", action, signIn, reason);
}
