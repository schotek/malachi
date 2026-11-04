// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/RecipientTokens.swift; GTK:
// ui/internal/recipients/tokens.go (Tokens, Token, split, sanitize, isSpace,
// cut).
//
// The model behind the To/Cc/Bcc fields of the compose window. Every
// finished address is a token (a badge with an ×), what is still being
// typed is the pending text after the last token. Pure logic, no WinUI: the
// view shows Tokens and Pending and feeds every keystroke to SetPending. It
// rests on the list rules of AddressList (commas and semicolons outside
// quotes and angle brackets separate, one mailbox per entry). Email is
// hostile input: the text a paste adds is bounded, text is stripped of
// control characters, and nothing is ever dropped silently (typed and
// initial text is never clipped; what does not fit under the token cap stays
// pending). Go counts runes and UTF-8 bytes, Swift Unicode scalars; here the
// text is read as scalars as well (a lone surrogate, which neither can hold,
// is U+FFFD as Go's range reads invalid UTF-8) and the paste limit counts
// UTF-8 bytes of them.

using System;
using System.Buffers;
using System.Collections.Generic;
using System.Text;
using Malachi.Core.Api;

namespace Malachi.Core.Compose;

/// <summary>The value of one recipient field: finished tokens and the text typed after them.</summary>
public sealed class RecipientTokens
{
    /// <summary>
    /// Most bytes of UTF-8 of pasted text (pending plus the paste) that are
    /// looked at; the rest of a paste is ignored. Typed and initial text is
    /// never clipped: nothing a user has in a field is lost silently.
    /// </summary>
    public const int MaxInput = 64 << 10;

    /// <summary>
    /// The most tokens text is split into automatically; the rest of it
    /// stays in <see cref="Pending"/> verbatim. <see cref="Add"/> and
    /// <see cref="Commit"/> still append.
    /// </summary>
    public const int MaxTokens = 1000;

    // The bidirectional controls a badge never shows (an RLM, an override
    // or isolate in a name could reorder the text around it).
    private static readonly SearchValues<char> BidiControls = SearchValues.Create(
        [(char)0x061C, (char)0x200E, (char)0x200F, (char)0x202A, (char)0x202B, (char)0x202C, (char)0x202D, (char)0x202E, (char)0x2066, (char)0x2067, (char)0x2068, (char)0x2069]);

    private readonly List<Token> tokens = [];
    private string pending = "";

    /// <summary>An empty field.</summary>
    public RecipientTokens()
    {
    }

    private RecipientTokens(RecipientTokens other)
    {
        tokens.AddRange(other.tokens);
        pending = other.pending;
    }

    /// <summary>
    /// Reads a whole field value: every non-empty entry becomes a token,
    /// valid or not. Nothing is pending unless <see cref="MaxTokens"/> is
    /// reached; the text from the first entry that no longer fits stays in
    /// <see cref="Pending"/> verbatim.
    /// </summary>
    public RecipientTokens(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var s = Sanitize(Scalars(text));
        var spans = Split(s, lineBreaks: false);
        if (SplitInto(s, spans, spans.Count, capped: true) is { } i)
        {
            pending = Str(TrimLeft(s, spans[i].Start));
        }
    }

    /// <summary>The tokens in order.</summary>
    public IReadOnlyList<Token> Tokens => tokens;

    /// <summary>The text typed after the last token.</summary>
    public string Pending => pending;

    /// <summary>Whether any token is not a mailbox.</summary>
    public bool HasInvalid => tokens.Exists(t => !t.IsValid);

    /// <summary>
    /// The field value the rest of the app sees: valid tokens as
    /// <see cref="AddressList.Format"/> writes them, invalid ones as typed,
    /// then the pending text unless blank, joined by ", ".
    /// </summary>
    public string Text
    {
        get
        {
            var parts = new List<string>(tokens.Count + 1);
            foreach (var token in tokens)
            {
                parts.Add(token.Address is { } a ? AddressList.Format([a]) : token.Raw);
            }
            var p = Trim(pending);
            if (p.Length > 0)
            {
                parts.Add(p);
            }
            return string.Join(", ", parts);
        }
    }

