// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Result<Data, RPCError> of
// macos/Tests/MalachiCoreTests/Fixtures/FakeDaemon.swift (FakeDaemon.Handler).

using Malachi.Core.Api;

namespace Malachi.Core.Tests.Fixtures;

/// <summary>What a <see cref="FakeDaemon.Handler"/> answers: result JSON, or an error.</summary>
internal readonly record struct FakeAnswer(string? ResultJson, RpcError? Error)
{
    /// <summary>A result, as JSON.</summary>
    public static FakeAnswer Result(string json) => new(json, null);

    /// <summary>An error answer.</summary>
    public static FakeAnswer Failure(RpcError error) => new(null, error);
}
