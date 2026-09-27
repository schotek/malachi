// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CredentialsTests.swift.
//
// Credentials is write-only (docs/security.md §6): printing one never shows
// the password. Swift's description, debugDescription, String(describing:),
// String(reflecting:) and interpolation are, in C#, ToString, the debugger's
// display, formatting, interpolation, and the printed form of a record that
// holds the credentials.

using System;
using System.Diagnostics;
using System.Globalization;
using System.Reflection;
using Malachi.Core.Api;
using Xunit;

namespace Malachi.Core.Tests.Api;

public sealed class CredentialsTests
{
    private static string[] Printed(Credentials c) =>
    [
        c.ToString(),
        $"{c}",
        string.Format(CultureInfo.InvariantCulture, "{0}", c),
        Convert.ToString(c, CultureInfo.InvariantCulture)!,
        new AccountAddParams { Config = new AccountConfig { Name = "n", Email = "e@x" }, Credentials = c }.ToString(),
    ];

    [Fact]
    public void PrintingRedacts()
    {
        var c = new Credentials { Password = "hunter2" };
        foreach (var text in Printed(c))
        {
            Assert.DoesNotContain("hunter2", text, StringComparison.Ordinal);
            Assert.Contains("<redacted>", text, StringComparison.Ordinal);
        }
        // The debugger shows the same text.
        Assert.Equal("{ToString(),nq}", typeof(Credentials).GetCustomAttribute<DebuggerDisplayAttribute>()!.Value);
        Assert.Equal("Credentials(password: nil, oauthSession: nil)", new Credentials().ToString());
        // Still encodes for the daemon.
        Assert.Contains("hunter2", JsonCoding.EncodeToString(c), StringComparison.Ordinal);
    }

    [Fact]
    public void PrintingHidesTheSession()
    {
        var c = new Credentials { OAuthSession = "s_secret42" };
        foreach (var text in Printed(c))
        {
            Assert.DoesNotContain("s_secret42", text, StringComparison.Ordinal);
            Assert.Contains("oauthSession: <set>", text, StringComparison.Ordinal);
        }
        Assert.Equal("Credentials(password: nil, oauthSession: <set>)", c.ToString());
        Assert.Contains("s_secret42", JsonCoding.EncodeToString(c), StringComparison.Ordinal);
    }
}
