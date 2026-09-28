// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The helpers of macos/Tests/MalachiCoreTests/CertTrustTests.swift (tlsJSON,
// rawJSON, tlsError), which RPCErrorTextTests uses as well: TLS details as
// the client holds them after the RPC, JSON.

using System.Text.Json;
using Malachi.Core.Api;

namespace Malachi.Core.Tests.Wizard;

internal static class TlsErrors
{
    /// <summary><paramref name="d"/> as generic JSON (Swift <c>tlsJSON</c>).</summary>
    public static JsonElement Json(TlsErrorData d) => JsonSerializer.SerializeToElement(d, ApiJsonContext.Wire.TlsErrorData);

    /// <summary>A literal JSON text (Swift <c>rawJSON</c>).</summary>
    public static JsonElement Raw(string json)
    {
        using var document = JsonDocument.Parse(json);
        return document.RootElement.Clone();
    }

    /// <summary>A tlsError carrying <paramref name="data"/>.</summary>
    public static RpcError Error(JsonElement? data) =>
        new() { Code = ErrorCode.TlsError, Message = "x509: certificate signed by unknown authority", Data = data };

    /// <summary>A tlsError carrying these details.</summary>
    public static RpcError Error(TlsErrorData d) => Error(Json(d));
}
