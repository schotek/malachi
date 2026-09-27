// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the DaemonKeyTests suite of
// macos/Tests/MalachiCoreTests/AuthTests.swift (the rest of AuthTests is
// Api/AuthTests.cs), with Go's TestReadKeyFile (backend/pkg/api/auth_test.go):
// the checks every key-file policy gets from DaemonKey itself, under the
// portable policy (the Go clients' rule). What Swift checks of the owner
// and the mode, and the named pipe and the device, are the Windows policy's
// (Malachi.Platform.Windows.Tests, WindowsKeyFilePolicyTests); a FIFO has no
// counterpart in a Windows file system. Added: the retry while another
// process holds the file without sharing it.

using System;
using System.IO;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Tests.Platform;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Transport;

public sealed class DaemonKeyTests
{
    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    [Fact]
    public async Task ReadsTheKeyTheDaemonWrote()
    {
        using var dir = new TemporaryDirectory();
        var key = RpcAuth.NewNonce();
        var path = Write(KeyFile(key), "rpc.sock.key", dir);
        Assert.Equal(key, await ReadAsync(path));
        // Read-only is fine as well.
        File.SetAttributes(path, FileAttributes.ReadOnly);
        try
        {
            Assert.Equal(key, await ReadAsync(path));
        }
        finally
        {
            File.SetAttributes(path, FileAttributes.Normal);
        }
    }

    [Fact]
    public async Task RefusesAMissingFile()
    {
        using var dir = new TemporaryDirectory();
        var path = Path.Combine(dir.Path, "missing.key");
        Assert.Equal($"{path} does not exist", await RefusalAsync(path));
        // A missing directory is a missing file too.
        var deeper = Path.Combine(dir.Path, "gone", "rpc.sock.key");
        Assert.Equal($"{deeper} does not exist", await RefusalAsync(deeper));
    }

    [Fact]
    public async Task RefusesASymbolicLink()
    {
        using var dir = new TemporaryDirectory();
        var target = Write(KeyFile(RpcAuth.NewNonce()), "target.key", dir);
        var link = Path.Combine(dir.Path, "link.key");
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

    [Fact]
    public async Task RefusesDirectories()
    {
        using var dir = new TemporaryDirectory();
        var sub = Path.Combine(dir.Path, "dir.key");
        Directory.CreateDirectory(sub);
        Assert.Equal($"{sub} is not a regular file", await RefusalAsync(sub));
    }

    [Fact]
    public async Task RefusesADevice()
    {
        var device = OperatingSystem.IsWindows() ? "NUL" : "/dev/null";
        Assert.NotNull(await RefusalAsync(device));
    }

    [Fact]
    public async Task RefusesWhatIsNotAKeyFile()
    {
        using var dir = new TemporaryDirectory();
        var good = RpcAuth.Hex(RpcAuth.NewNonce());
        (string Name, byte[] Content)[] sized =
        [
            ("empty", []),
            ("no newline", Encoding.ASCII.GetBytes(good)),
            ("two keys", Encoding.ASCII.GetBytes(good + "\n" + good + "\n")),
            ("10 MiB", new byte[10 << 20]),
            ("CRLF", Encoding.ASCII.GetBytes(good + "\r\n")),
        ];
        foreach (var (name, content) in sized)
        {
            var path = Write(content, name, dir);
            Assert.Equal($"{path} is not 65 bytes", await RefusalAsync(path));
        }
        // The right size, the wrong content; the reason never quotes it.
        var upper = Write(Encoding.ASCII.GetBytes(good.ToUpperInvariant() + "\n"), "upper", dir);
        Assert.Equal($"{upper} is not a key file", await RefusalAsync(upper));
        var crlf = Write(Encoding.ASCII.GetBytes(good[..^1] + "\r\n"), "crlf", dir);
        Assert.Equal($"{crlf} is not a key file", await RefusalAsync(crlf));
    }

    /// <summary>
    /// Another process holds the file without sharing it (an antivirus scan):
    /// the reader tries again after a short wait on the clock, and gives up
    /// with a reason after the last one.
    /// </summary>
    [Fact]
    public async Task RetriesWhileTheFileIsHeld()
    {
        Assert.SkipUnless(OperatingSystem.IsWindows(), "sharing violations are Windows'");
        using var dir = new TemporaryDirectory();
        var key = RpcAuth.NewNonce();
        var path = Write(KeyFile(key), "rpc.sock.key", dir);
        var time = new FakeTimeProvider();

        var held = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.None);
        var read = DaemonKey.ReadAsync(path, PortableKeyFilePolicy.Instance, time, Ct);
        Assert.False(read.IsCompleted); // the first attempt failed and waits
        held.Dispose();
        time.Advance(DaemonKey.SharingRetryDelays[0]);
        Assert.Equal(key, await read);

        using var kept = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.None);
        var refused = DaemonKey.ReadAsync(path, PortableKeyFilePolicy.Instance, time, Ct);
        foreach (var delay in DaemonKey.SharingRetryDelays)
        {
            Assert.False(refused.IsCompleted);
            time.Advance(delay);
        }
        var e = await Assert.ThrowsAsync<KeyUnavailableException>(() => refused);
        Assert.Equal($"cannot open {path}: in use by another process", e.Reason);
    }

    /// <summary>
    /// The daemon removes its key file when it quits, maybe while a client
    /// reads it: the reader shares delete, or the removal would fail and the
    /// key would stay behind (measured with a reader that did not).
    /// </summary>
    [Fact]
    public void TheReaderLetsTheDaemonRemoveTheFile()
    {
        using var dir = new TemporaryDirectory();
        var path = Write(KeyFile(RpcAuth.NewNonce()), "rpc.sock.key", dir);
        using (PortableKeyFilePolicy.Instance.Open(path))
        {
            File.Delete(path);
        }
        Assert.False(File.Exists(path));
    }

    private static byte[] KeyFile(byte[] key) => Encoding.ASCII.GetBytes(RpcAuth.Hex(key) + "\n");

    private static string Write(byte[] content, string name, TemporaryDirectory dir)
    {
        var path = Path.Combine(dir.Path, name);
        File.WriteAllBytes(path, content);
        return path;
    }

    private static Task<byte[]> ReadAsync(string path) => DaemonKey.ReadAsync(path, PortableKeyFilePolicy.Instance, TimeProvider.System, Ct);

    /// <summary>Why DaemonKey refused the file; null (and a failed assertion) when it read it.</summary>
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
}
