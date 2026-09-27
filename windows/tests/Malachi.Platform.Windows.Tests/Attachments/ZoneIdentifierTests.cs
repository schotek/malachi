// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the Zone.Identifier stream (Attachments/ZoneIdentifier.cs),
// written and read on NTFS, and parsed as Attachment Services and others
// write it.

using System.IO;
using Malachi.Platform.Windows.Attachments;
using Malachi.Platform.Windows.Tests.Files;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Attachments;

public sealed class ZoneIdentifierTests
{
    [Theory]
    [InlineData("[ZoneTransfer]\r\nZoneId=3\r\n", 3)]
    [InlineData("[ZoneTransfer]\r\nZoneId=4\r\n", 4)]
    [InlineData("[ZoneTransfer]\r\nZoneId=3\r\nReferrerUrl=https://example.com/\r\nHostUrl=https://example.com/a.pdf\r\n", 3)]
    [InlineData("[ZoneTransfer]\nZoneId=3\nHostUrl=about:internet\n", 3)]
    [InlineData("﻿[ZoneTransfer]\r\nZoneId=4", 4)]
    [InlineData("[zonetransfer]\r\n zoneid = 4 \r\n", 4)]
    [InlineData("[Other]\r\nZoneId=1\r\n[ZoneTransfer]\r\nZoneId=3\r\n", 3)]
    [InlineData("[ZoneTransfer]\r\nZoneIdX=3\r\nZoneId=4\r\n", 4)]
    [InlineData("ZoneId=3\r\n", null)]
    [InlineData("[ZoneTransfer]\r\nZoneId=\r\n", null)]
    [InlineData("[ZoneTransfer]\r\nZoneId=three\r\n", null)]
    [InlineData("[ZoneTransfer]\r\nHostUrl=about:internet\r\n", null)]
    [InlineData("", null)]
    [InlineData(null, null)]
    public void ParseReadsTheZoneOfTheSection(string? text, int? want)
    {
        Assert.Equal(want, ZoneIdentifier.Parse(text));
    }

    [Fact]
    public void WriteThenReadRoundTrips()
    {
        using var temp = new TestDirectory();
        var path = temp.Combine("a.txt");
        File.WriteAllText(path, "content");

        Assert.Null(ZoneIdentifier.Read(path));
        ZoneIdentifier.Write(path, ZoneIdentifier.Restricted);
        Assert.Equal(4, ZoneIdentifier.Read(path));
        ZoneIdentifier.Write(path, ZoneIdentifier.Internet, "about:internet");
        Assert.Equal(3, ZoneIdentifier.Read(path));

        Assert.Equal("[ZoneTransfer]\r\nZoneId=3\r\nHostUrl=about:internet\r\n", File.ReadAllText(path + ":Zone.Identifier"));
        // The stream is beside the content, not in it.
        Assert.Equal("content", File.ReadAllText(path));
        Assert.Single(Directory.GetFileSystemEntries(temp.Path));
    }

    [Fact]
    public void ReadOfNothingIsNull()
    {
        using var temp = new TestDirectory();

        Assert.Null(ZoneIdentifier.Read(temp.Combine("missing.txt")));
        Assert.Null(ZoneIdentifier.Read(temp.Path));
    }

    [Fact]
    public void AnOversizedStreamIsNotOurs()
    {
        using var temp = new TestDirectory();
        var path = temp.Combine("a.txt");
        File.WriteAllText(path, "x");
        File.WriteAllText(path + ":Zone.Identifier", "[ZoneTransfer]\r\nZoneId=3\r\n" + new string('x', 70 * 1024));

        Assert.Null(ZoneIdentifier.Read(path));
    }

    [Fact]
    public void TheNamesAreThoseWindowsReads()
    {
        Assert.Equal("Zone.Identifier", ZoneIdentifier.StreamName);
        Assert.Equal(3, ZoneIdentifier.Internet);
        Assert.Equal(4, ZoneIdentifier.Restricted);
    }
}
