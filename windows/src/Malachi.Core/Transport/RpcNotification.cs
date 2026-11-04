// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/JSONRPC.swift
// (RPCNotification); GTK: ui/internal/client/client.go (OnNotification's
// method and params).

using System;
using System.Text.Json.Serialization.Metadata;
using Malachi.Core.Api;

namespace Malachi.Core.Transport;

/// <summary>
/// A notification as the daemon sent it (docs/api.md §5): its method and
/// the whole line, from which the params are read on demand, so the
/// transport needs to know no payload type. <see cref="Decode"/> turns it
/// into a <see cref="DaemonNotification"/>.
/// </summary>
public sealed class RpcNotification
{
    /// <summary>A notification of <paramref name="method"/> read from <paramref name="line"/>, which it keeps.</summary>
    public RpcNotification(string method, ReadOnlyMemory<byte> line)
    {
        ArgumentNullException.ThrowIfNull(method);
        Method = method;
        Line = line;
    }

    /// <summary>The method, e.g. <c>notify.syncState</c>.</summary>
    public string Method { get; }

    /// <summary>The whole line as it came, without its "\n".</summary>
    public ReadOnlyMemory<byte> Line { get; }

    /// <summary>
    /// The params as <typeparamref name="T"/> (Swift <c>params(_:)</c>);
    /// throws a <see cref="System.Text.Json.JsonException"/> for a line
    /// without params or with params of another shape.
    /// </summary>
    public T Params<T>(JsonTypeInfo<T> typeInfo) => JsonRpc.ReadParams(Line.Span, typeInfo);

    /// <summary>The params as a type of the contract (<see cref="ApiJsonContext"/>).</summary>
    public T Params<T>() => Params(JsonCoding.TypeInfo<T>());

    /// <summary>
    /// The notification decoded by its method; an unknown method is
    /// <see cref="DaemonNotification.Unknown"/>. Throws a
    /// <see cref="System.Text.Json.JsonException"/> when a known method
    /// carries params of the wrong shape.
    /// </summary>
    public DaemonNotification Decode() => DaemonNotification.Decode(Method, Line.Span);

    /// <summary>The method; the params may carry mail data and are never printed.</summary>
    public override string ToString() => Method;
}
