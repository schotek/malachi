// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/EditorBridge.swift (BridgeMessage);
// GTK: ui/internal/editor/bridge.go (bridgeMessage, decodeMessage).
//
// Decoded as encoding/json and Swift's decoding read it: members matched by
// their exact names, a missing or null member is its zero value, an unknown
// one is ignored, a mistyped one fails the whole message, and so does
// anything that is not a JSON object. A repeated member counts once, the
// last time (encoding/json, Foundation). The page is hostile: nothing of a
// message is ever put in an exception.

using System;
using System.Text.Json;

namespace Malachi.Core.Html;

/// <summary>
/// editor.bridgeMessage: what the page posts: <c>ready</c>, <c>changed</c>
/// (with <see cref="Seq"/>, <see cref="Html"/>, <see cref="Text"/>),
/// <c>state</c> (the formatting, flattened into the same object as Go embeds
/// it), and on Windows <c>key</c> (with <see cref="Key"/>) and <c>drop</c>
/// (the files travel beside the message). A kind this client does not know
/// decodes, and the channel ignores it.
/// </summary>
public sealed record BridgeMessage
{
    /// <summary>The kind; "" when the page sent none.</summary>
    public string Type { get; init => field = value ?? ""; } = "";

    /// <summary>A <c>changed</c>'s sequence number, per document, from 1.</summary>
    public long Seq { get; init; }

    /// <summary>A <c>changed</c>'s <c>body.innerHTML</c>.</summary>
    public string Html { get; init => field = value ?? ""; } = "";

    /// <summary>A <c>changed</c>'s <c>body.innerText</c>.</summary>
    public string Text { get; init => field = value ?? ""; } = "";

    /// <summary>A <c>key</c>'s key: <c>escape</c> or <c>link</c>.</summary>
    public string Key { get; init => field = value ?? ""; } = "";

    /// <summary>A <c>state</c>'s formatting.</summary>
    public EditorState State { get; init => field = value ?? new(); } = new();

    /// <summary>
    /// editor.decodeMessage: one posted JSON string.
    /// </summary>
    /// <exception cref="FormatException">It is not a message of this shape.</exception>
    public static BridgeMessage Decode(string raw)
    {
        ArgumentNullException.ThrowIfNull(raw);
        JsonDocument document;
        try
        {
            document = JsonDocument.Parse(raw);
        }
        catch (JsonException)
        {
            throw new FormatException("bridge message: not JSON");
        }
        using (document)
        {
            var root = document.RootElement;
            if (root.ValueKind != JsonValueKind.Object)
            {
                throw new FormatException("bridge message: not an object");
            }
            string type = "", html = "", text = "", key = "", block = "", align = "";
            long seq = 0;
            bool bold = false, italic = false, underline = false, strike = false, ul = false, ol = false, link = false;
            foreach (var member in root.EnumerateObject())
            {
                var v = member.Value;
                switch (member.Name)
                {
                    case "type":
                        type = ReadString(v, "type");
                        break;
                    case "seq":
                        seq = ReadInteger(v);
                        break;
                    case "html":
                        html = ReadString(v, "html");
                        break;
                    case "text":
                        text = ReadString(v, "text");
                        break;
                    case "key":
                        key = ReadString(v, "key");
                        break;
                    case "bold":
                        bold = ReadBool(v, "bold");
                        break;
                    case "italic":
                        italic = ReadBool(v, "italic");
                        break;
                    case "underline":
                        underline = ReadBool(v, "underline");
                        break;
                    case "strike":
                        strike = ReadBool(v, "strike");
                        break;
                    case "ul":
                        ul = ReadBool(v, "ul");
                        break;
                    case "ol":
                        ol = ReadBool(v, "ol");
                        break;
                    case "link":
                        link = ReadBool(v, "link");
                        break;
                    case "block":
                        block = ReadString(v, "block");
                        break;
                    case "align":
                        align = ReadString(v, "align");
                        break;
                    default:
                        break;
                }
            }
            return new BridgeMessage
            {
                Type = type,
                Seq = seq,
                Html = html,
                Text = text,
                Key = key,
                State = new EditorState
                {
                    Bold = bold,
                    Italic = italic,
                    Underline = underline,
                    Strike = strike,
                    Ul = ul,
                    Ol = ol,
                    Link = link,
                    Block = block,
                    Align = align,
                },
            };
        }
    }

    /// <summary><see cref="Decode"/> without the exception: null for anything that is not a message.</summary>
    public static BridgeMessage? TryDecode(string? raw)
    {
        if (raw is null)
        {
            return null;
        }
        try
        {
            return Decode(raw);
        }
        catch (FormatException)
        {
            return null;
        }
    }

    private static string ReadString(JsonElement v, string name) => v.ValueKind switch
    {
        JsonValueKind.String => v.GetString() ?? "",
        JsonValueKind.Null => "",
        _ => throw new FormatException("bridge message: " + name + " is not a string"),
    };

    private static bool ReadBool(JsonElement v, string name) => v.ValueKind switch
    {
        JsonValueKind.True => true,
        JsonValueKind.False or JsonValueKind.Null => false,
        _ => throw new FormatException("bridge message: " + name + " is not a boolean"),
    };

    private static long ReadInteger(JsonElement v) => v.ValueKind switch
    {
        JsonValueKind.Number when v.TryGetInt64(out var n) => n,
        JsonValueKind.Null => 0,
        _ => throw new FormatException("bridge message: seq is not an integer"),
    };

    /// <summary>The kinds of messages the bridge posts.</summary>
    public static class Kinds
    {
        /// <summary>Once per document, after the bridge installed its listeners.</summary>
        public const string Ready = "ready";

        /// <summary>250 ms after the last edit, and at once on <c>flush()</c>.</summary>
        public const string Changed = "changed";

        /// <summary>On every edit and selection change, after a formatting key and every exec.</summary>
        public const string State = "state";

        /// <summary>Escape or Ctrl+K in the page (Windows).</summary>
        public const string Key = "key";

        /// <summary>Files dropped on the page, in the message's additional objects (Windows).</summary>
        public const string Drop = "drop";
    }
}
