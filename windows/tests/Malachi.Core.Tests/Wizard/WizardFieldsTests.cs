// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/WizardFieldsTests.swift, the
// counterpart of ui/internal/accountwizard/fields_test.go.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Wizard;
using Xunit;

namespace Malachi.Core.Tests.Wizard;

public sealed class WizardFieldsTests
{
    [Theory]
    [InlineData(Endpoint.Imap, Security.Tls, 993)]
    [InlineData(Endpoint.Imap, Security.Starttls, 143)]
    [InlineData(Endpoint.Imap, Security.None, 143)]
    [InlineData(Endpoint.Smtp, Security.Tls, 465)]
    [InlineData(Endpoint.Smtp, Security.Starttls, 587)]
    [InlineData(Endpoint.Smtp, Security.None, 587)]
    public void DefaultPorts(Endpoint kind, string security, int want)
    {
        Assert.Equal(want, Fields.DefaultPort(kind, security));
    }

    [Fact]
    public void PortForSecurityChanges()
    {
        Assert.Equal(143, Fields.PortForSecurityChange(Endpoint.Imap, 993, Security.Tls, Security.Starttls)); // default swap
        Assert.Equal(9993, Fields.PortForSecurityChange(Endpoint.Imap, 9993, Security.Tls, Security.Starttls)); // custom kept
        Assert.Equal(587, Fields.PortForSecurityChange(Endpoint.Smtp, 587, Security.Starttls, Security.Starttls)); // same mode
        Assert.Equal(587, Fields.PortForSecurityChange(Endpoint.Smtp, 0, Security.Tls, Security.None)); // zero port
    }

    [Fact]
    public void SecurityChoicesRoundTrip()
    {
        Assert.Equal([Security.Tls, Security.Starttls, Security.None], Fields.SecurityChoices);
        for (var i = 0; i < Fields.SecurityChoices.Count; i++)
        {
            var s = Fields.SecurityChoices[i];
            Assert.Equal(i, Fields.IndexOfSecurity(s));
            Assert.Equal(s, Fields.SecurityAt(i));
        }
        Assert.Equal(Security.Tls, Fields.SecurityAt(99));
        Assert.Equal(Security.Tls, Fields.SecurityAt(-1));
        Assert.Equal(0, Fields.IndexOfSecurity(new Security("bogus")));
    }

    [Fact]
    public void ValidateEmails()
    {
        var cases = new Dictionary<string, bool>
        {
            ["me@example.org"] = true,
            ["  me@example.org "] = true,
            ["Name <me@example.org>"] = false,
            ["me@"] = false,
            ["@example.org"] = false,
            [""] = false,
            ["a b@example.org"] = false,
            ["<me@example.org>"] = false,
        };
        foreach (var (input, ok) in cases)
        {
            Assert.True((Fields.ValidateEmail(input) is not null) == ok, $"ValidateEmail({input})");
        }
        Assert.Equal("me@example.org", Fields.ValidateEmail("  me@example.org "));
    }