    /// <summary>
    /// The recipients as they would be if pending were committed now,
    /// without changing the model: the addresses of the valid tokens in
    /// order, the raw text of the invalid ones (the shape of
    /// <see cref="AddressList.Parse"/>), the non-blank pending text
    /// evaluated as <see cref="Commit"/> would, so it may yield several
    /// entries. Unlike re-parsing <see cref="Text"/> it never merges or
    /// re-reads tokens (two invalid entries joined by a comma can make one
    /// valid address, a name with a colon makes a valid one invalid).
    /// </summary>
    public (IReadOnlyList<Address> Addresses, IReadOnlyList<string> Invalid) Resolved()
    {
        var committed = new RecipientTokens(this);
        committed.Commit();
        var addresses = new List<Address>();
        var invalid = new List<string>();
        foreach (var token in committed.tokens)
        {
            if (token.Address is { } a)
            {
                addresses.Add(a);
            }
            else
            {
                invalid.Add(token.Raw);
            }
        }
        return (addresses, invalid);
    }

    /// <summary>
    /// Takes the editor's full text after a keystroke. Entries completed by
    /// a separator become tokens and the rest stays pending; so does a bare
    /// address followed by whitespace ("a@b.cz "). Returns whether the
    /// tokens changed, in which case the editor must be reset to
    /// <see cref="Pending"/>.
    /// </summary>
    public bool SetPending(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var s = Sanitize(Scalars(text));
        var spans = Split(s, lineBreaks: false);
        if (spans.Count > 1)
        {
            if (SplitInto(s, spans, spans.Count - 1, capped: true) is { } i)
            {
                pending = Str(TrimLeft(s, spans[i].Start));
                return true;
            }
            pending = Str(TrimLeft(s, spans[^1].Start));
            Fold();
            return true;
        }
        pending = Str(s);
        return Fold();
    }

    /// <summary>
    /// Turns non-blank pending text into tokens, valid or not (Enter, Tab,
    /// focus loss); blank pending is just cleared. Normally pending is one
    /// entry; the rest left by <see cref="MaxTokens"/> is split into entries
    /// and appended past the cap. Returns whether the tokens changed.
    /// </summary>
    public bool Commit()
    {
        var s = Sanitize(Scalars(pending));
        pending = "";
        var n = tokens.Count;
        SplitInto(s, Split(s, lineBreaks: false), int.MaxValue, capped: false);
        return tokens.Count != n;
    }

    /// <summary>Appends a valid token (a suggestion picked) and clears pending.</summary>
    public void Add(Address address)
    {
        ArgumentNullException.ThrowIfNull(address);
        pending = "";
        var name = Trim(Str(Sanitize(Scalars(address.Name ?? ""))));
        var a = address with { Name = name.Length == 0 ? null : name };
        tokens.Add(new Token(AddressList.Format([a]), a));
    }

    /// <summary>Drops the token at <paramref name="index"/>; out of range does nothing.</summary>
    public void Remove(int index)
    {
        if (index < 0 || index >= tokens.Count)
        {
            return;
        }
        tokens.RemoveAt(index);
    }

    /// <summary>
    /// Commits pending, removes the token at <paramref name="index"/> and
    /// makes its text the pending string, which it returns. Out of range
    /// changes nothing and returns the current pending text.
    /// </summary>
    public string Edit(int index)
    {
        if (index < 0 || index >= tokens.Count)
        {
            return pending;
        }
        Commit();
        var token = tokens[index];
        Remove(index);
        pending = token.Address is { } a ? AddressList.Format([a]) : token.Raw;
        return pending;
    }

