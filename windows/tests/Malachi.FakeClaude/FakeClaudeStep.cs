// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the shell of the stand-in claude of macos/Tests/MalachiCoreTests/
// AssistantPanelControllerTests.swift (FakeTurn's lines and shell snippet,
// FakeClaude's onStart) and of ClaudeCodeProcessTests.swift's scripts, as
// steps: what Swift writes as `cat <<EOF`, `echo … >&2`, `sleep`, `exit`,
// `read -r line`, a loop of printf, `trap '' TERM` with an endless loop.
// Windows has no SIGTERM: a process that does not end when its stdin
// closes is what the kill after the grace is for (Hang).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;

namespace Malachi.FakeClaude;

/// <summary>One thing the stand-in claude does.</summary>
public sealed class FakeClaudeStep
{
    private FakeClaudeStep(string kind)
    {
        Kind = kind;
    }

    /// <summary>What the step is.</summary>
    public string Kind { get; }

    /// <summary>The text of stdout, stderr and printFile.</summary>
    public string Text { get; private init; } = "";

    /// <summary>The number of exit, sleep, fill and ifStart.</summary>
    public long Number { get; private init; }

    /// <summary>The steps of ifStart.</summary>
    public IReadOnlyList<FakeClaudeStep> Then { get; private init; } = [];

    /// <summary>Writes <paramref name="text"/> to stdout, flushed.</summary>
    public static FakeClaudeStep Stdout(string text) => new("stdout") { Text = text };

    /// <summary>Writes each line and a newline to stdout, flushed (Swift's <c>cat &lt;&lt;EOF</c> of a turn's lines).</summary>
    public static FakeClaudeStep Lines(params string[] lines) => Stdout(string.Concat(lines.Select(l => l + "\n")));

    /// <summary>Writes <paramref name="text"/> to stderr, flushed.</summary>
    public static FakeClaudeStep Stderr(string text) => new("stderr") { Text = text };

    /// <summary>Exits with <paramref name="status"/>.</summary>
    public static FakeClaudeStep Exit(int status) => new("exit") { Number = status };

    /// <summary>Waits <paramref name="milliseconds"/>.</summary>
    public static FakeClaudeStep Sleep(int milliseconds) => new("sleep") { Number = milliseconds };

    /// <summary>Consumes one line of stdin (recorded as the turns' lines are).</summary>
    public static FakeClaudeStep ReadLine() => new("readLine");

    /// <summary>Writes <paramref name="count"/> bytes 'x' to stderr.</summary>
    public static FakeClaudeStep FillStderr(long count) => new("fill") { Number = count };

    /// <summary>
    /// Never ends by itself, whatever happens to stdin: the counterpart of
    /// Swift's <c>trap '' TERM</c> with an endless loop (only a kill ends it).
    /// </summary>
    public static FakeClaudeStep Hang() => new("hang");

    /// <summary>Writes the content of the file <paramref name="path"/> to stdout (a state the test changes).</summary>
    public static FakeClaudeStep PrintFile(string path) => new("printFile") { Text = path };

    /// <summary>Runs <paramref name="then"/> at the conversation start number <paramref name="start"/> only (Swift's <c>$n</c>).</summary>
    public static FakeClaudeStep IfStart(int start, params FakeClaudeStep[] then) => new("ifStart") { Number = start, Then = then };

    internal static IReadOnlyList<FakeClaudeStep> ReadSteps(JsonElement array) =>
        array.ValueKind == JsonValueKind.Array ? array.EnumerateArray().Select(Read).ToList() : [];

    internal static void WriteSteps(Utf8JsonWriter writer, IEnumerable<FakeClaudeStep> steps)
    {
        writer.WriteStartArray();
        foreach (var step in steps)
        {
            writer.WriteStartObject();
            writer.WriteString("kind", step.Kind);
            writer.WriteString("text", step.Text);
            writer.WriteNumber("number", step.Number);
            writer.WritePropertyName("then");
            WriteSteps(writer, step.Then);
            writer.WriteEndObject();
        }
        writer.WriteEndArray();
    }

    private static FakeClaudeStep Read(JsonElement e) => new(e.GetProperty("kind").GetString() ?? "")
    {
        Text = e.GetProperty("text").GetString() ?? "",
        Number = e.GetProperty("number").GetInt64(),
        Then = ReadSteps(e.GetProperty("then")),
    };
}
