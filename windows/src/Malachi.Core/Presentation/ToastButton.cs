// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// GTK: Adw.Toast's button-label and button-clicked, as window/undo.go
// showUndoToast sets them (Undo (Ctrl+Z) after a move of mail).

using System;

namespace Malachi.Core.Presentation;

/// <summary>The button of a toast: a click dismisses the toast and runs <paramref name="Clicked"/>.</summary>
/// <param name="Label">Its text (translated by the caller).</param>
/// <param name="Clicked">What it does, on the UI thread.</param>
public sealed record ToastButton(string Label, Action Clicked);