    /// <summary>
    /// Adds pasted text to pending. Without any separator (comma,
    /// semicolon, line break, tab) it is typed text and goes through
    /// <see cref="SetPending"/>; otherwise every entry, the last one too,
    /// becomes a token. At most <see cref="MaxInput"/> bytes of pending and
    /// the paste together are looked at.
    /// </summary>
    public void Paste(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var own = Scalars(pending);
        var room = Math.Max(0, MaxInput - ByteCount(own));
        var pasted = Cut(Scalars(text), room);
        var all = new int[own.Length + pasted.Length];
        own.CopyTo(all, 0);
        pasted.CopyTo(all, own.Length);
        var spans = Split(all, lineBreaks: true);
        if (spans.Count <= 1)
        {
            SetPending(Str(all));
            return;
        }
        pending = "";
        if (SplitInto(all, spans, spans.Count, capped: true) is { } i)
        {
            // Line breaks and tabs were separators: they stay as commas.
            var rest = new List<string>();
            for (var k = i; k < spans.Count; k++)
            {
                var raw = Trim(Str(Sanitize(all[spans[k].Start..spans[k].End])));
                if (raw.Length > 0)
                {
                    rest.Add(raw);
                }
            }
            pending = string.Join(", ", rest);
        }
    }

    // A pending bare addr-spec that ends in whitespace becomes a token.
    private bool Fold()
    {
        if (pending.Length == 0 || tokens.Count >= MaxTokens || !IsSpace(Scalars(pending)[^1]))
        {
            return false;
        }
        var raw = Trim(pending);
        if (AddressList.ParseAddress(raw) is not { } a || !string.IsNullOrEmpty(a.Name) || raw.Contains('<', StringComparison.Ordinal))
        {
            return false;
        }
        pending = "";
        tokens.Add(new Token(raw, a));
        return true;
    }

    // Makes a token of every non-empty entry among the first count spans of
    // s. When capped and MaxTokens is reached it stops and returns the index
    // of the first entry left, whose text and everything after it the
    // caller keeps; null when all were taken.
    private int? SplitInto(int[] s, IReadOnlyList<Span> spans, int count, bool capped)
    {
        for (var i = 0; i < spans.Count && i < count; i++)
        {
            var raw = Trim(Str(Sanitize(s[spans[i].Start..spans[i].End])));
            if (raw.Length == 0)
            {
                continue;
            }
            if (capped && tokens.Count >= MaxTokens)
            {
                return i;
            }
            tokens.Add(new Token(raw, AddressList.ParseAddress(raw)));
        }
        return null;
    }

    // recipients.split: AddressList.SplitRanges over scalars; with
    // lineBreaks, CR, LF and tab outside quotes and angle brackets
    // separate as well.
    private static List<Span> Split(int[] s, bool lineBreaks)
    {
        var output = new List<Span>();
        var start = 0;
        var quoted = false;
        var angled = false;
        var escaped = false;
        for (var i = 0; i < s.Length; i++)
        {
            var r = s[i];
            if (escaped)
            {
                escaped = false;
            }
            else if (r == '\\' && quoted)
            {
                escaped = true;
            }
            else if (r == '"')
            {
                quoted = !quoted;
            }
            else if (quoted)
            {
                // Inside quotes nothing separates.
            }
            else if (r == '<')
            {
                angled = true;
            }
            else if (r == '>')
            {
                angled = false;
            }
            else if ((r == ',' || r == ';' || (lineBreaks && (r == '\n' || r == '\r' || r == '\t'))) && !angled)
            {
                output.Add(new Span(start, i));
                start = i + 1;
            }
        }
        output.Add(new Span(start, s.Length));
        return output;
    }

    private static int Width(int r) => r < 0x80 ? 1 : r < 0x800 ? 2 : r < 0x10000 ? 3 : 4;

    private static int ByteCount(int[] s)
    {
        var n = 0;
        foreach (var r in s)
        {
            n += Width(r);
        }
        return n;
    }

