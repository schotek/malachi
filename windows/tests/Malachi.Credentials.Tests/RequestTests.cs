// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiKeychainTests/RequestTests.swift (RequestTests),
// test for test and case for case; the Windows additions for the reader
// that keeps the value as bytes follow after them.

using System;
using System.Linq;
using System.Text;
using System.Text.Json;
using Xunit;

namespace Malachi.Credentials.Tests;

public sealed class RequestTests
{
    [Fact]
    public void ParsesEveryOperation()
    {
        var get = Parse("get", """{"account":"acc_1","key":"password"}""").Request;
        Assert.Equal(new Request("acc_1", "password"), get);
        var set = Parse("set", """{"account":"acc_1","key":"password","value":"hunter2"}""").Request;
        Assert.Equal("hunter2", Utf8(set?.Value));
        var del = Parse("delete", """{"account":"acc_1","key":"oauth2.refresh_token"}""").Request;
        Assert.Equal("oauth2.refresh_token", del?.Key);
        // A trailing newline and unknown keys are fine: the daemon sends one line.
        Assert.NotNull(Parse("get", "{\"account\":\"acc_1\",\"key\":\"password\",\"extra\":1}\n").Request);
    }

    [Fact]
    public void RejectsBadInput()
    {
        Assert.Equal(HelperExit.BadRequest, Parse("fetch", """{"account":"acc_1","key":"password"}""").Exit);
        Assert.Equal(HelperExit.BadRequest, Parse("get", "not json").Exit);
        Assert.Equal(HelperExit.BadRequest, Parse("get", "").Exit);
        Assert.Equal(HelperExit.BadRequest, Parse("get", """{"key":"password"}""").Exit);
        Assert.Equal(HelperExit.BadRequest, Parse("get", """{"account":"acc_1"}""").Exit);
        // set needs a value
        Assert.Equal(HelperExit.BadRequest, Parse("set", """{"account":"acc_1","key":"password"}""").Exit);
        Assert.Equal(HelperExit.Ok, Parse("get", """{"account":"acc_1","key":"password","value":null}""").Exit);
    }

    [Fact]
    public void RejectsIdentifiersThatCouldReachAnAttribute()
    {
        foreach (var bad in new[] { "", "acc/1", "acc 1", "acc\n1", "ünïcode", "a\0b", new string('a', 129) })
        {
            Assert.False(Request.IsIdentifier(bad), $"{Escaped(bad)} must not pass");
            var json = $$"""{"account":{{Quoted(bad)}},"key":"password"}""";
            Assert.True(Parse("get", json).Exit == HelperExit.BadRequest, $"account {Escaped(bad)}");
            var keyJson = $$"""{"account":"acc_1","key":{{Quoted(bad)}}}""";
            Assert.True(Parse("get", keyJson).Exit == HelperExit.BadRequest, $"key {Escaped(bad)}");
        }
        foreach (var good in new[] { "acc_1", "password", "oauth2.refresh_token", "A-Z.0", new string('a', 128) })
        {
            Assert.True(Request.IsIdentifier(good), $"{good} must pass");
        }
    }

    [Fact]
    public void RejectsOversizedInput()
    {
        var padding = new string(' ', Request.MaxInput);
        var json = """{"account":"acc_1","key":"password"}""" + padding;
        Assert.Equal(HelperExit.BadRequest, Parse("get", json).Exit);
    }

    [Fact]
    public void ItemAccountAndLabelNameTheIdentifiersOnly()
    {
        var r = new Request("acc_1", "password", "hunter2"u8.ToArray());
        Assert.Equal("acc_1/password", r.ItemAccount);
        Assert.Equal("Malachi Mail: acc_1 (password)", r.Label);
        Assert.DoesNotContain("hunter2", r.Label, StringComparison.Ordinal);
        Assert.Equal("io.github.schotek.Malachi", Request.Service);
    }

    [Fact]
    public void ExitCodesFollowTheProtocol()
    {
        Assert.Equal(0, (int)HelperExit.Ok);
        Assert.Equal(1, (int)HelperExit.Failure);
        Assert.Equal(2, (int)HelperExit.NotFound);
        Assert.Equal(3, (int)HelperExit.BadRequest);
    }

    [Fact]
    public void ValueLineIsOneJSONLine()
    {
        Assert.Equal("{\"value\":\"hunter2\"}\n", Encoding.UTF8.GetString(Request.ValueLine("hunter2"u8)));
        const string tricky = "a\"b\\c\nd\u0001e";
        var line = Request.ValueLine(Encoding.UTF8.GetBytes(tricky));
        Assert.Equal((byte)'\n', line[^1]);
        Assert.DoesNotContain((byte)'\n', line[..^1]);
        using var back = JsonDocument.Parse(line);
        Assert.Equal(tricky, back.RootElement.GetProperty("value").GetString());
    }

    // Windows additions.

    [Fact]
    public void OperationsAreTheWordsOfTheProtocol()
    {
        string[] words = ["get", "set", "delete"];
        Assert.Equal(words, Enum.GetValues<Operation>().Select(op => op.RawValue));
        foreach (var op in Enum.GetValues<Operation>())
        {
            Assert.Equal(op, Operation.FromRawValue(op.RawValue));
        }
        Assert.Null(Operation.FromRawValue("GET"));
        Assert.Null(Operation.FromRawValue("fetch"));
        Assert.Null(Operation.FromRawValue(null));
    }

