// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/EditorBridge.swift (BridgeMessage);
// GTK: ui/internal/editor/bridge.go (bridgeMessage, decodeMessage).
//
// The "rewrite" message is GTK's (its selected and text); macOS has none,
// its bridge returns the passage instead (RewriteTarget).
//
// Decoded as encoding/json and Swift's decoding read it: a missing or null
// member is its zero value, an unknown one is ignored, a mistyped one fails
// the whole message, and so does anything that is not a JSON object. A
// repeated member counts once, the last time (encoding/json, Foundation).
// Members are matched by their exact names, as Swift's decoder matches
// them; encoding/json would also take "Type" or "HTML", which the bridge
// never sends, so the exact match is a deliberate tightening. Strings are
// read as encoding/json reads them: an escaped surrogate that does not pair
// with the escape after it is U+FFFD, and so is a lone surrogate in the
// posted string itself (JSON.stringify escapes one, but a message need not
// come from it). Chromium keeps an unpaired surrogate that a plain-text
// paste brought into the body, and JSON.stringify posts it escaped, so a
// changed carrying one is a real draft: decoding it must neither throw in
// the view's event handler nor drop it, which would leave the flush it
// answers waiting. The page is hostile: nothing of a message is ever put in
// an exception, and ToString shows only the kind and sizes (§3.1: no mail
// content in logs).

using System;
using System.Globalization;
using System.Text;
using System.Text.Json;

namespace Malachi.Core.Html;

/// <summary>
/// editor.bridgeMessage: what the page posts: <c>ready</c>, <c>changed</c>
/// (with <see cref="Seq"/>, <see cref="Html"/>, <see cref="Text"/>),
/// <c>state</c> (the formatting, flattened into the same object as Go embeds
/// it), <c>rewrite</c> (with <see cref="Selected"/> and <see cref="Text"/>),
/// and on Windows <c>key</c> (with <see cref="Key"/>) and <c>drop</c> (the
/// files travel beside the message). A kind this client does not know
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

    /// <summary>A <c>changed</c>'s <c>body.innerText</c>; a <c>rewrite</c>'s passage.</summary>
    public string Text { get; init => field = value ?? ""; } = "";

    /// <summary>A <c>rewrite</c>'s: the passage is the selection.</summary>
    public bool Selected { get; init; }

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
            // Through UTF-8 with replacement: a lone surrogate in the string
            // is U+FFFD, as Go reads invalid UTF-8, where parsing the string
            // itself would throw an ArgumentException.
            document = JsonDocument.Parse(Encoding.UTF8.GetBytes(raw));
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
            bool selected = false, bold = false, italic = false, underline = false, strike = false, ul = false, ol = false, link = false;
            foreach (var member in root.EnumerateObject())
            {
                var v = member.Value;
                switch (NameOf(member))
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
                    case "selected":
                        selected = ReadBool(v, "selected");
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
                Selected = selected,
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

    /// <summary>
    /// The kind (when it is one of <see cref="Kinds"/>), the <c>seq</c> and
    /// the sizes of the content: never the draft, whose text this is, nor
    /// any other string of the page's.
    /// </summary>
    public override string ToString()
    {
        var kind = Type switch
        {
            Kinds.Ready or Kinds.Changed or Kinds.State or Kinds.Rewrite or Kinds.Key or Kinds.Drop => Type,
            "" => "\"\"",
            _ => "<other>",
        };
        return string.Create(
            CultureInfo.InvariantCulture,
            $"BridgeMessage(type: {kind}, seq: {Seq}, html: {Html.Length} chars, text: {Text.Length} chars, key: {Key.Length} chars)");
    }

    // A member's name; "" for a name with an unpaired escaped surrogate,
    // which is none of ours (Go reads it with U+FFFD and ignores it).
    private static string NameOf(JsonProperty member)
    {
        try
        {
            return member.Name;
        }
        catch (InvalidOperationException)
        {
            return "";
        }
    }

    private static string ReadString(JsonElement v, string name) => v.ValueKind switch
    {
        JsonValueKind.String => StringOf(v),
        JsonValueKind.Null => "",
        _ => throw new FormatException("bridge message: " + name + " is not a string"),
    };

    // The value of a string token. GetString refuses an escaped surrogate
    // that does not pair; encoding/json reads it as U+FFFD, and so does this.
    private static string StringOf(JsonElement v)
    {
        try
        {
            return v.GetString() ?? "";
        }
        catch (InvalidOperationException)
        {
            return Unquote(v.GetRawText());
        }
    }

    // encoding/json unquote over a string token the parser has accepted
    // (quotes included, every escape well-formed): the escapes decoded, an
    // escaped high surrogate kept only together with an escaped low one
    // right after it, every other escaped surrogate U+FFFD.
    private static string Unquote(string token)
    {
        ArgumentNullException.ThrowIfNull(token);
        var output = new StringBuilder(token.Length);
        var end = token.Length - 1;
        for (var i = 1; i < end; i++)
        {
            var c = token[i];
            if (c != '\\')
            {
                output.Append(c);
                continue;
            }
            i++;
            switch (token[i])
            {
                case 'b':
                    output.Append('\b');
                    break;
                case 'f':
                    output.Append('\f');
                    break;
                case 'n':
                    output.Append('\n');
                    break;
                case 'r':
                    output.Append('\r');
                    break;
                case 't':
                    output.Append('\t');
                    break;
                case 'u':
                    var r = Hex4(token, i + 1);
                    i += 4;
                    if (!char.IsSurrogate(r))
                    {
                        output.Append(r);
                    }
                    else if (char.IsHighSurrogate(r) && i + 6 < end && token[i + 1] == '\\' && token[i + 2] == 'u'
                        && Hex4(token, i + 3) is var low && char.IsLowSurrogate(low))
                    {
                        output.Append(r).Append(low);
                        i += 6;
                    }
                    else
                    {
                        output.Append('�');
                    }
                    break;
                default:
                    // '"', '\\' and '/' stand for themselves.
                    output.Append(token[i]);
                    break;
            }
        }
        return output.ToString();
    }

    // The four hex digits at start, which the parser has checked.
    private static char Hex4(string s, int start) =>
        (char)int.Parse(s.AsSpan(start, 4), NumberStyles.AllowHexSpecifier, CultureInfo.InvariantCulture);

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

        /// <summary>The passage of the assistant's rewrite, once per <c>rewriteTarget</c>.</summary>
        public const string Rewrite = "rewrite";

        /// <summary>Escape or Ctrl+K in the page (Windows).</summary>
        public const string Key = "key";

        /// <summary>Files dropped on the page, in the message's additional objects (Windows).</summary>
        public const string Drop = "drop";
    }
}
