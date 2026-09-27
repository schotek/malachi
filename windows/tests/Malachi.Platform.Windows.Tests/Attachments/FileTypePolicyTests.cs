// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: what is never opened (Attachments/FileTypePolicy.cs), the
// counterpart of macos/Tests/MalachiCoreTests/AttachmentsTests.swift
// (claimedTypesTest: the system's own judgement next to the name lists),
// with AssocIsDangerous in the place of the type system.

using System.Collections.Generic;
using Malachi.Core.Platform;
using Malachi.Platform.Windows.Attachments;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Attachments;

public sealed class FileTypePolicyTests
{
    // Every extension AssocIsDangerous names on Windows 11 26100 (probed
    // over some 200 candidates; it matches the attachment policy's blocks).
    public static readonly TheoryData<string> ShellDangerous = new()
    {
        "ade", "adp", "app", "appref-ms", "asp", "bas", "bat", "cdxml", "cer", "chm", "cmd", "cnt", "com",
        "cpl", "crt", "der", "exe", "fxp", "gadget", "grp", "hlp", "hpj", "hta", "img", "inf", "ins", "iso",
        "isp", "its", "js", "jse", "ksh", "library-ms", "lnk", "mad", "maf", "mag", "mam", "maq", "mar",
        "mas", "mat", "mau", "mav", "maw", "mcf", "mda", "mdb", "mde", "mdt", "mdw", "mdz", "msc", "msh",
        "msh1", "msh1xml", "msh2", "msh2xml", "mshxml", "msi", "msp", "mst", "msu", "ocx", "ops", "pcd",
        "pif", "pl", "plg", "prf", "prg", "printerexport", "ps1", "ps1xml", "ps2", "ps2xml", "psc1", "psc2",
        "psd1", "psm1", "pssc", "pst", "reg", "scf", "scr", "sct", "shb", "shs", "theme", "tmp", "udl",
        "url", "vb", "vbe", "vbp", "vbs", "vhd", "vhdx", "vsmacros", "vsw", "webpnp", "website", "ws", "wsc",
        "wsf", "wsh", "xnk",
    };

    [Theory]
    [InlineData("setup.exe", true)]
    [InlineData("shortcut.lnk", true)]
    [InlineData("report.pdf", false)]
    [InlineData("letter.docx", false)]
    [InlineData("photo.jpg", false)]
    [InlineData("connect.rdp", true)]
    [InlineData("setup.exe.", true)]
    [InlineData("", false)]
    [InlineData(null, false)]
    public void IsDangerous(string? name, bool want)
    {
        Assert.Equal(want, new FileTypePolicy().IsDangerous(name, "application/octet-stream"));
    }

    [Theory]
    [InlineData(".exe", true)]
    [InlineData("exe", true)]
    [InlineData(".lnk", true)]
    [InlineData(".pdf", false)]
    [InlineData(".txt", false)]
    [InlineData("", false)]
    [InlineData(null, false)]
    // Missed by the shell, listed by DangerousTypes (measured).
    [InlineData(".rdp", false)]
    [InlineData(".search-ms", false)]
    [InlineData(".searchconnector-ms", false)]
    [InlineData(".settingcontent-ms", false)]
    [InlineData(".appinstaller", false)]
    [InlineData(".msix", false)]
    [InlineData(".xll", false)]
    [InlineData(".jar", false)]
    public void TheShellsOwnVerdict(string? extension, bool want)
    {
        Assert.Equal(want, FileTypePolicy.IsDangerousToTheShell(extension));
    }

    [Theory]
    [MemberData(nameof(ShellDangerous))]
    public void TheListsHaveWhatTheShellCallsDangerous(string extension)
    {
        Assert.True(FileTypePolicy.IsDangerousToTheShell(extension), $".{extension} is no longer dangerous to the shell");
        Assert.Contains(extension, DangerousTypes.Extensions);
    }

    [Fact]
    public void TheShellIsAskedAboutTheGivenAndTheWrittenName()
    {
        var asked = new List<string>();
        var policy = new FileTypePolicy(ext =>
        {
            asked.Add(ext);
            return ext == "newtype";
        });

        Assert.True(policy.IsDangerous("data.newtype", null));
        Assert.False(policy.IsDangerous("data.oldtype", null));
        Assert.True(policy.IsDangerous("data.newtype.", null));
        // A listed type needs no question.
        Assert.True(policy.IsDangerous("setup.exe", null));
        Assert.True(policy.IsDangerous("blob", "application/x-msdownload"));

        Assert.Equal(["newtype", "oldtype", "newtype"], asked);
    }
}
