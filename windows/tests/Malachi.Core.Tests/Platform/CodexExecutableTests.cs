// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first Codex discovery safety, following ClaudeCodeLocatorTests;
// GTK reference: ui/internal/assistantpanel/locator_test.go; no Swift port yet.

using System.Text;
using Malachi.Core.Platform;
using Xunit;

namespace Malachi.Core.Tests.Platform;

public sealed class CodexExecutableTests
{
    [Theory]
    [InlineData(@"C:\Users\Example\codex.cmd")]
    [InlineData(@"C:\Users\Example\codex.ps1")]
    [InlineData("codex.exe")]
    [InlineData(@"wsl.exe codex")]
    [InlineData(@"\\wsl.localhost\Ubuntu\usr\bin\codex")]
    public void NeverAcceptsShellShimsOrCommands(string path) => Assert.False(CodexExecutable.IsExecutableFile(path));

    [Theory]
    [InlineData("codex-cli 0.159.0", "codex-cli 0.159.0")]
    [InlineData("codex-cli 0.159.0-alpha.12.1\r\n", "codex-cli 0.159.0-alpha.12.1")]
    [InlineData("codex 1.2.3", "codex 1.2.3")]
    [InlineData("codex-cli 1.2.3\nsecret canary", null)]
    [InlineData("secret canary", null)]
    [InlineData("\u001b[31mcodex-cli 1.2.3", null)]
    public void VersionDisplayIsStrictlyBounded(string output, string? expected) =>
        Assert.Equal(expected, CodexExecutable.SafeVersion(Encoding.UTF8.GetBytes(output), 0));

    [Fact]
    public void FailedOrOversizedVersionIsNeverDisplayed()
    {
        Assert.Null(CodexExecutable.SafeVersion(Encoding.UTF8.GetBytes("codex-cli 1.2.3"), 1));
        Assert.Null(CodexExecutable.SafeVersion(Encoding.UTF8.GetBytes("codex-cli 1.2.3+" + new string('x', 300)), 0));
    }
}
