// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the key-file policy (Transport/WindowsKeyFilePolicy.cs), the
// counterpart of the owner and mode cases of the DaemonKeyTests suite of
// macos/Tests/MalachiCoreTests/AuthTests.swift (refusesAFileOthersMayRead,
// refusesASymbolicLink, refusesAFIFOAtOnce, refusesDirectoriesAndDevices),
// through DaemonKey as the client reads the key. The DACLs are written as
// SDDL, so each case says exactly which entries the file has.

using System;
using System.IO;
using System.IO.Pipes;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;
using Malachi.Platform.Windows.Tests.Files;
using Malachi.Platform.Windows.Transport;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Transport;

public sealed class WindowsKeyFilePolicyTests
{
    private static readonly SecurityIdentifier User = CurrentUser();

    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    /// <summary>The daemon's key file in an ordinary directory inherits the user, SYSTEM and Administrators (measured).</summary>
    [Fact]
    public async Task ReadsTheKeyTheDaemonWrote()
    {
        using var dir = new TestDirectory();
        var key = RpcAuth.NewNonce();
        var path = WriteKey(dir, key);
        Assert.Equal(key, await ReadAsync(path));
    }

    /// <summary>In the run directory the client protects, the key file has the user alone.</summary>
    [Fact]
    public async Task ReadsAKeyOnlyTheUserMayRead()
    {
        using var dir = new TestDirectory();
        var key = RpcAuth.NewNonce();
        var path = WriteKey(dir, key);
        SetDacl(path, $"D:P(A;;FA;;;{User})");
        Assert.Equal(key, await ReadAsync(path));
        SetDacl(path, $"D:P(A;;FR;;;{User})(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;OW)");
        Assert.Equal(key, await ReadAsync(path));
    }

    /// <summary>Swift's refusesAFileOthersMayRead: every entry that lets someone else read or change the key.</summary>
    [Theory]
    [InlineData("(A;;FR;;;WD)", "Everyone may read")]
    [InlineData("(A;;0x1;;;BU)", "Users may read the data")]
    [InlineData("(A;;0x2;;;AU)", "Authenticated Users may write the data")]
    [InlineData("(A;;0x4;;;WD)", "Everyone may append")]
    [InlineData("(A;;GR;;;WD)", "Everyone has GENERIC_READ")]
    [InlineData("(A;;GA;;;S-1-5-21-1-2-3-1001)", "another user has GENERIC_ALL")]
    [InlineData("(A;;WD;;;WD)", "Everyone may change the DACL")]
    [InlineData("(A;;WO;;;WD)", "Everyone may take the file")]
    [InlineData("(A;;FA;;;AC)", "all app containers")]
    public async Task RefusesAFileOthersMayRead(string entry, string what)
    {
        using var dir = new TestDirectory();
        var path = WriteKey(dir, RpcAuth.NewNonce());
        SetDacl(path, $"D:P(A;;FA;;;{User})(A;;FA;;;SY){entry}");
        Assert.True($"{path} is accessible to other users" == await RefusalAsync(path), what);
    }

    /// <summary>Rights that do not reach the key, and entries that only take away, are no one's access.</summary>
    [Fact]
    public async Task AcceptsWhatDoesNotReachTheKey()
    {
        using var dir = new TestDirectory();
        var key = RpcAuth.NewNonce();
        var path = WriteKey(dir, key);
        // Attributes, the security descriptor, synchronising, deleting.
        SetDacl(path, $"D:P(A;;FA;;;{User})(A;;0x100080;;;WD)(A;;RC;;;BU)(A;;SD;;;AU)");
        Assert.Equal(key, await ReadAsync(path));
        // A deny entry for everyone else.
        SetDacl(path, $"D:P(D;;FA;;;S-1-5-21-1-2-3-1001)(A;;FA;;;{User})");
        Assert.Equal(key, await ReadAsync(path));
        // CREATOR OWNER stands for whoever creates an object below a
        // directory; on a file it is nobody's (no token carries it).
        SetDacl(path, $"D:P(A;;FA;;;{User})(A;;FA;;;S-1-3-0)");
        Assert.Equal(key, await ReadAsync(path));
    }

    /// <summary>A NULL DACL lets everyone in.</summary>
    [Fact]
    public async Task RefusesANullDacl()
    {
        using var dir = new TestDirectory();
        var path = WriteKey(dir, RpcAuth.NewNonce());
        var security = new FileSecurity();
        security.SetSecurityDescriptorSddlForm("D:NO_ACCESS_CONTROL", AccessControlSections.Access);
        new FileInfo(path).SetAccessControl(security);
        // Written as a NULL DACL: nothing in the descriptor restricts access.
        var written = new RawSecurityDescriptor(new FileInfo(path).GetAccessControl(AccessControlSections.Access).GetSecurityDescriptorBinaryForm(), 0);
        Assert.Null(written.DiscretionaryAcl);
        Assert.Equal($"{path} is accessible to other users", await RefusalAsync(path));
    }

    /// <summary>
    /// A file of another user. Giving a file away needs the restore
    /// privilege, which a test does not have; the policy is asked as if
    /// another user ran it instead.
    /// </summary>
    [Fact]
    public async Task RefusesAFileOfAnotherUser()
    {
        using var dir = new TestDirectory();
        var path = WriteKey(dir, RpcAuth.NewNonce());
        var stranger = new SecurityIdentifier("S-1-5-21-1-2-3-1001");
        var policy = new WindowsKeyFilePolicy(stranger, stranger);
        var e = await Assert.ThrowsAsync<KeyUnavailableException>(() => DaemonKey.ReadAsync(path, policy, TimeProvider.System, Ct));
        Assert.Equal($"{path} belongs to another user", e.Reason);
    }

