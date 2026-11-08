// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantSuggestReply.swift
// (suggestReplyDraftTool, suggestReplyTimeout, suggestReplyMaxInstruction,
// cleanSuggestReplyInstruction); GTK: ui/internal/assistant/suggest_reply.go.
//
// The board's Suggest Reply (docs/mcp.md "A suggested reply on the board",
// docs/security.md §10.2): the pure half of the one-shot request the
// board's reply controller sends the user's assistant for one case, on the
// user's click. It is the panel's command line (Args) with the bridge
// started as the daemon's prompt of purpose reply says (docs/api.md §4.18:
// bridgeArgs --in-app --reply-only <replyMessageId>, tools read_message,
// list_messages and a create_draft that accepts only a reply to exactly
// that message). The system prompt and the message of ReplyInput are the
// daemon's; here stay the draft tool's name, the timeout and the cleaning
// of the user's instruction (read as Go's runes are read: bad UTF-8 and
// lone surrogates are U+FFFD). This file holds no translatable text.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// The tool whose result names the draft (<see cref="ParseDraftResult"/>),
    /// without the bridge's prefix, as <see cref="AssistantEvent.Tool"/> names it.
    /// </summary>
    public const string SuggestReplyDraftTool = "create_draft";

    /// <summary>The longest instruction the message carries, in characters (Unicode scalars).</summary>
    public const int SuggestReplyMaxInstruction = 500;

    /// <summary>How long a suggested reply may take before it ends as a timeout.</summary>
    public static readonly TimeSpan SuggestReplyTimeout = TimeSpan.FromSeconds(120);

    /// <summary>
    /// The instruction field's text as it goes into the message: every run
    /// of white space one space, control and bidirectional formatting
    /// characters left out, trimmed, cut to <see cref="SuggestReplyMaxInstruction"/>
    /// characters.
    /// </summary>
    public static string CleanSuggestReplyInstruction(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var b = Utf8(text);
        var output = new List<byte>(Math.Min(b.Length, SuggestReplyMaxInstruction * 4));
        var count = 0;
        var space = false;
        var i = 0;
        while (i < b.Length)
        {
            var (r, w) = DecodeRune(b, i, b.Length);
            i += w;
            if (IsSpace(r))
            {
                space = output.Count > 0;
                continue;
            }
            if (IsControl(r) || IsBidiControl(r))
            {
                continue;
            }
            if (space)
            {
                if (count >= SuggestReplyMaxInstruction - 1)
                {
                    break;
                }
                output.Add(0x20);
                count++;
                space = false;
            }
            if (count >= SuggestReplyMaxInstruction)
            {
                break;
            }
            AppendUtf8(r, output);
            count++;
        }
        return FromUtf8(output.ToArray());
    }
}
