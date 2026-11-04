// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the keys suggest.go's onKey reads off a recipient row (and of
// the doCommandBy selectors of macos/Sources/MalachiMail/Compose/
// RecipientSuggestionsController.swift): the arrows, Return and KP_Enter,
// Tab, Escape.

namespace Malachi.Core.Presentation;

/// <summary>A key a recipient row hands to its suggestions while they show.</summary>
public enum SuggestionKey
{
    /// <summary>Down: the next suggestion.</summary>
    Down,

    /// <summary>Up: the previous suggestion.</summary>
    Up,

    /// <summary>Enter (Return, KP_Enter): accept.</summary>
    Enter,

    /// <summary>Tab: accept.</summary>
    Tab,

    /// <summary>Escape: hide.</summary>
    Escape,
}
