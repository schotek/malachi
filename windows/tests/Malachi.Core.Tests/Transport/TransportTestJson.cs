// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The small Decodable structs the transport tests of
// macos/Tests/MalachiCoreTests (RPCClientTests, JSONRPCTests) declare
// inline, with their own source-generated JSON context, and the RPC
// methods the tests call that the contract does not have.

using System.Text.Json.Serialization;
using Malachi.Core.Api;
using Malachi.Core.Platform;

namespace Malachi.Core.Tests.Transport;

/// <summary>A result naming which handler answered (Swift <c>Which</c>).</summary>
internal sealed record Which([property: JsonPropertyName("which")] string Value);

/// <summary>Params with one number (Swift <c>X</c>).</summary>
internal sealed record XParams([property: JsonPropertyName("x")] int X);

/// <summary>Params naming an account (Swift <c>P</c> of JSONRPCTests).</summary>
internal sealed record AccountParams([property: JsonPropertyName("accountId")] string AccountId);

/// <summary>An object result of any members, as Swift's empty <c>Decodable</c>.</summary>
internal sealed record AnyObject;

/// <summary>The method an echoing fake daemon answers with.</summary>
internal sealed record EchoedMethod([property: JsonPropertyName("method")] string Method);

[JsonSourceGenerationOptions(DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull)]
[JsonSerializable(typeof(Which))]
[JsonSerializable(typeof(XParams))]
[JsonSerializable(typeof(AccountParams))]
[JsonSerializable(typeof(AnyObject))]
[JsonSerializable(typeof(EchoedMethod))]
internal sealed partial class TransportTestJson : JsonSerializerContext;

/// <summary>The test methods of RPCClientTests as descriptors.</summary>
internal static class TestMethods
{
    public static readonly RpcMethod<EmptyParams, Which> Slow = Method("test.slow");
    public static readonly RpcMethod<EmptyParams, Which> Fast = Method("test.fast");
    public static readonly RpcMethod<EmptyParams, Which> Never = Method("test.never");
    public static readonly RpcMethod<EmptyParams, Which> Nope = Method("nope");
    public static readonly RpcMethod<EmptyParams, Which> Fail = Method("test.fail");

    public static readonly RpcMethod<XParams, Which> Echo =
        new("test.echo", RpcTimeouts.Default, TransportTestJson.Default.XParams, TransportTestJson.Default.Which);

    /// <summary>system.info answered by an echoing daemon with its method's name.</summary>
    public static readonly RpcMethod<EmptyParams, EchoedMethod> EchoedSystemInfo =
        new(API.SystemInfoName, RpcTimeouts.SystemInfo, ApiJsonContext.Wire.EmptyParams, TransportTestJson.Default.EchoedMethod);

    private static RpcMethod<EmptyParams, Which> Method(string name) =>
        new(name, RpcTimeouts.Default, ApiJsonContext.Wire.EmptyParams, TransportTestJson.Default.Which);
}
