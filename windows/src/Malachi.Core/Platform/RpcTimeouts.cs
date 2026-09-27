// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/RPCTimeouts.swift; GTK:
// ui/internal/window/actions.go (rpcTimeout), window.go (fetchSystemInfo),
// attachments.go (partTimeout), remote.go (remoteTimeout), compose_open.go
// (composeTimeout), ui/internal/accountwizard/wizard.go (discoverTimeout,
// testTimeout, addTimeout, oauthStartTimeout, oauthWaitCallTimeout),
// backend/pkg/api/auth.go (HandshakeTimeout).

using System;

namespace Malachi.Core.Platform;

/// <summary>
/// How long the client waits for each kind of call, as the GTK UI does
/// (ui/internal/window, ui/internal/compose, ui/internal/accountwizard).
/// Every <c>API</c> method carries one of these as its default; a caller
/// may pass another explicitly.
/// </summary>
public static class RpcTimeouts
{
    /// <summary>Everything not named below.</summary>
    public static readonly TimeSpan Default = TimeSpan.FromSeconds(5);

    /// <summary><c>system.info</c>: the health check must answer at once.</summary>
    public static readonly TimeSpan SystemInfo = TimeSpan.FromSeconds(3);

    /// <summary>
    /// The connection handshake, <c>system.hello</c> and
    /// <c>system.authenticate</c> together (api.HandshakeTimeout); the daemon
    /// allows 10 s from accept.
    /// </summary>
    public static readonly TimeSpan Handshake = TimeSpan.FromSeconds(5);

    /// <summary><c>message.part</c>, <c>attachment.get</c>: payloads up to 16 MiB.</summary>
    public static readonly TimeSpan Part = TimeSpan.FromSeconds(60);

    /// <summary>
    /// <c>message.body</c> (any call the policy may resolve to <c>allow</c>),
    /// <c>message.embedded</c>: the daemon may fetch images for up to 10 s.
    /// </summary>
    public static readonly TimeSpan Remote = TimeSpan.FromSeconds(30);

    /// <summary><c>draft.create</c>: quoting copies parts into the attachment store.</summary>
    public static readonly TimeSpan Compose = TimeSpan.FromSeconds(30);

    /// <summary><c>account.discover</c>: ISPDB, autoconfig, DNS and guesses, 20 s inside.</summary>
    public static readonly TimeSpan Discover = TimeSpan.FromSeconds(15);

    /// <summary><c>account.test</c>: 10 s to connect and 20 s per endpoint inside.</summary>
    public static readonly TimeSpan Test = TimeSpan.FromSeconds(45);

    /// <summary><c>account.add</c>, <c>account.update</c>: the keyring may prompt.</summary>
    public static readonly TimeSpan Save = TimeSpan.FromSeconds(30);

    /// <summary><c>account.oauthStart</c>: the daemon opens a listener and builds the URL.</summary>
    public static readonly TimeSpan OAuthStart = TimeSpan.FromSeconds(10);

    /// <summary>
    /// One <c>account.oauthWait</c> call: the daemon blocks up to 60 s before
    /// it answers <c>pending</c>.
    /// </summary>
    public static readonly TimeSpan OAuthWaitCall = TimeSpan.FromSeconds(75);
}
