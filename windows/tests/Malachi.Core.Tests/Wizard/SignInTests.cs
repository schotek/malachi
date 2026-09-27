// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/SignInTests.swift, the counterpart of
// ui/internal/signin/signin_test.go and the text helpers of
// ui/internal/accountwizard/oauth.go, with the Go tests Swift did not port
// (TestKindAndProvider, TestGOAAccountID, TestNeedsBrowserSignIn,
// TestClassifyDiscovery, TestClassifyDiscoveryCopies, TestClassifyFailure,
// TestIsClientMissing, TestTestNeedsSignIn, TestBrowserURL of signin; and
// TestOAuthErrorText, TestBrowserPage, TestProviderLabel of oauth_test.go).
// A thrown RPCError, Swift's client errors and Go's wrapped errors are
// RpcErrorTextFailureException and InnerException chains here.

using System;
using System.Linq;
using System.Text.Json;
using System.Text.Json.Nodes;
using Malachi.Core.Api;
using Malachi.Core.Tests.Text;
using Malachi.Core.Wizard;
using Xunit;

namespace Malachi.Core.Tests.Wizard;

public sealed class SignInTests
{
    private static readonly ServerConfig OAuthServer = new() { Host = "imap.gmail.com", Port = 993, Security = Security.Tls, Username = "me@gmail.com", AuthMethod = AuthMethod.OAuth2 };
    private static readonly ServerConfig OAuthSmtp = new() { Host = "smtp.gmail.com", Port = 465, Security = Security.Tls, Username = "me@gmail.com", AuthMethod = AuthMethod.OAuth2 };
    private static readonly ServerConfig PasswordImap = new() { Host = "imap.gmail.com", Port = 993, Security = Security.Tls, Username = "me@gmail.com", AuthMethod = AuthMethod.Password };
    private static readonly ServerConfig PasswordSmtp = new() { Host = "smtp.gmail.com", Port = 465, Security = Security.Tls, Username = "me@gmail.com", AuthMethod = AuthMethod.Password };

    private static readonly AccountConfig GoaLinked = new()
    {
        Name = "me@gmail.com",
        Email = "me@gmail.com",
        Imap = OAuthServer,
        Smtp = OAuthSmtp,
        OAuth2 = new OAuth2Config { Source = OAuth2Source.Goa, GoaAccountId = "account_1", Provider = OAuth2Provider.Google },
    };

    private static readonly AccountConfig GoaHint = new()
    {
        Name = "me@gmail.com",
        Email = "me@gmail.com",
        Imap = OAuthServer,
        Smtp = OAuthSmtp,
        OAuth2 = new OAuth2Config { Source = OAuth2Source.Goa, Provider = OAuth2Provider.Google },
    };

