// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the canned stream-json of macos/Tests/MalachiCoreTests/
// AssistantPanelControllerTests.swift (fakeInit, fakeInitFailed, fakeDelta,
// fakeText, fakeToolUse, fakeToolResult, fakeResult, answerTurn) and of
// AssistantRequestTests.swift (structuredResult, errorResult); GTK:
// ui/internal/assistantpanel harness_test.go and oneshot_test.go. A turn of
// Swift's FakeTurn is the stand-in's steps here: its lines first, then
// what Swift's shell snippet does. Texts are put into the JSON as they
// are, so a caller escapes what JSON needs escaped, as in Swift. Also the
// environment the tests hand the stand-in, which Windows needs to start a
// program at all.

using System;
using System.Collections.Generic;
using Malachi.FakeClaude;

namespace Malachi.Core.Tests.Assistants;

/// <summary>The stream-json lines and turns of the stand-in claude.</summary>
internal static class CannedStreamJson
{
    /// <summary>system/init with the bridge connected (fakeInit).</summary>
    public const string Init = """{"type":"system","subtype":"init","mcp_servers":[{"name":"malachi","status":"connected"}],"tools":["mcp__malachi__read_message"],"model":"claude-sonnet"}""";

    /// <summary>system/init with the bridge failed (fakeInitFailed).</summary>
    public const string InitFailed = """{"type":"system","subtype":"init","mcp_servers":[{"name":"malachi","status":"failed"}],"tools":[]}""";

    /// <summary>A result that failed without a text (errorResult).</summary>
    public const string ErrorResult = """{"type":"result","subtype":"error_during_execution","is_error":true,"result":"","total_cost_usd":0}""";

    /// <summary>A text delta (fakeDelta).</summary>
    public static string Delta(string t) =>
        "{\"type\":\"stream_event\",\"event\":{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"" + t + "\"}}}";

    /// <summary>A whole text block of an assistant message (fakeText).</summary>
    public static string Text(string t) =>
        "{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"" + t + "\"}]}}";

    /// <summary>A tool call of the bridge (fakeToolUse).</summary>
    public static string ToolUse(string id, string tool) =>
        "{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"tool_use\",\"id\":\"" + id
        + "\",\"name\":\"mcp__malachi__" + tool + "\",\"input\":{}}]}}";

    /// <summary>A tool's answer (fakeToolResult).</summary>
    public static string ToolResult(string id, string text, bool error = false) =>
        "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"" + id
        + "\",\"is_error\":" + (error ? "true" : "false") + ",\"content\":[{\"type\":\"text\",\"text\":\"" + text + "\"}]}]}}";

    /// <summary>The result of a turn (fakeResult).</summary>
    public static string Result(string text = "done", bool success = true) =>
        "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":" + (success ? "false" : "true") + ",\"result\":\"" + text
        + "\",\"total_cost_usd\":0.01}";

    /// <summary>A result with a <c>structured_output</c> (raw JSON) and a text (structuredResult).</summary>
    public static string StructuredResult(string structured, string text = "") =>
        "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"" + text + "\",\"structured_output\":" + structured
        + ",\"total_cost_usd\":0.001}";

    /// <summary>A turn of <paramref name="lines"/> alone (Swift's FakeTurn without shell).</summary>
    public static IReadOnlyList<FakeClaudeStep> Turn(params string[] lines) => [FakeClaudeStep.Lines(lines)];

    /// <summary>
    /// A plain answer: init, two deltas, the whole text, the result
    /// (answerTurn; <paramref name="text"/> is cut in half by UTF-16 units,
    /// Swift's by characters, the same for the ASCII texts of the tests).
    /// </summary>
    public static IReadOnlyList<FakeClaudeStep> AnswerTurn(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var half = text.Length / 2;
        return Turn(Init, Delta(text[..half]), Delta(text[half..]), Text(text), Result());
    }

    /// <summary>
    /// A parent environment of the stand-in with <paramref name="entries"/>:
    /// SystemRoot as this machine's (a Windows program starts poorly
    /// without it; Swift's children get by with HOME and PATH) and the
    /// given variables.
    /// </summary>
    public static Dictionary<string, string> Environment(params (string Name, string Value)[] entries)
    {
        var env = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        if (System.Environment.GetEnvironmentVariable("SystemRoot") is { Length: > 0 } root)
        {
            env["SystemRoot"] = root;
        }
        foreach (var (name, value) in entries)
        {
            env[name] = value;
        }
        return env;
    }
}
