// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the private directories (Files/PrivateDirectory.cs), read
// back with GetAccessControl: owned by the user, a protected DACL with the
// user and SYSTEM only, links and files refused. The counterpart of the
// mode checks of macos/Tests/MalachiCoreTests/AttachmentsTests.swift
// (openDirWrite: 0700).

using System;
using System.IO;
using System.Linq;
using System.Security.AccessControl;
using System.Security.Principal;
using Malachi.Platform.Windows.Files;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Files;

public sealed class PrivateDirectoryTests
{
    private const InheritanceFlags Inherited = InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit;

    private static readonly SecurityIdentifier User = CurrentUser();
    private static readonly SecurityIdentifier LocalSystem = new(WellKnownSidType.LocalSystemSid, null);
    private static readonly SecurityIdentifier Everyone = new(WellKnownSidType.WorldSid, null);

    [Fact]
    public void EnsureCreatesAPrivateDirectory()
    {
        using var temp = new TestDirectory();
        var dir = temp.Combine("a", "b", "private");

        new PrivateDirectory().Ensure(dir);

        AssertPrivate(dir);
        // The parents it had to create are ordinary directories.
        Assert.False(new DirectoryInfo(temp.Combine("a")).GetAccessControl().AreAccessRulesProtected);
        Assert.True(new PrivateDirectory().IsPrivate(dir));
        Assert.False(new PrivateDirectory().IsPrivate(temp.Combine("a")));
    }

    [Fact]
    public void EnsureMakesAnExistingDirectoryPrivateAgain()
    {
        using var temp = new TestDirectory();
        var dir = temp.Combine("existing");
        Directory.CreateDirectory(dir);
        var file = Path.Combine(dir, "inside.txt");
        File.WriteAllText(file, "x");
        var security = new DirectoryInfo(dir).GetAccessControl();
        security.AddAccessRule(new FileSystemAccessRule(Everyone, FileSystemRights.Read, Inherited, PropagationFlags.None, AccessControlType.Allow));
        new DirectoryInfo(dir).SetAccessControl(security);
        Assert.Contains(Rules(dir), r => Everyone.Equals(r.IdentityReference));

        new PrivateDirectory().Ensure(dir);

        AssertPrivate(dir);
        // What it held before inherits the new entries only.
        var inside = new FileInfo(file).GetAccessControl();
        var rules = inside.GetAccessRules(true, true, typeof(SecurityIdentifier)).Cast<FileSystemAccessRule>().ToList();
        Assert.NotEmpty(rules);
        Assert.All(rules, r => Assert.True(User.Equals(r.IdentityReference) || LocalSystem.Equals(r.IdentityReference), r.IdentityReference.Value));
    }

    [Fact]
    public void EnsureOfAPrivateDirectoryKeepsIt()
    {
        using var temp = new TestDirectory();
        var dir = temp.Combine("private");
        var directories = new PrivateDirectory();

        directories.Ensure(dir);
        var before = new DirectoryInfo(dir).GetAccessControl().GetSecurityDescriptorSddlForm(AccessControlSections.Owner | AccessControlSections.Access);
        directories.Ensure(dir);

        AssertPrivate(dir);
        Assert.Equal(before, new DirectoryInfo(dir).GetAccessControl().GetSecurityDescriptorSddlForm(AccessControlSections.Owner | AccessControlSections.Access));
    }

    [Fact]
    public void FilesInsideInheritTheUserAndSystemOnly()
    {
        using var temp = new TestDirectory();
        var dir = temp.Combine("private");
        new PrivateDirectory().Ensure(dir);
        var file = Path.Combine(dir, "attachment.pdf");
        File.WriteAllBytes(file, [1, 2, 3]);
        var sub = Path.Combine(dir, "sub");
        Directory.CreateDirectory(sub);

        foreach (var security in new FileSystemSecurity[] { new FileInfo(file).GetAccessControl(), new DirectoryInfo(sub).GetAccessControl() })
        {
            var rules = security.GetAccessRules(true, true, typeof(SecurityIdentifier)).Cast<FileSystemAccessRule>().ToList();
            Assert.NotEmpty(rules);
            Assert.All(rules, r =>
            {
                Assert.True(r.IsInherited);
                Assert.Equal(AccessControlType.Allow, r.AccessControlType);
                Assert.True(User.Equals(r.IdentityReference) || LocalSystem.Equals(r.IdentityReference), r.IdentityReference.Value);
            });
        }
    }

