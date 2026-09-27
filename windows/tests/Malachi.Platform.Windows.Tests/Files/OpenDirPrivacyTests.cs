// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: Malachi.Core.Platform.OpenDir over the real private
// directories, the counterpart of the mode checks of
// macos/Tests/MalachiCoreTests/AttachmentsTests.swift (openDirWrite: the
// directory and each subdirectory 0700, the file 0600).

using System;
using System.IO;
using System.Linq;
using System.Security.AccessControl;
using System.Security.Principal;
using Malachi.Core.Platform;
using Malachi.Platform.Windows.Files;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Files;

public sealed class OpenDirPrivacyTests
{
    private static readonly SecurityIdentifier LocalSystem = new(WellKnownSidType.LocalSystemSid, null);

    [Fact]
    public void OpenDirWrite()
    {
        using var temp = new TestDirectory();
        var directories = new PrivateDirectory();
        var open = OpenDir.InDataDirectory(temp.Path, directories, TimeProvider.System);

        var path = open.Write("report.pdf", "%PDF-1.7"u8);

        Assert.True(directories.IsPrivate(open.Path));
        Assert.True(directories.IsPrivate(Path.GetDirectoryName(path)!));
        using var identity = WindowsIdentity.GetCurrent();
        var rules = new FileInfo(path).GetAccessControl().GetAccessRules(true, true, typeof(SecurityIdentifier))
            .Cast<FileSystemAccessRule>().ToList();
        Assert.NotEmpty(rules);
        Assert.All(rules, r => Assert.True(
            identity.User!.Equals(r.IdentityReference) || LocalSystem.Equals(r.IdentityReference),
            r.IdentityReference.Value));

        open.RemoveAll();

        Assert.False(Directory.Exists(open.Path));
    }

    [Fact]
    public void AnOpenDirectoryThatIsALinkIsRefused()
    {
        using var temp = new TestDirectory();
        var elsewhere = temp.Combine("elsewhere");
        Directory.CreateDirectory(elsewhere);
        File.WriteAllText(Path.Combine(elsewhere, "keep.txt"), "x");
        TestDirectory.CreateJunction(temp.Combine("open"), elsewhere);
        var open = OpenDir.InDataDirectory(temp.Path, new PrivateDirectory(), TimeProvider.System);

        Assert.Throws<IOException>(() => open.Write("a.txt", "x"u8));
        open.RemoveAll();

        Assert.True(File.Exists(Path.Combine(elsewhere, "keep.txt")));
        Assert.Single(Directory.GetFileSystemEntries(elsewhere));
        Assert.False(Directory.Exists(temp.Combine("open")));
    }
}
