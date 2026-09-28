// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The helpers of macos/Tests/MalachiCoreTests/APICodingTests.swift (decode,
// encodeObject) and Fixtures/FakeDaemon.swift (json): the contract's coding,
// and a structural comparison of JSON where Swift compares values whose
// arrays C# records compare by reference.

using System;
using System.Linq;
using System.Text.Json;
using Malachi.Core.Api;
using Xunit;

namespace Malachi.Core.Tests.Api;

/// <summary>JSON through the contract's coding, for the API tests.</summary>
internal static class ApiJson
{
    /// <summary>Decodes with the wire options (Swift <c>decode(_:_:)</c>).</summary>
    public static T Decode<T>(string json) => JsonCoding.Decode<T>(json);

    /// <summary>Encodes with the wire options and returns the JSON object (Swift <c>encodeObject</c>).</summary>
    public static JsonElement EncodeObject<T>(T value)
    {
        var element = JsonSerializer.SerializeToElement(value, JsonCoding.TypeInfo<T>());
        Assert.Equal(JsonValueKind.Object, element.ValueKind);
        return element;
    }

    /// <summary>The member names of an object, sorted (Swift <c>keys.sorted()</c>).</summary>
    public static string[] Keys(JsonElement obj) =>
        [.. obj.EnumerateObject().Select(p => p.Name).Order(StringComparer.Ordinal)];

    /// <summary>A member of an object, or null when it is absent.</summary>
    public static JsonElement? Member(JsonElement obj, string name) =>
        obj.TryGetProperty(name, out var value) ? value : null;

    /// <summary>A JSON value, owning its memory.</summary>
    public static JsonElement Parse(string json)
    {
        using var document = JsonDocument.Parse(json);
        return document.RootElement.Clone();
    }

    /// <summary>Whether two JSON texts are the same value, whatever the order of members and the spacing.</summary>
    public static void AssertSameJson(string expected, string actual)
    {
        var e = Parse(expected);
        var a = Parse(actual);
        Assert.True(JsonElement.DeepEquals(e, a), $"expected {e.GetRawText()}\n  actual {a.GetRawText()}");
    }

    /// <summary>
    /// Whether two values encode to the same JSON: Swift's == for structs whose
    /// arrays a C# record compares by reference.
    /// </summary>
    public static void AssertSameValue<T>(T expected, T actual) =>
        AssertSameJson(JsonCoding.EncodeToString(expected), JsonCoding.EncodeToString(actual));
}