    private static readonly AccountConfig DaemonGoogle = new()
    {
        Name = "me@gmail.com",
        Email = "me@gmail.com",
        Imap = OAuthServer,
        Smtp = OAuthSmtp,
        OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google },
    };

    private static readonly AccountConfig AppPassword = new() { Name = "me@gmail.com", Email = "me@gmail.com", Imap = PasswordImap, Smtp = PasswordSmtp };

    private static readonly AccountConfig GraphHint = new()
    {
        Name = "me@contoso.com",
        Email = "me@contoso.com",
        Kind = AccountKind.Graph,
        Graph = new GraphConfig { Source = GraphSource.Goa },
    };

    private static readonly AccountConfig DaemonGraph = new()
    {
        Name = "me@contoso.com",
        Email = "me@contoso.com",
        Kind = AccountKind.Graph,
        OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Office365 },
        Graph = new GraphConfig { Source = GraphSource.Daemon },
    };

    private static readonly AccountConfig Ispdb = new()
    {
        Name = "example.org",
        Email = "me@example.org",
        Imap = new ServerConfig { Host = "imap.example.org", Port = 993, Security = Security.Tls, Username = "me@example.org", AuthMethod = AuthMethod.Password },
        Smtp = new ServerConfig { Host = "smtp.example.org", Port = 587, Security = Security.Starttls, Username = "me@example.org", AuthMethod = AuthMethod.Password },
    };

    private static AccountDiscoverResult Discovered(AccountConfig? cfg, params AccountConfig[] alternatives) =>
        new() { Config = cfg, Source = cfg is null ? DiscoverSource.None : DiscoverSource.Provider, Alternatives = alternatives };

    private static JsonElement SignedInAs(JsonNode? value) => TlsErrors.Raw(new JsonObject { ["signedInAs"] = value }.ToJsonString());

    private static RpcErrorTextFailureException E(ErrorCode code, JsonElement? data = null) =>
        RpcErrorTextFailureException.Daemon(new RpcError { Code = code, Message = "technical", Data = data });

    [Fact]
    public void ClassifyDiscoveryTable()
    {
        // A half-password alternative is not an app-password account.
        var mixed = AppPassword with { Smtp = AppPassword.Smtp! with { AuthMethod = AuthMethod.OAuth2 } };
        (string Name, AccountDiscoverResult? Result, Exception? Error, Discovery Want)[] cases =
        [
            ("failure", null, RpcErrorTextFailureException.Daemon(ErrorCode.NetworkError), new Discovery { Path = SignIn.Path.Password }),
            ("nothing found", Discovered(null), null, new Discovery { Path = SignIn.Path.Password }),
            ("ispdb", Discovered(Ispdb), null, new Discovery { Path = SignIn.Path.Password, Config = Ispdb }),
            ("signed in through GNOME Online Accounts", Discovered(GoaLinked, DaemonGoogle), null,
                new Discovery { Path = SignIn.Path.Goa, Config = GoaLinked, Provider = LinkedProvider.Google }),
            ("GNOME Online Accounts hint with both ways out", Discovered(GoaHint, DaemonGoogle, AppPassword), null,
                new Discovery { Path = SignIn.Path.GoaHint, Config = GoaHint, OAuthAlt = DaemonGoogle, PasswordAlt = AppPassword, Provider = LinkedProvider.Google }),
            ("GNOME Online Accounts hint without alternatives", Discovered(GraphHint), null,
                new Discovery { Path = SignIn.Path.GoaHint, Config = GraphHint, Provider = LinkedProvider.Microsoft365 }),
            ("Microsoft 365 hint with the browser", Discovered(GraphHint, DaemonGraph), null,
                new Discovery { Path = SignIn.Path.GoaHint, Config = GraphHint, OAuthAlt = DaemonGraph, Provider = LinkedProvider.Microsoft365 }),
            ("daemon Google with the app password", Discovered(DaemonGoogle, AppPassword), null,
                new Discovery { Path = SignIn.Path.OAuth, Config = DaemonGoogle, PasswordAlt = AppPassword, Provider = LinkedProvider.Google }),
            ("daemon Google, only a half-password alternative", Discovered(DaemonGoogle, mixed), null,
                new Discovery { Path = SignIn.Path.OAuth, Config = DaemonGoogle, Provider = LinkedProvider.Google }),
            ("daemon Microsoft 365", Discovered(DaemonGraph), null,
                new Discovery { Path = SignIn.Path.OAuth, Config = DaemonGraph, Provider = LinkedProvider.Microsoft365 }),
        ];
        foreach (var (name, result, error, want) in cases)
        {
            Assert.True(want == SignIn.ClassifyDiscovery(result, error), name);
        }
    }

    [Fact]
    public void ProviderLabels()
    {
        Assert.Equal("Google", OAuth.OAuthProviderLabel(LinkedProvider.Google, "me@gmail.com"));
        Assert.Equal("Microsoft 365", OAuth.OAuthProviderLabel(LinkedProvider.Microsoft365, "me@contoso.com"));
        Assert.Equal("yahoo.example", OAuth.OAuthProviderLabel(new LinkedProvider("yahoo"), "me@yahoo.example")); // an unknown provider is named by the domain
        Assert.Equal("mail.example", OAuth.OAuthProviderLabel(null, "Me@Mail.Example"));
    }

    [Fact]
    public void PromptTexts()
    {
        Assert.Equal(
            "Your browser will open so you can sign in to Google. Malachi Mail never sees your password; it only receives permission to read and send your mail.",
            OAuth.OAuthPromptText("Google"));
        Assert.Equal("_Sign In with Microsoft 365", OAuth.OAuthSignInLabel("Microsoft 365"));
        Assert.Equal(
            "No OAuth client is configured for Google on this computer. Add a client ID to the mail backend's configuration and try again.",
            OAuth.OAuthUnavailableText("Google"));
    }

    [Fact]
    public void ErrorTexts()
    {
        Assert.Equal("The sign-in was cancelled", OAuth.OAuthErrorText("Google", E(ErrorCode.Cancelled)));
        Assert.Equal("The sign-in with Microsoft 365 was refused", OAuth.OAuthErrorText("Microsoft 365", E(ErrorCode.AuthFailed)));
        Assert.Equal("The sign-in took too long; try again", OAuth.OAuthErrorText("Google", E(ErrorCode.ServerTimeout)));
        Assert.Equal("The sign-in took too long; try again", OAuth.OAuthErrorText("Google", RpcErrorTextFailureException.Timeout("account.oauthWait")));
        Assert.Equal("The sign-in took too long; try again", OAuth.OAuthErrorText("Google", new OperationCanceledException()));
        Assert.Equal(
            "The browser signed in to other@gmail.com, not to this address",
            OAuth.OAuthErrorText("Google", E(ErrorCode.InvalidArgument, SignedInAs(" other@gmail.com "))));
        Assert.Equal("Signing in was rejected: technical", OAuth.OAuthErrorText("Google", E(ErrorCode.InvalidArgument)));
        // Control and format characters, by code point: a right-to-left
        // override, a zero-width space, a byte order mark, a soft hyphen and a
        // C1 control.
        JsonNode?[] rejected =
        [
            "", "   ", 1, "a\x0007b@example.org", new string('a', 250) + "@x.org",
            "a@b.example\x202Emoc.elgoog", "\x200Ba@b.example", "a@b.example\xFEFF", "a\x00ADb@c.example", "a\x0085b@c.example",
        ];
        foreach (var bad in rejected)
        {
            Assert.Equal("Signing in was rejected: technical", OAuth.OAuthErrorText("Google", E(ErrorCode.InvalidArgument, SignedInAs(bad))));
        }
        Assert.Equal(
            "The browser signed in to jan\x00E9@b.example, not to this address",
            OAuth.OAuthErrorText("Google", E(ErrorCode.InvalidArgument, SignedInAs("jan\x00E9@b.example"))));
        Assert.Equal("Signing in failed: the server could not be reached", OAuth.OAuthErrorText("Google", E(ErrorCode.NetworkError)));
        Assert.Equal("Signing in needs a running mail backend", OAuth.OAuthErrorText("Google", RpcErrorTextFailureException.Disconnected()));
    }

    [Fact]
    public void BrowserPageTexts()
    {
        var page = OAuth.BrowserPage();
        Assert.Equal(
            new OAuthBrowserPage
            {
                SuccessTitle = "Signed in",
                SuccessText = "You can close this tab and return to Malachi Mail.",
                FailureTitle = "Sign-in failed",
                FailureText = "Return to Malachi Mail and try again.",
            },
            page);
    }

    [Theory]
    [InlineData("https://accounts.google.com/o/oauth2/v2/auth?client_id=x&state=y", true)]
    [InlineData("https://login.microsoftonline.com/common/oauth2/v2.0/authorize?x=1", true)]
    [InlineData("HTTPS://accounts.google.com/", true)]
    [InlineData("http://accounts.google.com/", false)]
    [InlineData("https:accounts.google.com", false)]
    [InlineData("https://", false)]
    [InlineData("https://user:pw@accounts.google.com/", false)]
    [InlineData("https://user@accounts.google.com/", false)]
    [InlineData("javascript:alert(1)", false)]
    [InlineData("file:///etc/passwd", false)]
    [InlineData("", false)]
    public void BrowserUrls(string url, bool want)
    {
        Assert.Equal(want, OAuth.IsBrowserUrl(url));
        Assert.Equal("The link could not be opened: not an https address", OAuth.RefusedBrowserUrlText());
    }

    [Fact]
    public void SignInProblems()
    {
        static EndpointTestResult Tested(ErrorCode? code) =>
            new() { Ok = code is null, Error = code is { } c ? new RpcError { Code = c, Message = "x" } : null, LatencyMs = 1 };
        (string Name, AccountTestResult? Result, Exception? Error, bool Want)[] cases =
        [
            ("all fine", new AccountTestResult { Imap = Tested(null), Smtp = Tested(null) }, null, false),
            ("imap refused the token", new AccountTestResult { Imap = Tested(ErrorCode.AuthFailed), Smtp = Tested(null) }, null, true),
            ("no sign-in stored", new AccountTestResult { Graph = Tested(ErrorCode.AuthRequired) }, null, true),
            ("smtp needs a sign-in", new AccountTestResult { Imap = Tested(null), Smtp = Tested(ErrorCode.AuthRequired) }, null, true),
            ("network", new AccountTestResult { Imap = Tested(ErrorCode.NetworkError), Smtp = Tested(null) }, null, false),
            ("call said authRequired", null, RpcErrorTextFailureException.Daemon(ErrorCode.AuthRequired), true),
            ("call said authFailed", null, RpcErrorTextFailureException.Daemon(ErrorCode.AuthFailed), true),
            ("call timed out", null, RpcErrorTextFailureException.Timeout("account.oauthWait"), false),
            ("call failed otherwise", null, RpcErrorTextFailureException.Daemon(ErrorCode.OAuthClientMissing), false),
        ];
        foreach (var (name, result, error, want) in cases)
        {
            Assert.True(OAuth.IsSignInProblem(result, error) == want, name);
        }
    }

    // The fixtures of signin_test.go.
    private static ServerConfig GoServer(string host, AuthMethod auth, int port = 0) =>
        new() { Host = host, Port = port, Security = "", Username = "", AuthMethod = auth };

    private static readonly AccountConfig GoGoaGraph = new()
    {
        Name = "",
        Email = "me@contoso.example",
        Kind = AccountKind.Graph,
        Graph = new GraphConfig { Source = GraphSource.Goa, GoaAccountId = "account_1_0" },
    };

    private static readonly AccountConfig GoGoaGraphHint = new()
    {
        Name = "",
        Email = "me@contoso.example",
        Kind = AccountKind.Graph,
        Graph = new GraphConfig { Source = GraphSource.Goa },
    };

    private static readonly AccountConfig GoDaemonGraph = new()
    {
        Name = "",
        Email = "me@contoso.example",
        Kind = AccountKind.Graph,
        Graph = new GraphConfig { Source = GraphSource.Daemon },
        OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Office365 },
    };

    private static readonly AccountConfig GoGoaGoogle = new()
    {
        Name = "",
        Email = "me@gmail.com",
        Imap = GoServer("imap.gmail.com", AuthMethod.OAuth2),
        Smtp = GoServer("smtp.gmail.com", AuthMethod.OAuth2),
        OAuth2 = new OAuth2Config { Source = OAuth2Source.Goa, Provider = OAuth2Provider.Google, GoaAccountId = "account_2_0" },
    };

    private static readonly AccountConfig GoGoaGoogleHint = new()
    {
        Name = "",
        Email = "me@gmail.com",
        Imap = GoServer("imap.gmail.com", AuthMethod.OAuth2),
        Smtp = GoServer("smtp.gmail.com", AuthMethod.OAuth2),
        OAuth2 = new OAuth2Config { Source = OAuth2Source.Goa, Provider = OAuth2Provider.Google },
    };

    private static readonly AccountConfig GoDaemonGoogle = new()
    {
        Name = "",
        Email = "me@gmail.com",
        Imap = GoServer("imap.gmail.com", AuthMethod.OAuth2),
        Smtp = GoServer("smtp.gmail.com", AuthMethod.OAuth2),
        OAuth2 = new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google },
    };

    private static readonly AccountConfig GoAppPassword = new()
    {
        Name = "",
        Email = "me@gmail.com",
        Kind = AccountKind.Imap,
        Imap = GoServer("imap.gmail.com", AuthMethod.Password, 993),
        Smtp = GoServer("smtp.gmail.com", AuthMethod.Password, 465),
    };

    private static readonly AccountConfig GoCustom = new()
    {
        Name = "",
        Email = "",
        Imap = GoServer("", AuthMethod.OAuth2),
        OAuth2 = new OAuth2Config { Provider = OAuth2Provider.Custom },
    };

    [Fact]
    public void TestKindAndProvider()
    {
        (string Name, AccountConfig Cfg, SignIn.Kind Kind, LinkedProvider? Provider)[] cases =
        [
            ("goa graph", GoGoaGraph, SignIn.Kind.Goa, LinkedProvider.Microsoft365),
            ("goa graph hint", GoGoaGraphHint, SignIn.Kind.Goa, LinkedProvider.Microsoft365),
            ("daemon graph", GoDaemonGraph, SignIn.Kind.OAuth, LinkedProvider.Microsoft365),
            ("graph without block", new AccountConfig { Name = "", Email = "", Kind = AccountKind.Graph }, SignIn.Kind.OAuth, LinkedProvider.Microsoft365),
            ("goa google", GoGoaGoogle, SignIn.Kind.Goa, LinkedProvider.Google),
            ("goa google hint", GoGoaGoogleHint, SignIn.Kind.Goa, LinkedProvider.Google),
            ("daemon google", GoDaemonGoogle, SignIn.Kind.OAuth, LinkedProvider.Google),
            ("office365 imap", new AccountConfig { Name = "", Email = "", Imap = GoServer("", AuthMethod.OAuth2), OAuth2 = new OAuth2Config { Provider = OAuth2Provider.Office365 } },
                SignIn.Kind.OAuth, LinkedProvider.Microsoft365),
            ("custom", GoCustom, SignIn.Kind.OAuth, null),
            ("password", GoAppPassword, SignIn.Kind.Password, null),
            ("empty", new AccountConfig { Name = "", Email = "" }, SignIn.Kind.Password, null),
        ];
        foreach (var (name, cfg, kind, provider) in cases)
        {
            Assert.True(SignIn.KindOf(cfg) == kind, name + ": KindOf");
            Assert.True(SignIn.Provider(cfg) == provider, name + ": Provider");
        }
        Assert.Equal("Microsoft 365", SignIn.ProviderName(LinkedProvider.Microsoft365));
        Assert.Equal("Google", SignIn.ProviderName(LinkedProvider.Google));
        Assert.Equal("", SignIn.ProviderName(null));
        Assert.Equal("", SignIn.ProviderName(new LinkedProvider("yahoo")));
    }

    [Fact]
    public void TestGoaAccountId()
    {
        (AccountConfig Cfg, string? Want)[] cases =
        [
            (GoGoaGraph, "account_1_0"), (GoGoaGoogle, "account_2_0"), (GoGoaGraphHint, null), (GoGoaGoogleHint, null),
            (GoDaemonGraph, null), (GoDaemonGoogle, null), (GoAppPassword, null),
        ];
        foreach (var (cfg, want) in cases)
        {
            Assert.True(Linked.LinkedAccountId(cfg) == want, cfg.Email);
        }
    }

    [Fact]
    public void TestNeedsBrowserSignIn()
    {
        (AccountConfig Cfg, SyncStatus Status, bool Want)[] cases =
        [
            (GoDaemonGoogle, SyncStatus.AuthRequired, true),
            (GoDaemonGraph, SyncStatus.AuthRequired, true),
            (GoDaemonGoogle, SyncStatus.Idle, false),
            (GoDaemonGraph, SyncStatus.Error, false),
            (GoGoaGoogle, SyncStatus.AuthRequired, false),
            (GoAppPassword, SyncStatus.AuthRequired, false),
        ];
        foreach (var (cfg, status, want) in cases)
        {
            var a = new Account { Id = "a", Config = cfg, Enabled = true, State = new SyncState { AccountId = "a", Status = status } };
            Assert.True(SignIn.NeedsBrowserSignIn(a) == want, $"{cfg.Email}/{status}");
        }
    }

    [Fact]
    public void TestClassifyDiscovery()
    {
        var halfPassword = new AccountConfig { Name = "", Email = "me@gmail.com", Imap = GoServer("", AuthMethod.Password), Smtp = GoServer("", AuthMethod.OAuth2) };
        var imapOnly = new AccountConfig { Name = "", Email = "me@gmail.com", Imap = GoServer("", AuthMethod.Password) };
        (string Name, AccountDiscoverResult Result, Exception? Error, Discovery Want)[] cases =
        [
            ("error", new AccountDiscoverResult { Config = GoAppPassword, Source = DiscoverSource.Ispdb }, new InvalidOperationException("boom"),
                new Discovery { Path = SignIn.Path.Password }),
            ("nothing found", new AccountDiscoverResult { Source = DiscoverSource.None }, null, new Discovery { Path = SignIn.Path.Password }),
            ("ispdb", new AccountDiscoverResult { Config = GoAppPassword, Source = DiscoverSource.Ispdb }, null,
                new Discovery { Path = SignIn.Path.Password, Config = GoAppPassword }),
            ("signed in through goa (graph)", new AccountDiscoverResult { Config = GoGoaGraph, Source = DiscoverSource.Goa }, null,
                new Discovery { Path = SignIn.Path.Goa, Config = GoGoaGraph, Provider = LinkedProvider.Microsoft365 }),
            ("signed in through goa (google)", new AccountDiscoverResult { Config = GoGoaGoogle, Source = DiscoverSource.Goa, Alternatives = [GoAppPassword] }, null,
                new Discovery { Path = SignIn.Path.Goa, Config = GoGoaGoogle, Provider = LinkedProvider.Google }),
            ("goa hint, google, both alternatives",
                new AccountDiscoverResult { Config = GoGoaGoogleHint, Source = DiscoverSource.Provider, ProviderName = "<b>Evil</b>", Alternatives = [GoDaemonGoogle, GoAppPassword] }, null,
                new Discovery { Path = SignIn.Path.GoaHint, Config = GoGoaGoogleHint, OAuthAlt = GoDaemonGoogle, PasswordAlt = GoAppPassword, Provider = LinkedProvider.Google }),
            ("goa hint, alternatives out of order",
                new AccountDiscoverResult { Config = GoGoaGoogleHint, Source = DiscoverSource.Provider, Alternatives = [GoAppPassword, GoDaemonGoogle] }, null,
                new Discovery { Path = SignIn.Path.GoaHint, Config = GoGoaGoogleHint, OAuthAlt = GoDaemonGoogle, PasswordAlt = GoAppPassword, Provider = LinkedProvider.Google }),
            ("goa hint, microsoft, own sign-in only",
                new AccountDiscoverResult { Config = GoGoaGraphHint, Source = DiscoverSource.Provider, Alternatives = [GoDaemonGraph] }, null,
                new Discovery { Path = SignIn.Path.GoaHint, Config = GoGoaGraphHint, OAuthAlt = GoDaemonGraph, Provider = LinkedProvider.Microsoft365 }),
            ("goa hint, older daemon without alternatives",
                new AccountDiscoverResult { Config = GoGoaGraphHint, Source = DiscoverSource.Provider }, null,
                new Discovery { Path = SignIn.Path.GoaHint, Config = GoGoaGraphHint, Provider = LinkedProvider.Microsoft365 }),
            ("goa hint ignores a goa alternative",
                new AccountDiscoverResult { Config = GoGoaGoogleHint, Source = DiscoverSource.Provider, Alternatives = [GoGoaGoogle] }, null,
                new Discovery { Path = SignIn.Path.GoaHint, Config = GoGoaGoogleHint, Provider = LinkedProvider.Google }),
            ("own sign-in, google",
                new AccountDiscoverResult { Config = GoDaemonGoogle, Source = DiscoverSource.Provider, Alternatives = [GoAppPassword] }, null,
                new Discovery { Path = SignIn.Path.OAuth, Config = GoDaemonGoogle, PasswordAlt = GoAppPassword, Provider = LinkedProvider.Google }),
            ("own sign-in, microsoft",
                new AccountDiscoverResult { Config = GoDaemonGraph, Source = DiscoverSource.Provider }, null,
                new Discovery { Path = SignIn.Path.OAuth, Config = GoDaemonGraph, Provider = LinkedProvider.Microsoft365 }),
            ("own sign-in skips a half-password alternative",
                new AccountDiscoverResult { Config = GoDaemonGoogle, Source = DiscoverSource.Provider, Alternatives = [halfPassword, imapOnly, GoAppPassword] }, null,
                new Discovery { Path = SignIn.Path.OAuth, Config = GoDaemonGoogle, PasswordAlt = GoAppPassword, Provider = LinkedProvider.Google }),
        ];
        foreach (var (name, result, error, want) in cases)
        {
            Assert.True(want == SignIn.ClassifyDiscovery(result, error), name);
        }
    }

    // Records are immutable: what the discovery holds is not the result's to
    // change, and a change made from it is a copy (Go: the discovery does not
    // alias the result).
    [Fact]
    public void TestClassifyDiscoveryCopies()
    {
        var cfg = GoDaemonGoogle;
        AccountConfig[] alts = [GoAppPassword];
        var d = SignIn.ClassifyDiscovery(new AccountDiscoverResult { Config = cfg, Source = DiscoverSource.Provider, Alternatives = alts });
        var changed = d.Config! with { Name = "changed" };
        var changedAlt = d.PasswordAlt! with { Name = "changed" };
        Assert.True(cfg.Name != "changed" && alts[0].Name != "changed" && changed.Name == "changed" && changedAlt.Name == "changed");
        Assert.Equal(cfg, d.Config);
    }

    [Fact]
    public void TestClassifyFailure()
    {
        var wrong = E(ErrorCode.InvalidArgument, SignedInAs(" other@gmail.com "));
        (string Name, Exception? Error, SignIn.Failure Failure, string Who)[] cases =
        [
            ("cancelled", E(ErrorCode.Cancelled), SignIn.Failure.Cancelled, ""),
            ("refused", E(ErrorCode.AuthFailed), SignIn.Failure.Refused, ""),
            ("expired", E(ErrorCode.ServerTimeout), SignIn.Failure.Timeout, ""),
            ("call timed out", RpcErrorTextFailureException.Timeout("account.oauthWait"), SignIn.Failure.Timeout, ""),
            ("call timed out (TimeoutException)", new TimeoutException(), SignIn.Failure.Timeout, ""),
            ("wrapped call timeout", new InvalidOperationException("wait", new TimeoutException()), SignIn.Failure.Timeout, ""),
            ("wrong account", wrong, SignIn.Failure.WrongAccount, "other@gmail.com"),
            ("wrong account, wrapped", new InvalidOperationException("x", wrong), SignIn.Failure.WrongAccount, "other@gmail.com"),
            ("wrong account, string map", E(ErrorCode.InvalidArgument, SignedInAs("a@b.example")), SignIn.Failure.WrongAccount, "a@b.example"),
            ("invalid argument without data", RpcErrorTextFailureException.Daemon(ErrorCode.InvalidArgument, "unknown session"), SignIn.Failure.Other, ""),
            ("signedInAs not a string", E(ErrorCode.InvalidArgument, SignedInAs(42.0)), SignIn.Failure.Other, ""),
            ("signedInAs with a newline", E(ErrorCode.InvalidArgument, SignedInAs("a@b.example\nclick here")), SignIn.Failure.Other, ""),
            ("signedInAs too long", E(ErrorCode.InvalidArgument, SignedInAs(new string('a', 250) + "@b.example")), SignIn.Failure.Other, ""),
            ("signedInAs invalid utf-8", E(ErrorCode.InvalidArgument, TlsErrors.Raw("{\"signedInAs\":\"a\\ud800@b.example\"}")), SignIn.Failure.Other, ""),
            ("signedInAs with a right-to-left override", E(ErrorCode.InvalidArgument, SignedInAs("a@b.example\x202Emoc.elgoog")), SignIn.Failure.Other, ""),
            ("signedInAs with a zero-width space", E(ErrorCode.InvalidArgument, SignedInAs("\x200Ba@b.example")), SignIn.Failure.Other, ""),
            ("signedInAs with a byte order mark", E(ErrorCode.InvalidArgument, SignedInAs("a@b.example\xFEFF")), SignIn.Failure.Other, ""),
            ("signedInAs with a soft hyphen", E(ErrorCode.InvalidArgument, SignedInAs("a\x00ADb@c.example")), SignIn.Failure.Other, ""),
            ("signedInAs with a C1 control", E(ErrorCode.InvalidArgument, SignedInAs("a\x0085b@c.example")), SignIn.Failure.Other, ""),
            ("signedInAs with a non-ASCII letter", E(ErrorCode.InvalidArgument, SignedInAs("jan\x00E9@b.example")), SignIn.Failure.WrongAccount, "jan\x00E9@b.example"),
            ("signedInAs on another code", E(ErrorCode.AuthFailed, SignedInAs("a@b.example")), SignIn.Failure.Refused, ""),
            ("network", E(ErrorCode.NetworkError), SignIn.Failure.Other, ""),
            ("plain error", new InvalidOperationException("disconnected"), SignIn.Failure.Other, ""),
            ("nil", null, SignIn.Failure.Other, ""),
        ];
        foreach (var (name, error, failure, who) in cases)
        {
            Assert.True(SignIn.ClassifyFailure(error) == (failure, who), name);
        }
    }

    [Fact]
    public void TestIsClientMissing()
    {
        Assert.True(SignIn.IsClientMissing(RpcErrorTextFailureException.Daemon(ErrorCode.OAuthClientMissing)));
        Assert.True(SignIn.IsClientMissing(new InvalidOperationException("start", RpcErrorTextFailureException.Daemon(ErrorCode.OAuthClientMissing))));
        Assert.False(SignIn.IsClientMissing(RpcErrorTextFailureException.Daemon(ErrorCode.AuthFailed)));
        Assert.False(SignIn.IsClientMissing(new InvalidOperationException("x")));
        Assert.False(SignIn.IsClientMissing(null));
    }

    [Fact]
    public void TestTestNeedsSignIn()
    {
        var ok = new EndpointTestResult { Ok = true, LatencyMs = 0 };
        var refused = new EndpointTestResult { Ok = false, Error = new RpcError { Code = ErrorCode.AuthFailed }, LatencyMs = 0 };
        var signedOut = new EndpointTestResult { Ok = false, Error = new RpcError { Code = ErrorCode.AuthRequired }, LatencyMs = 0 };
        var unreachable = new EndpointTestResult { Ok = false, Error = new RpcError { Code = ErrorCode.NetworkError }, LatencyMs = 0 };
        (string Name, AccountTestResult Result, Exception? Error, bool Want)[] cases =
        [
            ("all fine", new AccountTestResult { Imap = ok, Smtp = ok }, null, false),
            ("imap refused", new AccountTestResult { Imap = refused, Smtp = ok }, null, true),
            ("smtp signed out", new AccountTestResult { Imap = ok, Smtp = signedOut }, null, true),
            ("graph signed out", new AccountTestResult { Graph = signedOut }, null, true),
            ("network only", new AccountTestResult { Imap = unreachable, Smtp = ok }, null, false),
            ("call auth required", new AccountTestResult(), RpcErrorTextFailureException.Daemon(ErrorCode.AuthRequired), true),
            ("call failed otherwise", new AccountTestResult(), RpcErrorTextFailureException.Daemon(ErrorCode.KeyringError), false),
            ("disconnected", new AccountTestResult(), RpcErrorTextFailureException.Disconnected(), false),
            ("empty", new AccountTestResult(), null, false),
        ];
        foreach (var (name, result, error, want) in cases)
        {
            Assert.True(SignIn.TestNeedsSignIn(result, error) == want, name);
        }
    }

    [Theory]
    [InlineData("https://accounts.google.com/o/oauth2/v2/auth?client_id=x&state=y", true)]
    [InlineData("https://login.microsoftonline.com/common/oauth2/v2.0/authorize?login_hint=a%40b.c", true)]
    [InlineData("HTTPS://accounts.google.com/", true)]
    [InlineData("http://accounts.google.com/", false)]
    [InlineData("file:///etc/passwd", false)]
    [InlineData("javascript:alert(1)", false)]
    [InlineData("https:opaque", false)]
    [InlineData("https:///path-only", false)]
    [InlineData("https://user:pass@accounts.google.com/", false)]
    [InlineData("", false)]
    [InlineData("accounts.google.com/o/oauth2", false)]
    [InlineData("https://[::1", false)]
    public void TestBrowserUrl(string url, bool want)
    {
        Assert.Equal(want, SignIn.BrowserUrl(url));
    }

    // Windows: an IPv6 zone is read by Go 1.25's rules (UrlSyntax), each
    // case checked against url.Parse.
    [Theory]
    [InlineData("https://[fe80::1%25%41]/", true)]
    [InlineData("https://[fe80::1%25%20x]/", true)]
    [InlineData("https://[fe80::1%25en%30]/", true)]
    [InlineData("https://[fe80::1%25en0]:8080/", true)]
    [InlineData("https://[fe80::1%25%C3%A4]/", false)]
    [InlineData("https://[fe80::1%25%0A]/", false)]
    [InlineData("https://[fe80::1%25a%2]/", false)]
    public void BrowserUrlZones(string url, bool want)
    {
        Assert.Equal(want, SignIn.BrowserUrl(url));
    }

    // oauth_test.go TestOAuthErrorText: the sentence for each kind of failure.
    [Fact]
    public void TestOAuthErrorText()
    {
        (Exception Error, string Want)[] cases =
        [
            (RpcErrorTextFailureException.Daemon(ErrorCode.Cancelled, "access_denied"), "The sign-in was cancelled"),
            (RpcErrorTextFailureException.Daemon(ErrorCode.AuthFailed, "invalid_grant"), "The sign-in with Google was refused"),
            (RpcErrorTextFailureException.Daemon(ErrorCode.ServerTimeout, "expired"), "The sign-in took too long; try again"),
            (new TimeoutException(), "The sign-in took too long; try again"),
            (E(ErrorCode.InvalidArgument, SignedInAs("other@gmail.com")), "The browser signed in to other@gmail.com, not to this address"),
            (RpcErrorTextFailureException.Daemon(ErrorCode.NetworkError, "dial"), "Signing in failed: the server could not be reached"),
            (new InvalidOperationException("boom"), "Signing in failed"),
        ];
        foreach (var (error, want) in cases)
        {
            Assert.Equal(want, OAuth.OAuthErrorText("Google", error));
        }
    }

    // oauth_test.go TestBrowserPage: every text within the daemon's 200
    // characters.
    [Fact]
    public void TestBrowserPage()
    {
        var p = OAuth.BrowserPage();
        foreach (var s in new[] { p.SuccessTitle, p.SuccessText, p.FailureTitle, p.FailureText })
        {
            Assert.True(!string.IsNullOrEmpty(s) && s.EnumerateRunes().Count() <= 200, $"browser page text {s}: empty or over the daemon's 200 characters");
        }
    }

    // oauth_test.go TestProviderLabel.
    [Fact]
    public void TestProviderLabel()
    {
        Assert.Equal("Google", OAuth.OAuthProviderLabel(LinkedProvider.Google, "me@gmail.com"));
        Assert.Equal("Microsoft 365", OAuth.OAuthProviderLabel(LinkedProvider.Microsoft365, ""));
        Assert.Equal("example.org", OAuth.OAuthProviderLabel(null, "me@example.org"));
    }
}
