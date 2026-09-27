// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JSONRPCTests.swift, and of the
// classification rules of ui/internal/client/client.go (readLoop) that the
// Envelope keeps.

using System;
using System.Linq;
using System.Text;
using System.Text.Json;
using Malachi.Core.Api;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Transport;

public sealed class JsonRpcTests
{
    [Fact]
    public void RequestEncodesAsTheContractExpects()
    {
        var data = JsonRpc.EncodeRequest(7, API.SystemInfoName, new EmptyParams(), ApiJsonContext.Wire.EmptyParams);
        // One frame: the terminating newline and no other.
        Assert.Equal((byte)'\n', data[^1]);
        Assert.DoesNotContain((byte)'\n', data[..^1]);
        using var doc = JsonDocument.Parse(data);
        var obj = doc.RootElement;
        Assert.Equal("2.0", obj.GetProperty("jsonrpc").GetString());
        Assert.Equal(7, obj.GetProperty("id").GetInt32());
        Assert.Equal("system.info", obj.GetProperty("method").GetString());
        Assert.Equal(JsonValueKind.Object, obj.GetProperty("params").ValueKind);
        Assert.Empty(obj.GetProperty("params").EnumerateObject());
        // The members in Go's order, nothing else (TestHandshakeFreshNonce).
        Assert.Equal(["jsonrpc", "id", "method", "params"], obj.EnumerateObject().Select(p => p.Name));
    }

    [Fact]
    public void RequestKeepsHtmlAndNonAsciiRaw()
    {
        var data = JsonRpc.EncodeRequest(1, "test.echo", new AccountParams("<b>ř</b>\n"), TransportTestJson.Default.AccountParams);
        var text = Encoding.UTF8.GetString(data);
        Assert.Contains("<b>ř</b>\\n", text, StringComparison.Ordinal);
    }

    [Fact]
    public void EnvelopeClassifiesLines()
    {
        Assert.True(Envelope.TryParse("""{"jsonrpc":"2.0","id":3,"result":{"x":1}}"""u8, out var response));
        Assert.True(response.IsResponse && !response.IsNotification && response.Error is null);
        Assert.Equal(3, response.Id);

        Assert.True(Envelope.TryParse("""{"jsonrpc":"2.0","id":4,"error":{"code":1102,"message":"gone"}}"""u8, out var failure));
        Assert.Equal(new RpcError { Code = 1102, Message = "gone" }, failure.Error);
        Assert.Equal(ErrorCode.MessageNotFound, failure.Error!.Code.Value);

        Assert.True(Envelope.TryParse("""{"jsonrpc":"2.0","method":"notify.accountsChanged","params":{}}"""u8, out var notification));
        Assert.True(notification.IsNotification && !notification.IsResponse);
        Assert.Equal("notify.accountsChanged", notification.Method);
    }

    // The classification's corners: Go's rules (client.go readLoop), Swift's
    // for a line it cannot read at all.
    [Fact]
    public void EnvelopeCorners()
    {
        // A null id with a method is a notification; an empty method is none.
        Assert.True(Envelope.TryParse("""{"jsonrpc":"2.0","id":null,"method":"notify.syncState","params":{}}"""u8, out var nulled));
        Assert.True(nulled.IsNotification);
        Assert.True(Envelope.TryParse("""{"jsonrpc":"2.0","method":"","params":{}}"""u8, out var empty));
        Assert.False(empty.IsNotification || empty.IsResponse);
        // A string id answers nothing the client sent, and is no notification.
        Assert.True(Envelope.TryParse("""{"jsonrpc":"2.0","id":"str-7","result":{}}"""u8, out var stringId));
        Assert.False(stringId.IsResponse || stringId.IsNotification);
        // The daemon's parse-error answer has a null id.
        Assert.True(Envelope.TryParse("""{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}"""u8, out var parse));
        Assert.False(parse.IsResponse || parse.IsNotification);
        // Lines that are not read at all.
        foreach (var bad in new[] { "", "hello", "null", "[1]", """{"id":1}x""", """{"id":1,"error":"x"}""", """{"id":1,"error":{"message":"no code"}}""", """{"method":5}""" })
        {
            Assert.False(Envelope.TryParse(Encoding.UTF8.GetBytes(bad), out _), bad);
        }
        // The last of repeated members wins, as in Go.
        Assert.True(Envelope.TryParse("""{"id":1,"id":2,"result":{}}"""u8, out var repeated));
        Assert.Equal(2, repeated.Id);
    }

    [Fact]
    public void NotificationParamsDecodeOnDemand()
    {
        var n = new RpcNotification("notify.syncState", """{"jsonrpc":"2.0","method":"notify.syncState","params":{"accountId":"a1"}}"""u8.ToArray());
        Assert.Equal(new AccountParams("a1"), n.Params(TransportTestJson.Default.AccountParams));
        Assert.Equal("notify.syncState", n.ToString());
    }

    [Fact]
    public void SystemInfoDecodes()
    {
        var raw = """{"result":{"version":"0.1.0","protocolVersion":1,"pid":42,"storePath":"/x/store.db"}}"""u8;
        var info = JsonRpc.ReadResult(raw, ApiJsonContext.Wire.SystemInfoResult);
        Assert.Equal(new SystemInfoResult { Version = "0.1.0", ProtocolVersion = 1, Pid = 42, StorePath = "/x/store.db" }, info);
        // No result, or a null one, is a decoding error.
        Assert.Throws<JsonException>(() => JsonRpc.ReadResult("""{"id":1}"""u8, ApiJsonContext.Wire.SystemInfoResult));
        Assert.Throws<JsonException>(() => JsonRpc.ReadResult("""{"id":1,"result":null}"""u8, ApiJsonContext.Wire.SystemInfoResult));
    }

    [Fact]
    public void ErrorDataIsKeptAsJson()
    {
        var err = JsonCoding.Decode<RpcError>(
            """{"code":1502,"message":"too big","data":{"limit":1024,"size":2048,"tags":["a",true,null,1.5],"nested":{"k":"v"}}}""");
        Assert.Equal(ErrorCode.AttachmentTooBig, err.Code.Value);
        var data = err.Data!.Value;
        Assert.Equal(1024, data.GetProperty("limit").GetInt32());
        Assert.Equal(2048, data.GetProperty("size").GetInt32());
        Assert.Equal(new SizeLimit(1024, 2048), err.AttachmentTooBig);
        Assert.Equal("""["a",true,null,1.5]""", data.GetProperty("tags").GetRawText());
        Assert.Equal("v", data.GetProperty("nested").GetProperty("k").GetString());
        Assert.False(data.TryGetProperty("missing", out _));
        Assert.Equal("too big (1502)", err.ToString());
        Assert.Equal("too big (1502)", new RpcException(err).Message);

        // Round trip, and a null data stays absent (FakeDaemon encodes errors this way).
        Assert.Equal(err, JsonCoding.Decode<RpcError>(JsonCoding.Encode(err)));
        using var plain = JsonDocument.Parse(JsonCoding.Encode(new RpcError { Code = -32601, Message = "nope" }));
        Assert.Equal(-32601, plain.RootElement.GetProperty("code").GetInt32());
        Assert.False(plain.RootElement.TryGetProperty("data", out _));
    }
}
