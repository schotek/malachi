// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AccountsPageTests.swift, the
// counterpart of ui/internal/window/accounts_page_test.go
// (TestAccountRowTitle, TestAccountStatusText, TestInsertIndex,
// TestMoveAccount); the Go cases of TestAccountStatusText the Swift suite
// does not carry (other, serverTimeout, an unknown code, idle with an
// error) are in AccountStatusTextGoCases.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.SyncStatusFixtures;

namespace Malachi.Core.Tests.Model;

public sealed class AccountsPageTests
{
    [Fact]
    public void AccountRowTitleTest()
    {
        var a = TestAccount("a", name: "Work", email: "me@example.invalid");
        Assert.Equal("Work", AccountsPage.AccountRowTitle(a));
        a = a with { Config = a.Config with { Name = "" } };
        Assert.Equal("me@example.invalid", AccountsPage.AccountRowTitle(a));
        // The name as typed: nothing is trimmed.
        a = a with { Config = a.Config with { Name = " Work " } };
        Assert.Equal(" Work ", AccountsPage.AccountRowTitle(a));
    }

    [Fact]
    public void AccountStatusTextTest()
    {
        var cases = new Dictionary<string, string>
        {
            [SyncStatus.Idle] = "",
            [SyncStatus.Disabled] = "Paused",
            [SyncStatus.Syncing] = "Syncing…",
            [SyncStatus.Offline] = "Offline",
            [SyncStatus.AuthRequired] = "Sign-in required",
            [SyncStatus.Error] = "Error",
            ["unknown"] = "",
        };
        foreach (var (status, want) in cases)
        {
            Assert.True(want == AccountsPage.AccountStatusText(State(status)), status);
        }
        // A refused certificate says so instead of "Offline" or "Error"; a
        // handshake failure stays "Offline".
        Assert.Equal("Certificate problem", AccountsPage.AccountStatusText(Tls(SyncStatus.Offline, TlsErrorReason.Untrusted)));
        Assert.Equal("Certificate problem", AccountsPage.AccountStatusText(Tls(SyncStatus.Error, TlsErrorReason.HostnameMismatch)));
        Assert.Equal("Certificate changed", AccountsPage.AccountStatusText(Tls(SyncStatus.Offline, TlsErrorReason.PinMismatch)));
        Assert.Equal("Certificate problem", AccountsPage.AccountStatusText(Tls(SyncStatus.Offline, TlsErrorReason.Expired)));
        Assert.Equal("Offline: The secure connection could not be established",
            AccountsPage.AccountStatusText(Tls(SyncStatus.Offline, TlsErrorReason.Handshake)));
        Assert.Equal("Offline: The server does not offer STARTTLS",
            AccountsPage.AccountStatusText(Tls(SyncStatus.Offline, TlsErrorReason.StarttlsUnavailable)));
        Assert.Equal("Paused", AccountsPage.AccountStatusText(Tls(SyncStatus.Disabled, TlsErrorReason.Untrusted)));
        Assert.Equal("Syncing…", AccountsPage.AccountStatusText(Tls(SyncStatus.Syncing, TlsErrorReason.Untrusted)));
        Assert.Equal("Offline: The secure connection could not be established",
            AccountsPage.AccountStatusText(State(SyncStatus.Offline, TlsError((TlsErrorData?)null))));
        // Any other problem says why too; without an error the bare status.
        Assert.Equal("Offline: The server could not be reached", AccountsPage.AccountStatusText(Failed(SyncStatus.Offline, ErrorCode.NetworkError)));
        Assert.Equal("Offline: No network connection", AccountsPage.AccountStatusText(Failed(SyncStatus.Offline, ErrorCode.Offline)));
        Assert.Equal("Offline: The system keyring is unavailable", AccountsPage.AccountStatusText(Failed(SyncStatus.Offline, ErrorCode.KeyringError)));
        Assert.Equal("Error: The server returned an error", AccountsPage.AccountStatusText(Failed(SyncStatus.Error, ErrorCode.ServerError)));
        Assert.Equal("Error: Failed: technical detail", AccountsPage.AccountStatusText(Failed(SyncStatus.Error, ErrorCode.StorageError)));
        Assert.Equal("Sign-in required", AccountsPage.AccountStatusText(Failed(SyncStatus.AuthRequired, ErrorCode.AuthRequired)));
        Assert.Equal("Paused", AccountsPage.AccountStatusText(Failed(SyncStatus.Disabled, ErrorCode.NetworkError)));
        Assert.Equal("Syncing…", AccountsPage.AccountStatusText(Failed(SyncStatus.Syncing, ErrorCode.NetworkError)));
    }

    // accounts_page_test.go TestAccountStatusText: the cases the Swift suite
    // does not have.
    [Fact]
    public void AccountStatusTextGoCases()
    {
        Assert.Equal("Certificate problem", AccountsPage.AccountStatusText(Tls(SyncStatus.Error, TlsErrorReason.Other)));
        Assert.Equal("Offline: The server did not respond in time", AccountsPage.AccountStatusText(Failed(SyncStatus.Offline, ErrorCode.ServerTimeout)));
        Assert.Equal("Error: The system keyring is unavailable", AccountsPage.AccountStatusText(Failed(SyncStatus.Error, ErrorCode.KeyringError, "locked")));
        Assert.Equal("Error: Failed: odd", AccountsPage.AccountStatusText(Failed(SyncStatus.Error, 9999, "odd")));
        Assert.Equal("Sign-in required", AccountsPage.AccountStatusText(Failed(SyncStatus.AuthRequired, ErrorCode.AuthFailed)));
        Assert.Equal("", AccountsPage.AccountStatusText(Failed(SyncStatus.Idle, ErrorCode.NetworkError)));
    }

