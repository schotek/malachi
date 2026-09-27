// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of SelfRegistration: a copy started with MALACHI_DATA_DIR (a test,
// an agent, a dev build in a temporary folder) leaves the user's mailto:
// registration and Run value alone at start; an empty value counts as
// unset, as it does for the data directory.

using Malachi.Platform.Windows.Registration;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Registration;

public sealed class SelfRegistrationTests
{
    [Theory]
    [InlineData(null, true)]
    [InlineData("", true)]
    [InlineData(@"C:\Users\me\AppData\Local\Temp\malachi-test", false)]
    [InlineData(" ", false)]
    public void OnlyACopyWithoutADataDirectoryOverrideRegistersItself(string? dataDir, bool allowed)
    {
        Assert.Equal(allowed, SelfRegistration.IsAllowed(dataDir));
    }

    [Fact]
    public void TheVariableIsTheDataDirectorys()
    {
        Assert.Equal("MALACHI_DATA_DIR", SelfRegistration.DataDirVariable);
    }
}