    // s limited to n bytes of UTF-8, on a scalar boundary.
    private static int[] Cut(int[] s, int n)
    {
        var used = 0;
        for (var i = 0; i < s.Length; i++)
        {
            used += Width(s[i]);
            if (used > n)
            {
                return s[..i];
            }
        }
        return s;
    }

    // The scalars of text, a lone surrogate as U+FFFD.
    private static int[] Scalars(string text)
    {
        var s = ScalarText.Of(text);
        for (var i = 0; i < s.Length; i++)
        {
            if (s[i] < 0)
            {
                s[i] = 0xFFFD;
            }
        }
        return s;
    }

    // Tab and line breaks become spaces; the other C0 controls, DEL and the
    // Unicode line and paragraph separators are dropped.
    private static int[] Sanitize(int[] s)
    {
        var output = new List<int>(s.Length);
        foreach (var r in s)
        {
            if (r is 0x09 or 0x0a or 0x0d)
            {
                output.Add(' ');
            }
            else if (r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029)
            {
                continue;
            }
            else
            {
                output.Add(r);
            }
        }
        return [.. output];
    }

    // Unicode white space, listed so that the Go and Swift ports agree.
    private static bool IsSpace(int r) =>
        (r >= 9 && r <= 13) || r == 32 || r == 0x85 || r == 0xa0 || r == 0x1680
        || (r >= 0x2000 && r <= 0x200a) || r == 0x2028 || r == 0x2029 || r == 0x202f
        || r == 0x205f || r == 0x3000;

    private static string Trim(string s)
    {
        var scalars = Scalars(s);
        var start = 0;
        var end = scalars.Length;
        while (start < end && IsSpace(scalars[start]))
        {
            start++;
        }
        while (end > start && IsSpace(scalars[end - 1]))
        {
            end--;
        }
        return Str(scalars, start, end);
    }

    private static int[] TrimLeft(int[] s, int from)
    {
        var start = from;
        while (start < s.Length && IsSpace(s[start]))
        {
            start++;
        }
        return s[start..];
    }

    private static string Str(int[] s) => Str(s, 0, s.Length);

    private static string Str(int[] s, int start, int end)
    {
        var b = new StringBuilder(end - start);
        for (var i = start; i < end; i++)
        {
            ScalarText.Append(b, s[i]);
        }
        return b.ToString();
    }

    private static string WithoutBidi(string s)
    {
        if (s.AsSpan().IndexOfAny(BidiControls) < 0)
        {
            return s;
        }
        var b = new StringBuilder(s.Length);
        foreach (var c in s)
        {
            if (!BidiControls.Contains(c))
            {
                b.Append(c);
            }
        }
        return b.ToString();
    }

    // A range [Start, End) of scalars.
    private readonly record struct Span(int Start, int End);

    /// <summary>One finished entry of the field.</summary>
    /// <param name="Raw">The trimmed text the token was made from.</param>
    /// <param name="Address">The parsed mailbox, null when <paramref name="Raw"/> is not one.</param>
    public sealed record Token(string Raw, Address? Address)
    {
        /// <summary>Whether the entry parsed as a mailbox.</summary>
        public bool IsValid => Address is not null;

        /// <summary>
        /// What the badge shows: the display name, else the address; for an
        /// invalid entry its text. Without bidirectional controls.
        /// </summary>
        public string Label
        {
            get
            {
                if (Address is not { } a)
                {
                    return WithoutBidi(Raw);
                }
                var name = Trim(a.Name ?? "");
                return WithoutBidi(name.Length == 0 ? a.Email : name);
            }
        }

        /// <summary>
        /// The full "Name &lt;addr&gt;" form, or the text of an invalid
        /// entry. Without bidirectional controls.
        /// </summary>
        public string Tooltip => WithoutBidi(Address is { } a ? AddressList.Format([a]) : Raw);
    }
}
