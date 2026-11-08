// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantRewrite.swift
// (rewrites, cleanRewrite, stripFences, fenceTag, stripMarkers, quotePairs,
// stripQuotes, the byte helpers, rewriteLabel, composeTexts); GTK:
// ui/internal/assistant/rewrite.go and its texts in assistant.go
// (RewriteLabel, ComposeTexts). The compose window's rewrite (the In App
// target only): a passage of the message being written, the selection or
// the user's own text above the quoted original, goes to the user's
// assistant with a preset or the user's own instruction, and the answer,
// cleaned (CleanRewrite), replaces the passage or goes below it as plain
// text. It is a one-shot request (AssistantRequest, ComposeRewriteController)
// of purpose rewrite: the daemon builds the system prompt and the message of
// RewriteInput (docs/api.md §4.18), no bridge, no tool, the answer in the
// result event. Here are only the presets, the cleaning of the answer and
// the texts.
//
// Byte for byte like the Go package (Go's strings.TrimSpace, HasPrefix and
// Contains on UTF-8, not .NET's string comparisons); the texts at the end
// go through L10n. An unknown rewrite is an enum value outside the five,
// named by RewriteNick.

using System;
using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.I18n;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>assistant.Rewrites: the presets in the order of the popover; <see cref="AssistantRewrite.Custom"/> is its free field.</summary>
    public static IReadOnlyList<AssistantRewrite> Rewrites { get; } =
        [AssistantRewrite.Politer, AssistantRewrite.Shorter, AssistantRewrite.Fix, AssistantRewrite.ToEnglish];

    /// <summary>The markers around the passage in the daemon's message of a rewrite.</summary>
    internal static ReadOnlySpan<byte> PassageOpen => "<<<"u8;

    /// <inheritdoc cref="PassageOpen"/>
    internal static ReadOnlySpan<byte> PassageClose => ">>>"u8;

    /// <summary>Markdown's code fence.</summary>
    internal static ReadOnlySpan<byte> Fence => "```"u8;

    /// <summary>
    /// quotePairs: the quotation marks <see cref="StripQuotes"/> takes off,
    /// opening and closing: straight, English, Czech and German, Swedish,
    /// the guillemets both ways, and the single ones.
    /// </summary>
    internal static IReadOnlyList<(byte[] Open, byte[] Close)> QuotePairs { get; } =
    [
        QuotePair(0x22, 0x22), // " "
        QuotePair(0x201C, 0x201D), // “ ”
        QuotePair(0x201E, 0x201C), // „ “
        QuotePair(0x201E, 0x201D), // „ ”
        QuotePair(0x201D, 0x201D), // ” ”
        QuotePair(0xAB, 0xBB), // « »
        QuotePair(0xBB, 0xAB), // » «
        QuotePair(0x27, 0x27), // ' '
        QuotePair(0x2018, 0x2019), // ‘ ’
        QuotePair(0x201A, 0x2018), // ‚ ‘
        QuotePair(0x201A, 0x2019), // ‚ ’
    ];

    /// <summary>
    /// The nick of a rewrite as Go spells it ("politer", "shorter", "fix",
    /// "english", "custom"); an unknown value is its number, as its
    /// ToString() spells it.
    /// </summary>
    public static string RewriteNick(AssistantRewrite r) => r switch
    {
        AssistantRewrite.Politer => "politer",
        AssistantRewrite.Shorter => "shorter",
        AssistantRewrite.Fix => "fix",
        AssistantRewrite.ToEnglish => "english",
        AssistantRewrite.Custom => "custom",
        _ => ((int)r).ToString(CultureInfo.InvariantCulture),
    };

    /// <summary>
    /// The rewrite as the daemon's <see cref="Api.RewriteInput.Preset"/>
    /// takes it (<see cref="RewriteNick"/>).
    /// </summary>
    public static Api.RewritePreset RewritePresetOf(AssistantRewrite r) => new(RewriteNick(r));

    /// <summary>
    /// assistant.CleanRewrite: the model's answer as the text that goes into
    /// the message: CRLF as LF, the control characters other than "\n" and
    /// "\t" left out (a lone CR too), a lone surrogate as U+FFFD, trimmed;
    /// then, in this order and each at most once, a pair of ``` fences
    /// around the whole answer (with an optional language tag on the opening
    /// line), the "&lt;&lt;&lt;" and "&gt;&gt;&gt;" markers of the daemon's
    /// message at its start and end, a pair of quotation
    /// marks around the whole answer (straight or typographic, and only when
    /// neither mark occurs inside), and once more the markers; the rest
    /// trimmed after each step. "" when nothing is left.
    /// </summary>
    public static string CleanRewrite(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        // CRLF as LF and every other CR left out is every CR left out.
        var src = Utf8(text);
        var b = new List<byte>(src.Length);
        var i = 0;
        while (i < src.Length)
        {
            var (r, w) = DecodeRune(src, i, src.Length);
            i += w;
            if (IsControl(r) && r != 0x0A && r != 0x09)
            {
                continue;
            }
            AppendUtf8(r, b);
        }
        var s = TrimmedBytes([.. b]);
        s = StripFences(s);
        s = StripMarkers(s);
        s = StripQuotes(s);
        s = StripMarkers(s);
        return FromUtf8(s);
    }

    /// <summary>
    /// stripFences: one pair of ``` fences that wrap all of <paramref name="s"/>
    /// (trimmed) removed: the text between them, without a first line that
    /// is only a language tag (<see cref="FenceTag"/>), trimmed.
    /// <paramref name="s"/> stays when it does not start and end with a
    /// fence or holds another fence inside.
    /// </summary>
    internal static byte[] StripFences(byte[] s)
    {
        if (s.Length < 2 * Fence.Length || !HasPrefix(s, Fence) || !HasSuffix(s, Fence))
        {
            return s;
        }
        var inner = s[Fence.Length..(s.Length - Fence.Length)];
        if (IndexOf(inner, Fence, 0, inner.Length) >= 0)
        {
            return s;
        }
        var nl = inner.AsSpan().IndexOf((byte)0x0A);
        if (nl >= 0 && FenceTag(inner.AsSpan(0, nl)))
        {
            inner = inner[(nl + 1)..];
        }
        return TrimmedBytes(inner);
    }

    /// <summary>
    /// fenceTag: whether the rest of a fence's opening line is a language
    /// tag: ASCII letters, digits and _ + - . # only (none is fine),
    /// trailing spaces and tabs allowed.
    /// </summary>
    internal static bool FenceTag(ReadOnlySpan<byte> s)
    {
        var end = s.Length;
        while (end > 0 && s[end - 1] is 0x20 or 0x09)
        {
            end--;
        }
        foreach (var c in s[..end])
        {
            if (c is not ((>= (byte)'a' and <= (byte)'z') or (>= (byte)'A' and <= (byte)'Z') or (>= (byte)'0' and <= (byte)'9')
                or (byte)'_' or (byte)'+' or (byte)'-' or (byte)'.' or (byte)'#'))
            {
                return false;
            }
        }
        return true;
    }

    /// <summary>stripMarkers: the "&lt;&lt;&lt;" at the start of <paramref name="s"/> and the "&gt;&gt;&gt;" at its end removed (each when there), what remains trimmed.</summary>
    internal static byte[] StripMarkers(byte[] s)
    {
        if (HasPrefix(s, PassageOpen))
        {
            s = TrimmedBytes(s[PassageOpen.Length..]);
        }
        if (HasSuffix(s, PassageClose))
        {
            s = TrimmedBytes(s[..(s.Length - PassageClose.Length)]);
        }
        return s;
    }

    /// <summary>
    /// stripQuotes: the first pair of <see cref="QuotePairs"/> that wraps all
    /// of <paramref name="s"/> removed when neither of its marks occurs
    /// between them, the rest trimmed.
    /// </summary>
    internal static byte[] StripQuotes(byte[] s)
    {
        foreach (var (open, close) in QuotePairs)
        {
            if (s.Length < open.Length + close.Length || !HasPrefix(s, open) || !HasSuffix(s, close))
            {
                continue;
            }
            var inner = s[open.Length..(s.Length - close.Length)];
            if (IndexOf(inner, open, 0, inner.Length) >= 0 || IndexOf(inner, close, 0, inner.Length) >= 0)
            {
                continue;
            }
            return TrimmedBytes(inner);
        }
        return s;
    }

    // Byte helpers

    /// <summary>strings.HasPrefix on bytes.</summary>
    internal static bool HasPrefix(ReadOnlySpan<byte> s, ReadOnlySpan<byte> p) => s.StartsWith(p);

    /// <summary>strings.HasSuffix on bytes.</summary>
    internal static bool HasSuffix(ReadOnlySpan<byte> s, ReadOnlySpan<byte> p) => s.EndsWith(p);

    /// <summary>strings.TrimSpace of bytes.</summary>
    internal static byte[] TrimmedBytes(byte[] b)
    {
        var (lo, hi) = TrimSpace(b, 0, b.Length);
        return b[lo..hi];
    }

    /// <summary>strings.TrimSpace of a string.</summary>
    internal static string TrimmedString(string s) => FromUtf8(TrimmedBytes(Utf8(s)));

    private static (byte[] Open, byte[] Close) QuotePair(int open, int close)
    {
        var o = new List<byte>(3);
        AppendUtf8(open, o);
        var c = new List<byte>(3);
        AppendUtf8(close, c);
        return ([.. o], [.. c]);
    }

    // Texts

    /// <summary>
    /// assistant.RewriteLabel: the button of a preset rewrite in the compose
    /// window's popover; "" for <see cref="AssistantRewrite.Custom"/> (its
    /// field has <see cref="ComposeStrings.Custom"/>) and an unknown one.
    /// </summary>
    public static string RewriteLabel(AssistantRewrite r)
    {
        switch (r)
        {
            case AssistantRewrite.Politer:
                // TRANSLATORS: A button of the assistant in the compose window: rewrites the text more politely.
                return L10n.T("More Polite");
            case AssistantRewrite.Shorter:
                // TRANSLATORS: A button of the assistant in the compose window: rewrites the text shorter.
                return L10n.T("Shorter");
            case AssistantRewrite.Fix:
                // TRANSLATORS: A button of the assistant in the compose window: fixes spelling, grammar and punctuation.
                return L10n.T("Fix Mistakes");
            case AssistantRewrite.ToEnglish:
                // TRANSLATORS: A button of the assistant in the compose window.
                return L10n.T("Translate to English");
            default:
                return "";
        }
    }

    /// <summary>assistant.ComposeTexts: the fixed texts of the compose window's rewrite, translated.</summary>
    public static ComposeStrings ComposeTexts() => new()
    {
        // TRANSLATORS: The title of the assistant's popover in the compose window when text is selected.
        RewriteSelection = L10n.T("Rewrite Selection"),
        // TRANSLATORS: The title of the assistant's popover in the compose window: the text the user wrote above the quoted message.
        RewriteText = L10n.T("Rewrite Your Text"),
        // TRANSLATORS: Placeholder of a field in the assistant's popover of the compose window: what to do with the text.
        Custom = L10n.T("Your own instruction…"),
        Rewriting = L10n.T("Rewriting…"),
        // TRANSLATORS: A button: the rewritten text replaces the original.
        Replace = L10n.T("Replace"),
        // TRANSLATORS: A button: the rewritten text goes below the original, which stays.
        InsertBelow = L10n.T("Insert Below"),
        Discard = L10n.T("_Discard"),
    };
}
