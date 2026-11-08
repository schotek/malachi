// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantSearch.swift
// (maxSearchQueryBytes, parseSearchQuery, searchTexts, searchFailedText);
// GTK: ui/internal/assistant/search.go and its texts in assistant.go
// (SearchTexts, SearchFailedText). The search in the user's own words (the
// In App target only): what the user typed into the search box ("invoices
// from Jana in March") goes to the user's assistant, which answers with a
// query in Malachi Mail's search syntax (backend/internal/search/query.go,
// docs/api.md search.query); the application puts it into the box and
// searches as if it had been typed. It is a one-shot request
// (AssistantRequest, SearchConversion) of purpose search: the daemon builds
// the system prompt, the message of SearchInput and the schema of the
// answer (docs/api.md §4.18), no bridge, no tool, and the answer comes in
// the result event's structured_output, read with ParseSearchQuery. Only
// the typed words go to the model, no mail.
//
// Byte for byte like the Go package; the texts at the end go through L10n.
// Go's (string, bool) of ParseSearchQuery is a string or null.

using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using Malachi.Core.I18n;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// assistant.MaxSearchQueryBytes: the longest query
    /// <see cref="ParseSearchQuery"/> returns, in bytes: the daemon's cap
    /// (api.MaxSearchQueryBytes, docs/api.md search.query).
    /// </summary>
    public const int MaxSearchQueryBytes = 1024;

    /// <summary>
    /// assistant.ParseSearchQuery: the query of the search's answer, the
    /// result event's structured_output (<see cref="AssistantEvent.Structured"/>):
    /// the string member "query" of a JSON object (the last one when it is
    /// there twice) as one line, every run of white space and control
    /// characters one space, trimmed and cut to at most
    /// <see cref="MaxSearchQueryBytes"/> bytes at a character boundary. Null
    /// (Go's false) when the answer is not a JSON object, has no "query", or
    /// its "query" is not a string or leaves nothing. The JSON is read as
    /// Go's encoding/json reads it (GoJson).
    /// </summary>
    public static string? ParseSearchQuery(ReadOnlySpan<byte> structured)
    {
        var b = structured.ToArray();
        var (lo, hi) = TrimSpace(b, 0, b.Length);
        if (hi <= lo || !GoJson.Valid(b, lo, hi) || GoJson.Object.Of(b, (lo, hi)) is not { } o
            || GoJson.String(b, o.Member("query")) is not { } s)
        {
            return null;
        }
        var output = new List<byte>(s.Length);
        var space = false;
        var sb = Utf8(s);
        var i = 0;
        while (i < sb.Length)
        {
            var (c, w) = DecodeRune(sb, i, sb.Length);
            i += w;
            if (IsSpace(c) || IsControl(c))
            {
                space = output.Count > 0;
                continue;
            }
            if (space)
            {
                output.Add(0x20);
                space = false;
            }
            AppendUtf8(c, output);
        }
        if (output.Count > MaxSearchQueryBytes)
        {
            var cut = MaxSearchQueryBytes;
            while (cut > 0 && !RuneStart(output[cut]))
            {
                cut--;
            }
            output.RemoveRange(cut, output.Count - cut);
            while (output.Count > 0 && output[^1] == 0x20)
            {
                output.RemoveAt(output.Count - 1);
            }
        }
        return output.Count == 0 ? null : FromUtf8(CollectionsMarshal.AsSpan(output));
    }

    // Texts

    /// <summary>assistant.SearchTexts: the fixed texts of the search in the user's own words, translated.</summary>
    public static SearchStrings SearchTexts() => new()
    {
        // TRANSLATORS: An item of the search field's menu: the assistant turns what was typed into a search.
        OwnWords = L10n.T("Search in Your Own Words"),
        // TRANSLATORS: The search field's placeholder while the assistant turns the typed words into a search.
        Converting = L10n.T("Converting the search…"),
    };

    /// <summary>
    /// assistant.SearchFailedText: the toast when the words could not be
    /// turned into a search (the words stay in the box).
    /// <paramref name="reason"/> is technical (Claude Code not found or not
    /// signed in, the result's text, stderr, a timeout) and shown as data, as
    /// StoppedText shows it: its first non-empty line without control
    /// characters, at most 400 bytes; "unknown" when nothing is left.
    /// </summary>
    public static string SearchFailedText(string reason)
    {
        ArgumentNullException.ThrowIfNull(reason);
        // TRANSLATORS: %s is a technical reason.
        return L10n.T("The search could not be converted: %s", FirstLine(reason, MaxReason));
    }
}
