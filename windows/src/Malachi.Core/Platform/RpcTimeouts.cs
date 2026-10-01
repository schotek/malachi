// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/RPCTimeouts.swift; GTK:
// ui/internal/window/actions.go (rpcTimeout), window.go (fetchSystemInfo),
// attachments.go (partTimeout), remote.go (remoteTimeout), compose_open.go
// (composeTimeout), download.go (downloadTimeout),
// ui/internal/accountwizard/wizard.go (discoverTimeout,
// testTimeout, addTimeout, oauthStartTimeout, oauthWaitCallTimeout),
// accountwizard/jira_flow.go (detectSiteTimeout, listSpacesTimeout),
// window/issue_actions.go (issueTimeout), window/bulk.go (unsubscribeTimeout),
// backend/pkg/api/auth.go
// (HandshakeTimeout).

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

    /// <summary>
    /// <c>message.download</c>: the daemon's budget is 4 minutes, and the
    /// download goes on when the caller gives up (docs/api.md: wait at least
    /// 5 minutes; download.go <c>downloadTimeout</c>).
    /// </summary>
    public static readonly TimeSpan Download = TimeSpan.FromMinutes(5);

    /// <summary><c>account.detectSite</c>: two anonymous requests to the site, 15 s inside.</summary>
    public static readonly TimeSpan DetectSite = TimeSpan.FromSeconds(15);

    /// <summary>
    /// <c>account.listSpaces</c>: signs in, lists spaces and statuses and may
    /// count every space's issues.
    /// </summary>
    public static readonly TimeSpan ListSpaces = TimeSpan.FromSeconds(45);

    /// <summary>
    /// <c>issue.transitions</c>: one request to the site, which may be slow;
    /// one value for both calls, as the GTK UI (issue_actions.go
    /// <c>issueTimeout</c>) and macOS have it (docs/api.md §4.12 says 20 s).
    /// </summary>
    public static readonly TimeSpan Transitions = TimeSpan.FromSeconds(45);

    /// <summary><c>issue.transition</c>: the transition, then the issue's refresh (up to 30 s).</summary>
    public static readonly TimeSpan Transition = TimeSpan.FromSeconds(45);

    /// <summary>
    /// <c>message.unsubscribe</c>: the daemon may verify the message and talk
    /// to the sender's server (15 s) first (bulk.go <c>unsubscribeTimeout</c>).
    /// </summary>
    public static readonly TimeSpan Unsubscribe = TimeSpan.FromSeconds(30);
}