    [Fact]
    public void GuessAndMerge()
    {
        var g = Fields.GuessConfig("me@Example.org");
        var imap = g.Imap;
        var smtp = g.Smtp;
        Assert.NotNull(imap);
        Assert.NotNull(smtp);
        Assert.Equal("imap.example.org", imap.Host);
        Assert.Equal(993, imap.Port);
        Assert.Equal(Security.Tls, imap.Security);
        Assert.Equal("smtp.example.org", smtp.Host);
        Assert.Equal(587, smtp.Port);
        Assert.Equal(Security.Starttls, smtp.Security);
        Assert.Equal("me@Example.org", imap.Username);
        Assert.Equal(AuthMethod.Password, smtp.AuthMethod);
        Assert.Equal(AccountKind.Imap, g.Kind);
        Assert.Equal("me@Example.org", g.Email);
        Assert.Equal("example.org", Fields.SuggestAccountName("me@Example.org"));
        Assert.Equal("nope", Fields.SuggestAccountName("nope"));
        Assert.Equal("", Fields.Domain("me@"));
        Assert.Equal("c.example", Fields.Domain("a@b@C.example"));

        var discovered = new AccountConfig
        {
            Name = "  ",
            Email = "",
            Imap = new ServerConfig { Host = "imap.x.org", Port = 993, Security = Security.Tls, Username = "", AuthMethod = AuthMethod.OAuth2 },
            Smtp = new ServerConfig { Host = "smtp.x.org", Port = 587, Security = Security.Starttls, Username = "custom", AuthMethod = AuthMethod.OAuth2 },
            Graph = new GraphConfig { Source = GraphSource.Goa, GoaAccountId = "x" },
        };
        var m = Fields.MergeIdentity(discovered, new Identity { DisplayName = " Me ", Email = "me@x.org", Password = "p" });
        Assert.Equal("x.org", m.Name);
        Assert.Equal("me@x.org", m.Email);
        Assert.Equal("Me", m.DisplayName);
        Assert.Equal(AccountKind.Imap, m.Kind);
        Assert.Null(m.Graph);
        Assert.Equal("me@x.org", m.Imap?.Username);
        Assert.Equal("imap.x.org", m.Imap?.Host);
        Assert.Equal("custom", m.Smtp?.Username);
        Assert.Equal(AuthMethod.Password, m.Imap?.AuthMethod);
        Assert.Equal(AuthMethod.Password, m.Smtp?.AuthMethod);
        // The input is not modified, and missing endpoints come from the guess.
        Assert.Equal("", discovered.Imap?.Username);
        var filled = Fields.MergeIdentity(new AccountConfig { Name = "Named", Email = "" }, new Identity { Email = "me@x.org" });
        Assert.Equal("Named", filled.Name);
        Assert.Null(filled.DisplayName);
        Assert.Equal("imap.x.org", filled.Imap?.Host);
        Assert.Equal("smtp.x.org", filled.Smtp?.Host);
        Assert.Equal("me@x.org", filled.Smtp?.Username);
    }

    [Fact]
    public void ValidateAndBuild()
    {
        var p = Fields.ValidateIdentity(new Identity { Email = "bad", Password = "" }, passwordRequired: true);
        Assert.True(p.Email);
        Assert.True(p.Password);
        Assert.True(p.Any);
        Assert.False(Fields.ValidateIdentity(new Identity { Email = "me@x.org", Password = "p" }, passwordRequired: true).Any);
        Assert.False(Fields.ValidateIdentity(new Identity { Email = "me@x.org" }, passwordRequired: false).Any);

        var sp = Fields.ValidateServers(new ServerFields { Host = " ", Username = "u" }, new ServerFields { Host = "smtp.x.org", Username = "" });
        Assert.True(sp.ImapHost);
        Assert.False(sp.ImapUser);
        Assert.False(sp.SmtpHost);
        Assert.True(sp.SmtpUser);
        Assert.True(sp.Any);
        Assert.False(new ServerProblems().Any);

        var cfg = Fields.BuildConfig(
            new Identity { DisplayName = "Me", Email = " me@x.org ", Password = "p" }, "",
            new ServerFields { Host = " imap.x.org ", Port = 993, Security = Security.Tls, Username = "me@x.org" },
            new ServerFields { Host = "smtp.x.org", Port = 587, Security = Security.Starttls, Username = "me@x.org" });
        Assert.Equal("x.org", cfg.Name);
        Assert.Equal("me@x.org", cfg.Email);
        Assert.Equal("Me", cfg.DisplayName);
        Assert.Equal(AccountKind.Imap, cfg.Kind);
        Assert.Equal("imap.x.org", cfg.Imap?.Host);
        Assert.Equal(AuthMethod.Password, cfg.Imap?.AuthMethod);
        Assert.Equal(587, cfg.Smtp?.Port);
        Assert.Equal(AuthMethod.Password, cfg.Smtp?.AuthMethod);
        Assert.Null(cfg.OAuth2);
        Assert.Null(cfg.Graph);
        Assert.True(cfg.Imap?.CertificateSha256 is null && cfg.Smtp?.CertificateSha256 is null, "no pin without one");
        // A trusted certificate travels with its endpoint only.
        var pin = new string('a', 64);
        var bridge = Fields.BuildConfig(
            new Identity { Email = "me@x.org" }, "Bridge",
            new ServerFields { Host = "100.64.0.1", Port = 1143, Security = Security.Starttls, Username = "me", CertificateSha256 = pin },
            new ServerFields { Host = "100.64.0.1", Port = 1025, Security = Security.Starttls, Username = "me" });
        Assert.True(bridge.Imap?.CertificateSha256 == pin && bridge.Smtp?.CertificateSha256 is null);
        Assert.Equal("Bridge", bridge.Name);
        Assert.Equal("p", Fields.CredentialsFor(new Identity { Password = "p" }).Password);
        Assert.Null(Fields.CredentialsFor(new Identity()).Password);
    }

