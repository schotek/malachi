// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: RpcClient with the Windows key-file policy against the real
// malachid.exe, as the app will use them (docs/windows-port.md §5, §15
// phase C gate "the handshake against the real daemon"): the handshake, a
// call, a notification the daemon sends, and a crashed daemon replaced by a
// new one with a new key. The counterpart of the manual checks of
// macos-port.md and of the research transcript (research 01 §7).

using System;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Transport;
using Malachi.Platform.Windows.Transport;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Transport;

public sealed class RealDaemonTests
{
    private static CancellationToken Ct => TestContext.Current.CancellationToken;

    [Fact]
    public async Task HandshakeCallAndNotification()
    {
        using var daemon = RealDaemon.CreateOrSkip();
        await daemon.StartAsync(Ct);
        using var client = new RpcClient(daemon.Socket, keyFilePolicy: new WindowsKeyFilePolicy());
        await client.ConnectAsync(Ct);
        Assert.Equal(new RpcClientState.Connected(), client.State);

        var info = await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct);
        Assert.Equal(API.ProtocolVersion, info.ProtocolVersion);
        Assert.Equal(daemon.Pid, info.Pid);
        Assert.Equal(Path.GetFullPath(daemon.Store), Path.GetFullPath(info.StorePath));

        // An account on a loopback port nobody serves, without a password:
        // the daemon stores it and tells every client (notify.accountsChanged
        // may come before the answer).
        var added = await client.CallAsync(API.AccountAdd, new AccountAddParams
        {
            Config = new AccountConfig
            {
                Name = "Test",
                Email = "test@example.org",
                Imap = new ServerConfig { Host = "127.0.0.1", Port = 1, Security = Security.None, Username = "test", AuthMethod = AuthMethod.Password },
                Smtp = new ServerConfig { Host = "127.0.0.1", Port = 1, Security = Security.None, Username = "test", AuthMethod = AuthMethod.Password },
            },
        }, Ct);
        Assert.False(string.IsNullOrEmpty(added.AccountId.Value));
        using var wait = CancellationTokenSource.CreateLinkedTokenSource(Ct);
        wait.CancelAfter(TimeSpan.FromSeconds(10));
        while (true)
        {
            var n = await client.Notifications.ReadAsync(wait.Token);
            if (n.Method == API.Notify.AccountsChanged)
            {
                Assert.IsType<DaemonNotification.AccountsChanged>(n.Decode());
                break;
            }
        }

        // The daemon's errors arrive as RpcException with the documented code.
        var e = await Assert.ThrowsAsync<RpcException>(() =>
            client.CallAsync(API.FolderList, new FolderListParams { AccountId = "nope" }, Ct));
        Assert.Equal(ErrorCode.AccountNotFound, e.Code.Value);
    }

    /// <summary>
    /// A killed daemon leaves its socket and key file; the probe finds
    /// nobody, and the next daemon replaces both: the client reads the new
    /// key and connects.
    /// </summary>
    [Fact]
    public async Task ACrashedDaemonIsReplaced()
    {
        using var daemon = RealDaemon.CreateOrSkip();
        await daemon.StartAsync(Ct);
        using var client = new RpcClient(daemon.Socket, keyFilePolicy: new WindowsKeyFilePolicy());
        await client.ConnectAsync(Ct);
        var before = await File.ReadAllBytesAsync(RpcAuth.KeyPath(daemon.Socket), Ct);

        daemon.Kill();
        using (var wait = CancellationTokenSource.CreateLinkedTokenSource(Ct))
        {
            wait.CancelAfter(TimeSpan.FromSeconds(10));
            await foreach (var s in client.States.ReadAllAsync(wait.Token))
            {
                if (s is RpcClientState.Disconnected { Reason: not null })
                {
                    break;
                }
            }
        }
        Assert.True(File.Exists(daemon.Socket), "a killed daemon leaves its socket");
        Assert.False(UnixSocketProbe.Answers(daemon.Socket));
        var refused = await Assert.ThrowsAsync<RpcClientException>(() => client.ConnectAsync(Ct));
        Assert.Equal(ClientError.Transport($"nothing listens on {daemon.Socket}"), refused.Error);

        await daemon.StartAsync(Ct);
        Assert.NotEqual(before, await File.ReadAllBytesAsync(RpcAuth.KeyPath(daemon.Socket), Ct));
        await client.ConnectAsync(Ct);
        Assert.Equal(daemon.Pid, (await client.CallAsync(API.SystemInfo, new EmptyParams(), Ct)).Pid);
    }
}
