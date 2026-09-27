// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The exit wording of DaemonSupervisor.swift ("exited with status N", "died
// of signal N") and MCPRegistrationTests.swift (exitDescription: 3 and -9),
// for Windows: -1 is what a kill leaves, and an NTSTATUS reads in hex.

using Malachi.Core.Daemon;
using Xunit;

namespace Malachi.Core.Tests.Daemon;

public sealed class ExitStatusTests
{
    [Theory]
    [InlineData(0, "exited with status 0")]
    [InlineData(3, "exited with status 3")]
    [InlineData(-1, "was killed")]
    [InlineData(unchecked((int)0xC0000005), "died with status 0xC0000005")]
    [InlineData(unchecked((int)0xC000013A), "died with status 0xC000013A")]
    [InlineData(-9, "died with status 0xFFFFFFF7")]
    public void DescribesHowAProcessEnded(int code, string expected)
    {
        Assert.Equal(expected, ExitStatus.Describe(code));
    }
}