    [Fact]
    public void LinkedConfigAndMatch()
    {
        var graph = new AccountConfig
        {
            Name = "Me@Contoso.example",
            Email = "Me@Contoso.example",
            Kind = AccountKind.Graph,
            Graph = new GraphConfig { Source = GraphSource.Goa, GoaAccountId = "account_1_0" },
        };
        var google = new AccountConfig
        {
            Name = "Work",
            Email = "me@gmail.example",
            Imap = new ServerConfig { Host = "imap.gmail.example", Port = 993, Security = Security.Tls, Username = "me@gmail.example", AuthMethod = AuthMethod.OAuth2 },
            Smtp = new ServerConfig { Host = "smtp.gmail.example", Port = 465, Security = Security.Tls, Username = "me@gmail.example", AuthMethod = AuthMethod.OAuth2 },
            OAuth2 = new OAuth2Config { Source = OAuth2Source.Goa, GoaAccountId = "account_2_0", Provider = OAuth2Provider.Google },
        };
        var hint = new AccountConfig { Name = "", Email = "me@gmail.example", OAuth2 = new OAuth2Config { Source = OAuth2Source.Goa, Provider = OAuth2Provider.Google } };
        Assert.Equal("account_1_0", Linked.LinkedAccountId(graph));
        Assert.Equal("account_2_0", Linked.LinkedAccountId(google));
        Assert.Null(Linked.LinkedAccountId(hint));
        Assert.Null(Linked.LinkedAccountId(new AccountConfig { Name = "", Email = "" }));
        Assert.Null(Linked.LinkedAccountId(new AccountConfig { Name = "", Email = "", Graph = new GraphConfig { Source = GraphSource.Goa, GoaAccountId = "" } }));
        Assert.Null(Linked.LinkedAccountId(new AccountConfig { Name = "", Email = "", OAuth2 = new OAuth2Config { GoaAccountId = "x", Provider = OAuth2Provider.Google } }));

        // The identity page adds the display name; a name the daemon left at
        // the address becomes the suggested one, a chosen name stays.
        var cfg = Linked.WithIdentity(graph, new Identity { DisplayName = " Me ", Email = " Me@Contoso.example ", Password = "ignored" });
        Assert.Equal("contoso.example", cfg.Name);
        Assert.Equal("Me", cfg.DisplayName);
        Assert.Equal("Me@Contoso.example", cfg.Email);
        Assert.Equal("account_1_0", cfg.Graph?.GoaAccountId);
        var named = Linked.WithIdentity(google, new Identity());
        Assert.Equal("Work", named.Name);
        Assert.Null(named.DisplayName);

        LinkedAccount[] linked =
        [
            new LinkedAccount { Provider = LinkedProvider.Microsoft365, Email = "Me@Contoso.example", GoaAccountId = "account_1_0", Configured = false, AttentionNeeded = false },
        ];
        Assert.Equal("account_1_0", Linked.LinkedMatch(linked, " me@contoso.EXAMPLE ")?.GoaAccountId);
        Assert.Null(Linked.LinkedMatch(linked, "other@contoso.example"));

        Assert.StartsWith("This address belongs to a Google account.", Linked.GoaHintText("Google"), StringComparison.Ordinal);
        Assert.StartsWith("This address belongs to a Microsoft 365 account.", Linked.GoaHintText(null), StringComparison.Ordinal);
        Assert.Contains("Microsoft 365", Linked.GoaHintText(""), StringComparison.Ordinal);
    }

    // Windows: the identity never prints a value (§3.1: no addresses or
    // secrets in logs).
    [Fact]
    public void IdentityHidesItsValues()
    {
        var id = new Identity { DisplayName = "Me", Email = "me@x.org", Password = "hunter2" };
        foreach (var secret in new[] { "hunter2", "me@x.org", "Me" })
        {
            Assert.DoesNotContain(secret, id.ToString(), StringComparison.Ordinal);
        }
        Assert.Equal("Identity(displayName: <redacted>, email: <redacted>, password: <redacted>)", id.ToString());
        Assert.Equal("Identity(displayName: \"\", email: \"\", password: \"\")", new Identity().ToString());
    }
}
