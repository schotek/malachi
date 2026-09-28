// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests of AppVersion, the port of
// macos/Sources/MalachiCore/Daemon/Version.swift, which has none: the
// short form is the rule of macos/Makefile (SHORT_VERSION), and the full
// one comes from the build (Directory.Build.props: MalachiVersion, "dev"
// without build.ps1).

using Malachi.Core.Daemon;
using Xunit;

namespace Malachi.Core.Tests.Daemon;

public sealed class AppVersionTests
{
    [Theory]
    [InlineData("0.1.0-33-gabc1234-dirty", "0.1.0")]
    [InlineData("0.1.0", "0.1.0")]
    [InlineData("1.2", "1.2")]
    [InlineData("7", "7")]
    [InlineData("1.2.3.4", "1.2.3")]
    [InlineData("12abc", "12")]
    [InlineData("abc1234", "0.0.0")]
    [InlineData("dev", "0.0.0")]
    [InlineData("", "0.0.0")]
    public void TheShortVersionIsOneToThreeIntegers(string full, string expected)
    {
        Assert.Equal(expected, AppVersion.ShortOf(full));
    }

    [Theory]
    [InlineData(null, "dev")]
    [InlineData("", "dev")]
    [InlineData("  ", "dev")]
    [InlineData("0.1.0-33-gabc1234-dirty", "0.1.0-33-gabc1234-dirty")]
    public void TheFullVersionIsTheBuildsOrDev(string? informational, string expected)
    {
        Assert.Equal(expected, AppVersion.FullOf(informational));
    }

    [Fact]
    public void TheVersionOfThisBuildIsConsistent()
    {
        Assert.False(string.IsNullOrEmpty(AppVersion.Full));
        Assert.Equal(AppVersion.ShortOf(AppVersion.Full), AppVersion.ShortVersion);
    }
}
