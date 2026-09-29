// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantEvents.swift
// (parseEvents, parseInit, parseAssistant, parseUser, resultText,
// parseResult, parseDraftResult, validID); GTK:
// ui/internal/assistant/events.go (ParseEvents, ParseDraftResult). The In
// App target: what Claude Code writes on stdout with `--output-format
// stream-json` (one JSON object per line) as the few events the panel
// shows, and the draft a `create_draft` result names. The lines carry mail
// content and model output: they are parsed defensively, a field of an
// unexpected type reads as its zero value, unknown types are ignored and
// nothing is interpreted beyond the fields named below.
//
// The shapes (Claude Code 2.1.178): system/init (tools, mcp_servers
// [{name, status}], model, session_id, …), system/status and others,
// rate_limit_event, stream_event (event content_block_delta with delta
// text_delta {text}; thinking_delta and signature_delta ignored),
// assistant (message.content: thinking, text {text}, tool_use {id, name,
// input}), user (message.content: tool_result {tool_use_id, is_error,
// content: a string or [{type: text, text}]}) and result (subtype success
// or error_*, is_error, result, structured_output, permission_denials
// [{tool_name}], total_cost_usd, usage).
//
// The JSON is read the way Go's encoding/json reads it into
// map[string]json.RawMessage (GoJson), not with System.Text.Json. Go's
// ParseDraftResult has a DraftRef and a bool; here a null DraftRef is the
// false. This file holds no translatable text.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// assistant.ParseEvents: one stdout line (without its newline): one
    /// event per text or tool_use block of an <c>assistant</c> message and
    /// per tool_result block of a <c>user</c> message, in order (thinking and
    /// other blocks yield nothing, so such a message may yield none), one
    /// <see cref="AssistantEventKind.SystemInit"/> for system/init, one
    /// <see cref="AssistantEventKind.TextDelta"/> for a text delta, one
    /// <see cref="AssistantEventKind.Result"/> for a result, and one
    /// <see cref="AssistantEventKind.Other"/> for any other line.
    /// </summary>
    /// <exception cref="AssistantException">
    /// <see cref="AssistantError.Malformed"/> when the line (without
    /// surrounding white space) is not JSON, <see cref="AssistantError.NotObject"/>
    /// when it is not a JSON object.
    /// </exception>
    public static IReadOnlyList<AssistantEvent> ParseEvents(ReadOnlySpan<byte> line)
    {
        var b = line.ToArray();
        var (lo, hi) = TrimSpace(b, 0, b.Length);
        if (!GoJson.Valid(b, lo, hi))
        {
            throw new AssistantException(AssistantError.Malformed, "assistant: a stream-json line that is not JSON");
        }
        var o = b[lo] == (byte)'{' ? GoJson.Object.Of(b, (lo, hi)) : null;
        if (o is null)
        {
            throw new AssistantException(AssistantError.NotObject, "assistant: a stream-json line that is not an object");
        }
        switch (o.Str("type"))
        {
            case "system":
                if (o.Str("subtype") == "init")
                {
                    return [ParseInit(o)];
                }
                break;
            case "stream_event":
                var ev = o.Obj("event");
                var delta = ev?.Obj("delta");
                if (ev?.Str("type") == "content_block_delta" && delta?.Str("type") == "text_delta")
                {
                    return [new AssistantEvent(AssistantEventKind.TextDelta) { Text = delta.Str("text") }];
                }
                break;
            case "assistant":
                return ParseAssistant(o);
            case "user":
                return ParseUser(o);
            case "result":
                return [ParseResult(o)];
            default:
                break;
        }
        return [new AssistantEvent(AssistantEventKind.Other)];
    }

    private static AssistantEvent ParseInit(GoJson.Object o)
    {
        var tools = new List<string>();
        foreach (var t in o.Array("tools"))
        {
            if (GoJson.String(o.B, t) is { } s)
            {
                tools.Add(s);
            }
        }
        var connected = false;
        foreach (var s in o.Array("mcp_servers"))
        {
            var srv = GoJson.Object.Of(o.B, s);
            if (srv?.Str("name") == BridgeServer && srv.Str("status") == "connected")
            {
                connected = true;
            }
        }
        return new AssistantEvent(AssistantEventKind.SystemInit) { BridgeConnected = connected, Tools = [.. tools] };
    }

    private static List<AssistantEvent> ParseAssistant(GoJson.Object o)
    {
        var output = new List<AssistantEvent>();
        foreach (var raw in o.Obj("message")?.Array("content") ?? [])
        {
            var block = GoJson.Object.Of(o.B, raw);
            switch (block?.Str("type") ?? "")
            {
                case "text":
                    output.Add(new AssistantEvent(AssistantEventKind.Text) { Text = block!.Str("text") });
                    break;
                case "tool_use":
                    output.Add(new AssistantEvent(AssistantEventKind.ToolUse)
                    {
                        Tool = StripBridgePrefix(block!.Str("name")),
                        ToolUseId = block.Str("id"),
                    });
                    break;
                default:
                    continue; // thinking, redacted thinking, anything newer
            }
        }
        return output;
    }

    private static List<AssistantEvent> ParseUser(GoJson.Object o)
    {
        var output = new List<AssistantEvent>();
        foreach (var raw in o.Obj("message")?.Array("content") ?? [])
        {
            var block = GoJson.Object.Of(o.B, raw);
            if (block is null || block.Str("type") != "tool_result")
            {
                continue;
            }
            output.Add(new AssistantEvent(AssistantEventKind.ToolResult)
            {
                ToolUseId = block.Str("tool_use_id"),
                IsError = block.Boolean("is_error"),
                ResultText = ResultText(block, block.Member("content")),
            });
        }
        return output;
    }

    /// <summary>
    /// A tool_result's content: a string, or the text blocks of an array
    /// joined with "\n" (images and other blocks left out; a text block
    /// whose text is not a string counts as "").
    /// </summary>
    private static string ResultText(GoJson.Object o, (int Lo, int Hi)? content)
    {
        if (GoJson.String(o.B, content) is { } s)
        {
            return s;
        }
        var texts = new List<string>();
        foreach (var raw in GoJson.Array(o.B, content))
        {
            var block = GoJson.Object.Of(o.B, raw);
            if (block?.Str("type") == "text")
            {
                texts.Add(block.Str("text"));
            }
        }
        return string.Join('\n', texts);
    }

    private static AssistantEvent ParseResult(GoJson.Object o)
    {
        var subtype = o.Str("subtype");
        var isError = o.Boolean("is_error");
        var resultText = o.Str("result");
        var success = subtype == "success" && !isError;
        if (!success && resultText.Length == 0)
        {
            resultText = subtype;
        }
        var denied = new List<string>();
        foreach (var raw in o.Array("permission_denials"))
        {
            var name = GoJson.Object.Of(o.B, raw)?.Str("tool_name") ?? "";
            if (name.Length > 0)
            {
                denied.Add(StripBridgePrefix(name));
            }
        }
        byte[]? structured = null;
        if (o.Member("structured_output") is { } member)
        {
            var (lo, hi) = TrimSpace(o.B, member.Lo, member.Hi);
            if (hi > lo && !o.B.AsSpan(lo, hi - lo).SequenceEqual("null"u8))
            {
                structured = o.B[lo..hi];
            }
        }
        return new AssistantEvent(AssistantEventKind.Result)
        {
            IsError = isError,
            ResultText = resultText,
            CostUsd = o.Number("total_cost_usd"),
            Success = success,
            Denied = [.. denied],
            Structured = structured,
        };
    }

    // Drafts

    // The parts of the head of a create_draft result,
    // backend/cmd/malachi-mcp/tools_write.go:
    // "draft <id> (version <n>) stored in account <acc>; it is NOT sent."
    private static ReadOnlySpan<byte> DraftHead => "draft "u8;

    private static ReadOnlySpan<byte> DraftVersion => " (version "u8;

    private static ReadOnlySpan<byte> DraftAccount => ") stored in account "u8;

    private static ReadOnlySpan<byte> DraftTail => "; it is NOT sent."u8;

    /// <summary>
    /// assistant.ParseDraftResult: the draft a create_draft tool result
    /// names (the panel asks only for the results of its create_draft
    /// calls, by tool use id); null (Go's false) otherwise. Only the result's
    /// first line counts (up to the first "\n"), and only when it starts
    /// exactly with the head the bridge itself writes, "draft &lt;id&gt;
    /// (version &lt;n&gt;) stored in account &lt;acc&gt;; it is NOT sent.",
    /// followed by the end of the line or a space (the bridge goes on with a
    /// sentence about sending). That line is the bridge's own text, outside
    /// the fence in which it quotes mail, so mail cannot forge it; the ids
    /// are non-empty and hold no white space or control characters, the
    /// version is 1 to 9 digits. The application still looks the draft up
    /// with draft.list before it offers to open it.
    /// </summary>
    public static DraftRef? ParseDraftResult(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var b = Utf8(text);
        var lineEnd = b.AsSpan().IndexOf((byte)0x0A);
        if (lineEnd < 0)
        {
            lineEnd = b.Length;
        }
        if (lineEnd < DraftHead.Length || !b.AsSpan(0, DraftHead.Length).SequenceEqual(DraftHead))
        {
            return null;
        }
        var idStart = DraftHead.Length;
        var idEnd = IndexOf(b, DraftVersion, idStart, lineEnd);
        if (idEnd < 0)
        {
            return null;
        }
        var digitsStart = idEnd + DraftVersion.Length;
        var digitsEnd = IndexOf(b, DraftAccount, digitsStart, lineEnd);
        if (digitsEnd < 0)
        {
            return null;
        }
        var accStart = digitsEnd + DraftAccount.Length;
        var accEnd = IndexOf(b, DraftTail, accStart, lineEnd);
        if (accEnd < 0)
        {
            return null;
        }
        var restStart = accEnd + DraftTail.Length;
        var digits = b.AsSpan(digitsStart, digitsEnd - digitsStart);
        if (!ValidId(b, idStart, idEnd) || !ValidId(b, accStart, accEnd)
            || digits.Length is < 1 or > 9 || digits.ContainsAnyExceptInRange((byte)'0', (byte)'9')
            || !(restStart == lineEnd || b[restStart] == 0x20))
        {
            return null;
        }
        var version = 0;
        foreach (var d in digits)
        {
            version = (version * 10) + (d - '0');
        }
        return new DraftRef(FromUtf8(b.AsSpan(accStart, accEnd - accStart)), FromUtf8(b.AsSpan(idStart, idEnd - idStart)), version);
    }

    /// <summary>
    /// An id of the bridge's head: non-empty, no white space or control
    /// characters (Go's unicode.IsSpace and unicode.IsControl).
    /// </summary>
    private static bool ValidId(ReadOnlySpan<byte> b, int lo, int hi)
    {
        if (hi <= lo)
        {
            return false;
        }
        var i = lo;
        while (i < hi)
        {
            var (r, w) = DecodeRune(b, i, hi);
            if (IsSpace(r) || IsControl(r))
            {
                return false;
            }
            i += w;
        }
        return true;
    }
}
