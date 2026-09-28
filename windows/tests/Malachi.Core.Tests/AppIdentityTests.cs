// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Scaffold (phase B): the first Core test. The names are fixed by CLAUDE.md,
// and the app ID is a key of settings, notifications and stored secrets, so
// a change here must be deliberate.

using Xunit;

namespace Malachi.Core.Tests;

public sealed class AppIdentityTests
{
    [Fact]
    public void AppIdIsTheReverseDomainNameOfTheProject()
    {
        Assert.Equal("io.github.schotek.Malachi", AppIdentity.AppId);
    }

    [Fact]
    public void DisplayNameIsTheProductName()
    {
        Assert.Equal("Malachi Mail", AppIdentity.DisplayName);
    }
}
