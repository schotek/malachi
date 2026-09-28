// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/compose/suggest.go (suggestionRow) and of the row
// macos/Sources/MalachiMail/Compose/RecipientSuggestionsController.swift
// draws (tableView(_:viewFor:row:)): what one suggestion shows. Every text is
// untrusted (a name, an address, an address book's name) and is shown as
// plain text, the name and the address cleaned for display (DisplayText,
// Windows-only; a collected name can be one a sender chose). GTK's test
// for a name (c.Name != "") is kept, applied to the cleaned name, which is
// not trimmed (Swift trims it first): a name of spaces, or of controls,
// which cleaning turns into spaces, is a name, shown blank over the
// address, as in GTK. A name that cleaning empties (only bidi controls) or
// that draws nothing (DisplayText.IsInvisible: a mark, ZWSP, a joiner)
// is none, so the address shows. Accepting a suggestion inserts the
// contact as received, not these texts.

using System;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Text;

namespace Malachi.Core.Presentation;

/// <summary>
/// One row of the recipient suggestions: the source icon, the name over
/// the address (or the address alone), where it comes from as the tooltip.
/// </summary>
/// <param name="Primary">The name, or the address when there is no name.</param>
/// <param name="Secondary">The address under the name; empty when <paramref name="Primary"/> is the address.</param>
/// <param name="Icon">The GTK icon name of the source (<see cref="Suggest.SuggestionIcon"/>).</param>
/// <param name="Tooltip">Where it comes from (<see cref="Suggest.SuggestionTooltip"/>).</param>
public sealed record SuggestionRow(string Primary, string Secondary, string Icon, string Tooltip)
{
    /// <summary>suggestionRow: the row of <paramref name="contact"/>.</summary>
    public static SuggestionRow For(Contact contact)
    {
        ArgumentNullException.ThrowIfNull(contact);
        var name = DisplayText.Clean(contact.Name);
        var address = DisplayText.Clean(contact.Address);
        var named = !DisplayText.IsInvisible(name);
        return new SuggestionRow(
            named ? name : address,
            named ? address : "",
            Suggest.SuggestionIcon(contact.Source),
            Suggest.SuggestionTooltip(contact));
    }
}
