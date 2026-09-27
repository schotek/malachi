// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ProviderTests.swift (accountProviderTest),
// the counterpart of ui/internal/widget/provider_test.go
// (TestProviderIconName); KindAndProviderTest carries the cases of
// ui/internal/signin/signin_test.go (TestKindAndProvider) that the Swift
// suite does not have.

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class ProviderTests
{
    private static readonly ServerConfig OAuth = new()
    {
        Host = "",
        Port = 0,
        Security = Security.Tls,
        Username = "",
        AuthMethod = AuthMethod.OAuth2,
    };

    [Fact]
    public void AccountProviderTest()
    {
        var graph = Config(kind: AccountKind.Graph, graph: new GraphConfig { Source = GraphSource.Goa, GoaAccountId = "account_1_0" });
        var google = Config(imap: OAuth, smtp: OAuth,
            oauth2: new OAuth2Config { Source = OAuth2Source.Goa, GoaAccountId = "account_2_0", Provider = OAuth2Provider.Google });
        var daemonGoogle = Config(imap: OAuth, smtp: OAuth,
            oauth2: new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Google });
        var daemonGraph = Config(kind: AccountKind.Graph,
            oauth2: new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Office365 },
            graph: new GraphConfig { Source = GraphSource.Daemon });
        var bareGraph = Config(kind: AccountKind.Graph);
        var daemonOffice = Config(imap: OAuth, smtp: OAuth,
            oauth2: new OAuth2Config { Source = OAuth2Source.Daemon, Provider = OAuth2Provider.Office365 });
        var own = Config(imap: OAuth, oauth2: new OAuth2Config { Provider = OAuth2Provider.Office365 });
        var password = Config(imap: OAuth with { AuthMethod = AuthMethod.Password });
        (string Name, AccountConfig Cfg, LinkedProvider? Provider, SignInKind Kind)[] cases =
        [
            ("graph", graph, LinkedProvider.Microsoft365, SignInKind.Goa),
            ("google", google, LinkedProvider.Google, SignInKind.Goa),
            ("daemon google", daemonGoogle, LinkedProvider.Google, SignInKind.OAuth),
            ("daemon graph", daemonGraph, LinkedProvider.Microsoft365, SignInKind.OAuth),
            ("graph without a source", bareGraph, LinkedProvider.Microsoft365, SignInKind.OAuth),
            ("daemon office365 over imap", daemonOffice, LinkedProvider.Microsoft365, SignInKind.OAuth),
            ("oauth2 block without a source", own, LinkedProvider.Microsoft365, SignInKind.OAuth),
            ("password", password, null, SignInKind.Password),
        ];
        foreach (var (name, cfg, provider, kind) in cases)
        {
            Assert.True(provider == Provider.AccountProvider(cfg), name);
            Assert.True(kind == Provider.SignInKindOf(cfg), name);
        }
        Assert.Equal("Microsoft 365", Provider.ProviderName(LinkedProvider.Microsoft365));
        Assert.Equal("Google", Provider.ProviderName(LinkedProvider.Google));
        Assert.Equal("", Provider.ProviderName(null));
        Assert.Equal("goa-account-google-symbolic", Provider.ProviderIconName(LinkedProvider.Google));
        Assert.Equal("goa-account-ms365-symbolic", Provider.ProviderIconName(LinkedProvider.Microsoft365));
        Assert.Null(Provider.ProviderIconName(new LinkedProvider("x")));
        Assert.Equal("mail-unread-symbolic", Provider.ProviderIcon(null));
        Assert.Equal("goa-account-google-symbolic", Provider.ProviderIcon(LinkedProvider.Google));
    }

    // signin_test.go TestKindAndProvider: the hints without a GOA account
    // id, a custom provider, an empty configuration, an unknown name.
    [Fact]
    public void KindAndProviderTest()
    {
        var oauthImap = OAuth with { Host = "imap.gmail.com" };
        var oauthSmtp = OAuth with { Host = "smtp.gmail.com" };
        var cases = new Dictionary<string, (AccountConfig Cfg, SignInKind Kind, LinkedProvider? Provider)>
        {
            ["goa graph hint"] = (Config(email: "me@contoso.example", kind: AccountKind.Graph,
                graph: new GraphConfig { Source = GraphSource.Goa }), SignInKind.Goa, LinkedProvider.Microsoft365),
            ["goa google hint"] = (Config(email: "me@gmail.com", imap: oauthImap, smtp: oauthSmtp,
                oauth2: new OAuth2Config { Source = OAuth2Source.Goa, Provider = OAuth2Provider.Google }), SignInKind.Goa, LinkedProvider.Google),
            ["custom"] = (Config(imap: OAuth, oauth2: new OAuth2Config { Provider = OAuth2Provider.Custom }), SignInKind.OAuth, null),
            ["password"] = (Config(email: "me@gmail.com", kind: AccountKind.Imap,
                imap: oauthImap with { Port = 993, AuthMethod = AuthMethod.Password },
                smtp: oauthSmtp with { Port = 465, AuthMethod = AuthMethod.Password }), SignInKind.Password, null),
            ["empty"] = (Config(), SignInKind.Password, null),
        };
        foreach (var (name, c) in cases)
        {
            Assert.True(c.Kind == Provider.SignInKindOf(c.Cfg), name);
            Assert.True(c.Provider == Provider.AccountProvider(c.Cfg), name);
        }
        Assert.Equal("", Provider.ProviderName(new LinkedProvider("yahoo")));
    }

    private static AccountConfig Config(
        string email = "", AccountKind? kind = null, ServerConfig? imap = null, ServerConfig? smtp = null,
        OAuth2Config? oauth2 = null, GraphConfig? graph = null) =>
        new() { Name = "", Email = email, Kind = kind, Imap = imap, Smtp = smtp, OAuth2 = oauth2, Graph = graph };
}