    [Fact]
    public void EnsureRefusesAJunction()
    {
        using var temp = new TestDirectory();
        var target = temp.Combine("target");
        Directory.CreateDirectory(target);
        var link = temp.Combine("link");
        TestDirectory.CreateJunction(link, target);
        var before = new DirectoryInfo(target).GetAccessControl().GetSecurityDescriptorSddlForm(AccessControlSections.All & ~AccessControlSections.Audit);

        var e = Assert.Throws<IOException>(() => new PrivateDirectory().Ensure(link));

        Assert.Contains("link", e.Message, StringComparison.Ordinal);
        Assert.Equal(before, new DirectoryInfo(target).GetAccessControl().GetSecurityDescriptorSddlForm(AccessControlSections.All & ~AccessControlSections.Audit));
        Assert.False(new PrivateDirectory().IsPrivate(link));
    }

    [Fact]
    public void EnsureRefusesASymbolicLink()
    {
        using var temp = new TestDirectory();
        var target = temp.Combine("target");
        Directory.CreateDirectory(target);
        var link = temp.Combine("link");
        TestDirectory.CreateSymbolicLinkOrSkip(link, target);

        Assert.Throws<IOException>(() => new PrivateDirectory().Ensure(link));
        Assert.False(new DirectoryInfo(target).GetAccessControl().AreAccessRulesProtected);
    }

    [Fact]
    public void EnsureRefusesAFile()
    {
        using var temp = new TestDirectory();
        var file = temp.Combine("file");
        File.WriteAllText(file, "x");

        var e = Assert.Throws<IOException>(() => new PrivateDirectory().Ensure(file));

        Assert.Contains("not a directory", e.Message, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData("relative")]
    [InlineData("C:relative")]
    [InlineData("\\rooted-without-a-drive")]
    [InlineData("C:\\")]
    [InlineData("\\\\.\\C:\\device")]
    [InlineData("")]
    public void EnsureRefusesWhatIsNotADirectoryPath(string path)
    {
        Assert.ThrowsAny<ArgumentException>(() => new PrivateDirectory().Ensure(path));
    }

    [Fact]
    public void CreateNewCreatesAPrivateDirectoryOnce()
    {
        using var temp = new TestDirectory();
        var dir = temp.Combine("new");
        var directories = new PrivateDirectory();

        directories.CreateNew(dir);

        AssertPrivate(dir);
        var e = Assert.Throws<IOException>(() => directories.CreateNew(dir));
        Assert.Equal(unchecked((int)0x800700B7), e.HResult); // ERROR_ALREADY_EXISTS
        File.WriteAllText(temp.Combine("file"), "x");
        Assert.Throws<IOException>(() => directories.CreateNew(temp.Combine("file")));
    }

    [Fact]
    public void CreateNewNeedsItsParent()
    {
        using var temp = new TestDirectory();

        Assert.Throws<DirectoryNotFoundException>(() => new PrivateDirectory().CreateNew(temp.Combine("missing", "new")));
    }

    [Fact]
    public void LongPathsWork()
    {
        using var temp = new TestDirectory();
        var dir = temp.Combine(new string('x', 100), new string('y', 100), new string('z', 100));
        Assert.True(dir.Length > 260);

        new PrivateDirectory().Ensure(dir);
        new PrivateDirectory().CreateNew(Path.Combine(dir, "sub"));

        AssertPrivate(dir);
        AssertPrivate(Path.Combine(dir, "sub"));
    }

    private static void AssertPrivate(string dir)
    {
        var security = new DirectoryInfo(dir).GetAccessControl(AccessControlSections.Owner | AccessControlSections.Access);
        Assert.Equal(User, security.GetOwner(typeof(SecurityIdentifier)));
        Assert.True(security.AreAccessRulesProtected, "the DACL should not inherit");
        var rules = Rules(dir);
        Assert.Equal(2, rules.Length);
        Assert.All(rules, r =>
        {
            Assert.Equal(AccessControlType.Allow, r.AccessControlType);
            Assert.False(r.IsInherited);
            Assert.Equal(FileSystemRights.FullControl, r.FileSystemRights);
            Assert.Equal(Inherited, r.InheritanceFlags);
            Assert.Equal(PropagationFlags.None, r.PropagationFlags);
        });
        Assert.Equal(
            new[] { User.Value, LocalSystem.Value }.Order(StringComparer.Ordinal),
            rules.Select(r => r.IdentityReference.Value).Order(StringComparer.Ordinal));
    }

    private static FileSystemAccessRule[] Rules(string dir) =>
        new DirectoryInfo(dir).GetAccessControl().GetAccessRules(true, true, typeof(SecurityIdentifier))
            .Cast<FileSystemAccessRule>().ToArray();

    private static SecurityIdentifier CurrentUser()
    {
        using var identity = WindowsIdentity.GetCurrent();
        return identity.User!;
    }
}
