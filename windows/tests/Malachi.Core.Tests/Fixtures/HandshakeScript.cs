// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/Fixtures/FakeDaemon.swift
// (FakeDaemon.Script). Swift's hello closure gets the right result only;
// here it gets the connection's nonces and key as well, which the cases of
// backend/pkg/api/handshake_test.go that Swift left out need (a reflected
// proof, swapped nonces).

using System;

namespace Malachi.Core.Tests.Fixtures;

/// <summary>
/// What a scripted <see cref="FakeDaemon"/> writes
/// (<see cref="HandshakeMode.Raw"/>), as exact text: each line with its
/// "\n", or a line cut short.
/// </summary>
internal sealed record HandshakeScript
{
    /// <summary>
    /// Written for <c>system.hello</c>, given the right result and what it is
    /// made of (<see cref="HelloContext"/>); null writes nothing.
    /// </summary>
    public required Func<HelloContext, string?> Hello { get; init; }

    /// <summary>Closes the connection's write side after the <c>system.hello</c> answer.</summary>
    public bool CloseAfterHello { get; init; }

    /// <summary>
    /// Written for a <c>system.authenticate</c> with the right proof instead
    /// of its answer, given the client's nonce as hex; null writes nothing.
    /// </summary>
    public Func<string, string?> Authenticate { get; init; } = _ => null;

    /// <summary>Closes the connection's write side after that.</summary>
    public bool CloseAfterAuthenticate { get; init; }
}

/// <summary>What a script's <c>system.hello</c> answer is made of.</summary>
/// <param name="RightResult">The JSON of the result the daemon would write (<see cref="FakeDaemon.HelloResult"/>).</param>
/// <param name="ClientNonce">The client's nonce.</param>
/// <param name="DaemonNonce">The daemon's nonce of the connection.</param>
/// <param name="Key">The key of the key file.</param>
internal sealed record HelloContext(string RightResult, byte[] ClientNonce, byte[] DaemonNonce, byte[] Key);