    [Fact]
    public void AccountRowOffersSignInTest()
    {
        var oauth = new ServerConfig
        {
            Host = "imap.gmail.com",
            Port = 993,
            Security = Security.Tls,
            Username = "me@gmail.com",
            AuthMethod = AuthMethod.OAuth2,
        };
        var daemon = new AccountConfig
        {
            Name = "Gmail",
            Email = "me@gmail.com",
            Imap = oauth,
            Smtp = oauth,
            OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google },
        };
        var graph = new AccountConfig
        {
            Name = "Work",
            Email = "me@contoso.com",
            Kind = AccountKind.Graph,
            OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Office365 },
            Graph = new GraphConfig { Source = GraphSource.Daemon },
        };
        var goa = new AccountConfig
        {
            Name = "Gmail",
            Email = "me@gmail.com",
            Imap = oauth,
            Smtp = oauth,
            OAuth2 = new OAuth2Config { Source = OAuth2Source.Goa, GoaAccountId = "account_1", Provider = OAuth2Provider.Google },
        };
        var password = TestAccount("p", name: "Home", email: "me@example.invalid").Config;
        (string Name, AccountConfig Cfg, string Status, bool Want)[] cases =
        [
            ("browser sign-in needs a sign-in", daemon, SyncStatus.AuthRequired, true),
            ("graph through the daemon", graph, SyncStatus.AuthRequired, true),
            ("browser sign-in, idle", daemon, SyncStatus.Idle, false),
            ("browser sign-in, error", daemon, SyncStatus.Error, false),
            ("GNOME Online Accounts", goa, SyncStatus.AuthRequired, false),
            ("password", password, SyncStatus.AuthRequired, false),
        ];
        foreach (var (name, cfg, status, want) in cases)
        {
            var account = new Account
            {
                Id = new AccountId("a"),
                Config = cfg,
                Enabled = true,
                State = new SyncState { AccountId = new AccountId("a"), Status = status },
            };
            Assert.True(want == AccountsPage.AccountRowOffersSignIn(account), name);
        }
    }

    [Theory]
    // A list a b c d: the index the dragged row ends up at, once it is taken
    // out of the list. from == result means "no change".
    [InlineData("onto itself, upper half", 1, 1, true, 1)]
    [InlineData("onto itself, lower half", 1, 1, false, 1)]
    [InlineData("onto the row above, upper half", 2, 1, true, 1)]
    // Below the row above is where it already is: a no-op (want == from).
    [InlineData("onto the row above, lower half", 2, 1, false, 2)]
    [InlineData("onto the row below, upper half", 1, 2, true, 1)]
    [InlineData("onto the row below, lower half", 1, 2, false, 2)]
    [InlineData("first onto last, lower half", 0, 3, false, 3)]
    [InlineData("last onto first, upper half", 3, 0, true, 0)]
    [InlineData("down two rows", 0, 2, false, 2)]
    [InlineData("up two rows", 3, 1, true, 1)]
    public void InsertIndexTest(string name, int from, int target, bool above, int want)
    {
        Assert.True(want == AccountsPage.InsertIndex(from, target, above), name);
    }

    [Theory]
    [InlineData("down one", 0, 1, "bacd")]
    [InlineData("down to the end", 0, 3, "bcda")]
    [InlineData("up one", 3, 2, "abdc")]
    [InlineData("up to the head", 3, 0, "dabc")]
    [InlineData("middle to middle", 1, 2, "acbd")]
    [InlineData("no move", 2, 2, "abcd")]
    [InlineData("out of range low", 0, -1, "abcd")]
    [InlineData("out of range high", 0, 4, "abcd")]
    public void MoveAccountTest(string name, int from, int to, string want)
    {
        var input = List("a", "b", "c", "d");
        Assert.True(want == Ids(AccountsPage.MoveAccount(input, from, to)), name);
        Assert.True(Ids(input) == "abcd", $"{name}: the input was modified");
    }

    [Fact]
    public void MoveAccountTestEdges()
    {
        var single = AccountsPage.MoveAccount(List("a"), 0, 0);
        Assert.Single(single);
        Assert.Equal(new AccountId("a"), single[0].Id);
        Assert.Empty(AccountsPage.MoveAccount([], 0, 1));
    }

    private static List<Account> List(params string[] names) => names.Select(n => TestAccount(n)).ToList();

    private static string Ids(IEnumerable<Account> accounts) => string.Concat(accounts.Select(a => a.Id.Value));

    private static SyncState State(string status, RpcError? error = null) =>
        new() { AccountId = new AccountId("a"), Status = status, Error = error };

    private static SyncState Tls(string status, string reason) => TlsState("a", status, reason);

    private static SyncState Failed(string status, int code, string message = "technical detail") =>
        State(status, new RpcError { Code = code, Message = message });
}
