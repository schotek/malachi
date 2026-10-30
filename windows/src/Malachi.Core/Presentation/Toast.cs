// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Shared/ToastPresenter.swift (Toast);
// GTK: Adw.Toast as window.go Toast/ToastFor makes it, and the toast with a
// button of window/undo.go showUndoToast (Adw.Toast's button-label and
// button-clicked).

namespace Malachi.Core.Presentation;

/// <summary>One transient message: plain text, never markup.</summary>
/// <param name="Text">What it says.</param>
/// <param name="Seconds">How long it stays; 0 keeps it until the next toast replaces it.</param>
/// <param name="Button">Its button (Undo of a move), or null for none.</param>
public sealed record Toast(string Text, int Seconds, ToastButton? Button = null);
