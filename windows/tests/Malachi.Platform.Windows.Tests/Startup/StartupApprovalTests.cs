// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of StartupApproval against the values read on the development
// machine (APP-SPIKES §6): 12-byte REG_BINARY, byte 0 even for enabled
// (02, 06) and odd for disabled (03, 07), a FILETIME in bytes 4 to 11 that
// may be zero; no value, or one that is not binary, is no approval (enabled).

using System;
using Malachi.Platform.Windows.Startup;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Startup;

public sealed class StartupApprovalTests
{
    [Theory]
    [InlineData("02 00 00 00 00 00 00 00 00 00 00 00", true)]
    [InlineData("06 00 00 00 00 00 00 00 00 00 00 00", true)]
    [InlineData("03 00 00 00 00 00 00 00 00 00 00 00", false)]
    [InlineData("07 00 00 00 00 00 00 00 00 00 00 00", false)]
    [InlineData("03", false)]
    [InlineData("02", true)]
    public void TheFirstByteDecides(string hex, bool enabled)
    {
        var approval = StartupApproval.Decode(Bytes(hex));
        Assert.NotNull(approval);
        Assert.Equal(enabled, approval.Value.Enabled);
        Assert.Null(approval.Value.DisabledAt);
    }

    [Fact]
    public void ADisabledEntryCarriesWhenItWasDisabled()
    {
        var at = new DateTime(2026, 9, 27, 13, 11, 55, DateTimeKind.Utc);
        var value = new byte[12];
        value[0] = 0x03;
        BitConverter.GetBytes(at.ToFileTimeUtc()).CopyTo(value, 4);
        Assert.Equal(new StartupApproval(false, at), StartupApproval.Decode(value));
        // An enabled entry's time is not a time it was disabled.
        value[0] = 0x02;
        Assert.Equal(new StartupApproval(true, null), StartupApproval.Decode(value));
        // A time out of range is left out.
        value[0] = 0x03;
        BitConverter.GetBytes(long.MaxValue).CopyTo(value, 4);
        Assert.Equal(new StartupApproval(false, null), StartupApproval.Decode(value));
    }

    [Fact]
    public void NoApprovalIsNull()
    {
        Assert.Null(StartupApproval.Decode(null));
        Assert.Null(StartupApproval.Decode(Array.Empty<byte>()));
        Assert.Null(StartupApproval.Decode("03"));
        Assert.Null(StartupApproval.Decode(3));
    }

    private static byte[] Bytes(string hex) => Convert.FromHexString(hex.Replace(" ", "", StringComparison.Ordinal));
}
