// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Scaffold (phase B): until the protocol lands, the helper fails every
// operation, which the daemon reports as a keyringError.

using Xunit;

namespace Malachi.Credentials.Tests;

public sealed class ProgramTests
{
    [Theory]
    [InlineData]
    [InlineData("get")]
    [InlineData("set")]
    [InlineData("delete")]
    public void EveryOperationFails(params string[] args)
    {
        Assert.Equal(Program.Failed, Program.Main(args));
    }
}
