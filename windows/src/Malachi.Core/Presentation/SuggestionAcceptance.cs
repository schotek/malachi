// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of what suggest.go's accept (and macOS's accept(_:) of
// RecipientSuggestionsController.swift) puts into the row: replaceToken's
// text, and its caret as the index a TextBox takes.

namespace Malachi.Core.Presentation;

/// <summary>An accepted recipient suggestion: the row's new text and caret.</summary>
/// <param name="Text">The row with the token replaced by the formatted address and a separator.</param>
/// <param name="Caret">The caret after the separator, as a UTF-16 index (<c>TextBox.SelectionStart</c>).</param>
public sealed record SuggestionAcceptance(string Text, int Caret);
