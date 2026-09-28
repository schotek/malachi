// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/compose/suggest.go (suggestionRow) and of the row
// macos/Sources/MalachiMail/Compose/RecipientSuggestionsController.swift
// draws (tableView(_:viewFor:row:)): what one suggestion shows. Every text is
// untrusted (a name, an address, an address book's name) and is shown as
// plain text, the name and the address cleaned for display (DisplayText,
// Windows-only; a collected name can be one a sender chose). A name counts
// when it is not empty once cleaned and trimmed, as Swift trims it (GTK
// only tests for empty), so a name of controls shows the address.
// Accepting a suggestion inserts the contact as received, not these texts.

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
        var name = DisplayText.Clean(contact.Name).Trim();
        var address = DisplayText.Clean(contact.Address);
        var named = name.Length > 0;
        return new SuggestionRow(
            named ? name : address,
            named ? address : "",
            Suggest.SuggestionIcon(contact.Source),
            Suggest.SuggestionTooltip(contact));
    }
}