    /// <summary>A file really given to another owner, where Windows lets the test do that (an elevated run).</summary>
    [Fact]
    public async Task RefusesAFileGivenToAnotherOwner()
    {
        using var dir = new TestDirectory();
        var path = WriteKey(dir, RpcAuth.NewNonce());
        var security = new FileSecurity();
        security.SetOwner(new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null));
        try
        {
            new FileInfo(path).SetAccessControl(security);
        }
        catch (Exception x) when (x is UnauthorizedAccessException or InvalidOperationException or PrivilegeNotHeldException)
        {
            Assert.Skip($"Windows refuses to give the file to SYSTEM here ({x.GetType().Name}); RefusesAFileOfAnotherUser covers the check");
        }
        Assert.Equal($"{path} belongs to another user", await RefusalAsync(path));
    }

    /// <summary>Swift's refusesASymbolicLink: a link is opened as itself and refused, and so is a junction, which needs no privilege.</summary>
    [Fact]
    public async Task RefusesAReparsePoint()
    {
        using var dir = new TestDirectory();
        var target = WriteKey(dir, RpcAuth.NewNonce());
        var junction = dir.Combine("junction.key");
        Directory.CreateDirectory(dir.Combine("elsewhere"));
        TestDirectory.CreateJunction(junction, dir.Combine("elsewhere"));
        Assert.Equal($"{junction} is a symbolic link", await RefusalAsync(junction));

        var link = dir.Combine("link.key");
        try
        {
            File.CreateSymbolicLink(link, target);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            Assert.Skip($"cannot create a symbolic link here ({e.GetType().Name}); Windows needs Developer Mode or the privilege");
        }
        Assert.Equal($"{link} is a symbolic link", await RefusalAsync(link));
    }

    /// <summary>Swift's refusesAFIFOAtOnce: a named pipe opens like a file, and is refused without waiting.</summary>
    [Fact]
    public async Task RefusesANamedPipeAtOnce()
    {
        var name = "malachi-key-" + Guid.NewGuid().ToString("N")[..8];
        await using var server = new NamedPipeServerStream(name, PipeDirection.InOut, 1, PipeTransmissionMode.Byte, PipeOptions.Asynchronous);
        var path = $@"\\.\pipe\{name}";
        var started = DateTime.UtcNow;
        Assert.Equal($"{path} is not a regular file", await RefusalAsync(path));
        Assert.True(DateTime.UtcNow - started < TimeSpan.FromSeconds(1));
    }

    /// <summary>Swift's refusesDirectoriesAndDevices.</summary>
    [Fact]
    public async Task RefusesDirectoriesAndDevices()
    {
        using var dir = new TestDirectory();
        var sub = dir.Combine("dir.key");
        Directory.CreateDirectory(sub);
        Assert.Equal($"{sub} is not a regular file", await RefusalAsync(sub));
        Assert.Equal("NUL is not a regular file", await RefusalAsync("NUL"));
    }

    [Fact]
    public async Task RefusesAMissingFile()
    {
        using var dir = new TestDirectory();
        var path = dir.Combine("missing.key");
        Assert.Equal($"{path} does not exist", await RefusalAsync(path));
        var deeper = dir.Combine("gone", "rpc.sock.key");
        Assert.Equal($"{deeper} does not exist", await RefusalAsync(deeper));
    }

    /// <summary>A sharing violation from CreateFile is retried as File.OpenHandle's is.</summary>
    [Fact]
    public async Task RetriesWhileTheFileIsHeld()
    {
        using var dir = new TestDirectory();
        var key = RpcAuth.NewNonce();
        var path = WriteKey(dir, key);
        var held = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.None);
        var read = DaemonKey.ReadAsync(path, new WindowsKeyFilePolicy(), TimeProvider.System, Ct);
        Assert.False(read.IsCompleted); // the first attempt failed and waits for the next
        held.Dispose();
        Assert.Equal(key, await read);
    }

    /// <summary>The daemon removes its key file when it quits, maybe while the client reads it.</summary>
    [Fact]
    public void TheReaderLetsTheDaemonRemoveTheFile()
    {
        using var dir = new TestDirectory();
        var path = WriteKey(dir, RpcAuth.NewNonce());
        using (new WindowsKeyFilePolicy().Open(path))
        {
            File.Delete(path);
        }
        Assert.False(File.Exists(path));
    }

    private static string WriteKey(TestDirectory dir, byte[] key)
    {
        var path = dir.Combine("rpc.sock.key");
        File.WriteAllBytes(path, Encoding.ASCII.GetBytes(RpcAuth.Hex(key) + "\n"));
        return path;
    }

    // Replaces the file's DACL with the SDDL's; the owner stays the user.
    private static void SetDacl(string path, string sddl)
    {
        var security = new FileSecurity();
        security.SetSecurityDescriptorSddlForm(sddl, AccessControlSections.Access);
        new FileInfo(path).SetAccessControl(security);
    }

    private static Task<byte[]> ReadAsync(string path) => DaemonKey.ReadAsync(path, new WindowsKeyFilePolicy(), TimeProvider.System, Ct);

    private static async Task<string?> RefusalAsync(string path)
    {
        try
        {
            await ReadAsync(path);
            Assert.Fail($"{path} was read");
        }
        catch (KeyUnavailableException e)
        {
            return e.Reason;
        }
        return null;
    }

    private static SecurityIdentifier CurrentUser()
    {
        using var identity = WindowsIdentity.GetCurrent();
        return identity.User!;
    }
}
