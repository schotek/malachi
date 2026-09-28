// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/Fixtures/FakeDaemon.swift
// (FakeDaemon.HandshakeMode).

namespace Malachi.Core.Tests.Fixtures;

/// <summary>
/// How the <see cref="FakeDaemon"/>'s side of the handshake behaves. A
/// connection keeps the mode it was accepted in, whatever
/// <see cref="FakeDaemon.SetHandshake"/> says later.
/// </summary>
internal abstract record HandshakeMode
{
    // Only the cases below derive from it.
    private HandshakeMode()
    {
    }

    /// <summary>As malachid does.</summary>
    public sealed record Normal : HandshakeMode;

    /// <summary>
    /// A daemon of protocol 1: no handshake, methodNotFound for
    /// <c>system.hello</c>, and every connection served (and notified) at once.
    /// </summary>
    public sealed record OldDaemon : HandshakeMode;

    /// <summary>A daemon of another protocol version: its <c>system.hello</c> result carries this one.</summary>
    /// <param name="Version">The version it claims.</param>
    public sealed record ProtocolVersion(int Version) : HandshakeMode;

    /// <summary>Something that does not hold the key file's key: its daemonProof is made with another key.</summary>
    public sealed record WrongProof : HandshakeMode;

    /// <summary>The daemon refuses the client's proof: 1005 for <c>system.authenticate</c>, then it closes the connection.</summary>
    public sealed record RejectClient : HandshakeMode;

    /// <summary><c>system.hello</c> is read and never answered.</summary>
    public sealed record Silent : HandshakeMode;

    /// <summary>The daemonProof is 63 hex digits, not 64.</summary>
    public sealed record MalformedProof : HandshakeMode;

    /// <summary>
    /// The connection is closed as soon as it is accepted, before a line is
    /// read (the "closed at once" of backend/pkg/api/handshake_test.go's
    /// droppedScripts, which Swift left out).
    /// </summary>
    public sealed record Hangup : HandshakeMode;

    /// <summary>
    /// The daemon writes what a script says instead of its answers, as the
    /// fakeDaemon of backend/pkg/api/handshake_test.go does.
    /// </summary>
    /// <param name="Script">What it writes.</param>
    public sealed record Raw(HandshakeScript Script) : HandshakeMode;
}