    [Theory]
    [InlineData("""{"account":"acc_1","account":"acc_2","key":"password"}""")]
    [InlineData("""{"account":"acc_1","key":"password","key":"other"}""")]
    [InlineData("""{"account":"acc_1","key":"password","value":"a","value":"b"}""")]
    [InlineData("""{"account":"acc_1","key":"password","value":null,"value":"b"}""")]
    [InlineData("""{"account":"acc_1","key":"password"}{}""")]
    [InlineData("""{"account":"acc_1","key":"password"}x""")]
    [InlineData("\uFEFF{\"account\":\"acc_1\",\"key\":\"password\"}")]
    [InlineData("""{"account":"acc_1","key":"password"} // comment""")]
    [InlineData("""{"account":"acc_1","key":"password",}""")]
    [InlineData("""[{"account":"acc_1","key":"password"}]""")]
    [InlineData("\"acc_1\"")]
    [InlineData("""{"account":1,"key":"password"}""")]
    [InlineData("""{"account":null,"key":"password"}""")]
    [InlineData("""{"account":"acc_1","key":"password","value":1}""")]
    [InlineData("""{"account":"acc_1","key":"password","value":{"a":"b"}}""")]
    [InlineData("""{"account":"acc_1","key":"password","value":"\ud800"}""")]
    [InlineData("""{"account":"acc_1","key":"password","value":"x""")]
    public void RejectsWhatIsNotExactlyOneRequestObject(string json)
    {
        Assert.Equal(HelperExit.BadRequest, Parse("set", json).Exit);
    }

    [Fact]
    public void RejectsBytesThatAreNotUtf8()
    {
        byte[] prefix = [.. """{"account":"acc_1","key":"password","value":"a"""u8];
        byte[] suffix = [.. "\"}"u8];
        byte[] data = [.. prefix, 0xC3, 0x28, .. suffix];
        Assert.False(Request.TryParse("set", data, out _));
        byte[] account = [.. "{\"account\":\"acc"u8, 0xFF, .. "\",\"key\":\"password\"}"u8];
        Assert.False(Request.TryParse("get", account, out _));
    }

    [Fact]
    public void SkipsUnknownMembersOfAnyShape()
    {
        var request = Parse("set", """{"x":{"y":[1,{"z":null}]},"account":"acc_1","value":"v","key":"password","w":true}""").Request;
        Assert.Equal(new Request("acc_1", "password", "v"u8.ToArray()), request);
    }

    [Fact]
    public void KeepsTheValueAsItsUtf8AndZeroesIt()
    {
        var request = Parse("set", """{"account":"acc_1","key":"password","value":"pa\"ss w\u00f6rd\n"}""").Request;
        Assert.NotNull(request?.Value);
        Assert.Equal(Encoding.UTF8.GetBytes("pa\"ss wörd\n"), request.Value);
        request.ZeroValue();
        Assert.All(request.Value, b => Assert.Equal(0, b));
    }

    [Fact]
    public void ValueLineKeepsUtf8AndEscapesEveryControl()
    {
        Assert.Equal(Encoding.UTF8.GetBytes("{\"value\":\"pa\\\"ss wörd\\n\"}\n"), Request.ValueLine(Encoding.UTF8.GetBytes("pa\"ss wörd\n")));
        var controls = new string([.. Enumerable.Range(0, 0x20).Select(c => (char)c), (char)0x7F, (char)0x2028]) + char.ConvertFromUtf32(0x1F600);
        var line = Request.ValueLine(Encoding.UTF8.GetBytes(controls));
        Assert.DoesNotContain(line[..^1], b => b < 0x20);
        using var back = JsonDocument.Parse(line);
        Assert.Equal(controls, back.RootElement.GetProperty("value").GetString());
        Assert.Equal("{\"value\":\"\"}\n", Encoding.UTF8.GetString(Request.ValueLine([])));
    }

    [Fact]
    public void ValueLineRefusesBytesThatAreNotUtf8()
    {
        Assert.Throws<ArgumentException>(() => Request.ValueLine([0xFF]));
        Assert.Throws<ArgumentException>(() => Request.ValueLine([(byte)'a', 0xC3]));
    }

    [Fact]
    public void EqualityComparesTheValueBytes()
    {
        Assert.Equal(new Request("a", "k", [1, 2]), new Request("a", "k", [1, 2]));
        Assert.NotEqual(new Request("a", "k", [1, 2]), new Request("a", "k", [1, 3]));
        Assert.NotEqual(new Request("a", "k", [1, 2]), new Request("a", "k"));
        Assert.NotEqual(new Request("a", "k"), new Request("A", "k"));
    }

    private static (HelperExit Exit, Request? Request) Parse(string op, string json) =>
        Request.TryParse(op, Encoding.UTF8.GetBytes(json), out var request)
            ? (HelperExit.Ok, request)
            : (HelperExit.BadRequest, null);

    private static string Utf8(byte[]? bytes) => bytes is null ? "(null)" : Encoding.UTF8.GetString(bytes);

    // The JSON string literal of s, quotes included.
    private static string Quoted(string s) => "\"" + JsonEncodedText.Encode(s).ToString() + "\"";

    private static string Escaped(string s) => JsonEncodedText.Encode(s).ToString();
}
