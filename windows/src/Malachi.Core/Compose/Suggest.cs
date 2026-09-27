// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Compose/Suggest.swift; GTK:
// ui/internal/compose/suggest.go (suggestMinChars, suggestDebounce,
// suggestLimit, tokenAt, replaceToken, byteOffset, suggestionIcon,
// suggestionTooltip).
//
// The pure part of recipient completion: it finds the address token under
// the caret, asks the daemon, and inserts the answer. The popup and its keys
// belong to the view. Caret positions are Unicode scalar offsets, the
// character positions a GTK entry reports and the Swift port keeps; the
// view converts TextBox.SelectionStart (UTF-16) with ScalarOffset. Go's byte
// offsets and Swift's String.Index are UTF-16 indices here.

using System;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Compose;

/// <summary>Recipient completion: the token under the caret and its replacement.</summary>
public static class Suggest
{
    /// <summary>
    /// suggestMinChars: how many characters a token needs before the daemon
    /// is asked (a directory book searches on its server).
    /// </summary>
    public const int SuggestMinChars = 2;

    /// <summary>suggestLimit: how many suggestions are asked for and shown.</summary>
    public const int SuggestLimit = 8;

    /// <summary>suggestDebounce: how long typing must pause before a search.</summary>
    public static TimeSpan SuggestDebounce { get; } = TimeSpan.FromMilliseconds(150);

    /// <summary>
    /// compose.tokenAt: the address token the caret is in: the run between
    /// the separators <see cref="AddressList.SplitRanges"/> honours, without
    /// the spaces and tabs around it. <paramref name="caret"/> is a scalar
    /// offset; the range is in <paramref name="text"/>. A caret past the text
    /// is the last token.
    /// </summary>
    public static (string Token, TextSpan Range) TokenAt(string text, int caret)
    {
        ArgumentNullException.ThrowIfNull(text);
        var pos = IndexAtScalarOffset(caret, text);
        foreach (var r in AddressList.SplitRanges(text))
        {
            if (pos < r.Start || pos > r.End)
            {
                continue;
            }
            var s = r.Start;
            var e = r.End;
            while (s < e && text[s] is ' ' or '\t')
            {
                s++;
            }
            while (e > s && text[e - 1] is ' ' or '\t')
            {
                e--;
            }
            return (text[s..e], new TextSpan(s, e));
        }
        return ("", new TextSpan(text.Length, text.Length));
    }

    /// <summary>
    /// compose.replaceToken: swaps <paramref name="range"/> of
    /// <paramref name="text"/> for the formatted address. A separator and a
    /// space follow unless one is already there (spaces the token had after
    /// it are dropped), and the caret (a scalar offset) lands after the
    /// inserted address.
    /// </summary>
    public static (string Text, int Caret) ReplaceToken(string text, TextSpan range, Address address)
    {
        ArgumentNullException.ThrowIfNull(text);
        ArgumentNullException.ThrowIfNull(address);
        var insert = AddressList.Format([address]);
        var restStart = range.End;
        while (restStart < text.Length && text[restStart] is ' ' or '\t')
        {
            restStart++;
        }
        var rest = text[restStart..];
        if (rest.Length == 0 || (rest[0] != ',' && rest[0] != ';'))
        {
            insert += ", ";
        }
        var head = text[..range.Start] + insert;
        return (head + rest, ScalarText.CountBefore(head, head.Length));
    }

    /// <summary>
    /// compose.byteOffset in reverse: the scalar offset of the UTF-16
    /// <paramref name="index"/> in <paramref name="text"/>, what a caret
    /// position is.
    /// </summary>
    public static int ScalarOffset(int index, string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        return ScalarText.CountBefore(text, index);
    }

    /// <summary>
    /// compose.byteOffset: the UTF-16 index of the scalar at
    /// <paramref name="offset"/>, clamped to the text (a negative offset is
    /// the start, one past the end is the end).
    /// </summary>
    public static int IndexAtScalarOffset(int offset, string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        return offset <= 0 ? 0 : ScalarText.IndexOf(text, offset);
    }

    /// <summary>compose.suggestionIcon: the GTK icon name for a suggestion's source.</summary>
    public static string SuggestionIcon(ContactSource source) =>
        source == ContactSource.AddressBook ? "x-office-address-book-symbolic" : "document-open-recent-symbolic";

    /// <summary>
    /// compose.suggestionTooltip: where a suggestion comes from: the address
    /// book's own name when it has one.
    /// </summary>
    public static string SuggestionTooltip(Contact contact)
    {
        ArgumentNullException.ThrowIfNull(contact);
        if (contact.Source == ContactSource.AddressBook)
        {
            if (!string.IsNullOrEmpty(contact.Book))
            {
                return contact.Book;
            }
            // TRANSLATORS: tooltip of a recipient suggestion that came from a
            // system address book whose name is unknown.
            return L10n.T("Address book");
        }
        // TRANSLATORS: tooltip of a recipient suggestion that is an address
        // the user has written to before.
        return L10n.T("Recently used");
    }
}
