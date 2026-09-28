// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: what a HandshakeScript's system.hello answer is made of
// (macos/Tests/MalachiCoreTests/Fixtures/FakeDaemon.swift hands its script
// the right result alone). The cases of backend/pkg/api/handshake_test.go
// that forge a proof need the nonces and the key as well.

namespace Malachi.Core.Tests.Fixtures;

/// <summary>What a script's <c>system.hello</c> answer is made of.</summary>
/// <param name="RightResult">The JSON of the result the daemon would write (<see cref="FakeDaemon.HelloResult"/>).</param>
/// <param name="ClientNonce">The client's nonce.</param>
/// <param name="DaemonNonce">The daemon's nonce of the connection.</param>
/// <param name="Key">The key of the key file.</param>
internal sealed record HelloContext(string RightResult, byte[] ClientNonce, byte[] DaemonNonce, byte[] Key);
