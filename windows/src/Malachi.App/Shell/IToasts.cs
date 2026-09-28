// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Contracts.swift (Toasts); GTK:
// Adw.ToastOverlay through window.go Toast and ToastFor. The shell gives
// one to every window (Controls.ToastHost) and routes the application's
// toasts through ToastRouter. Text only, never markup: a toast often
// carries hostile input (a subject, a file name).

namespace Malachi.App.Shell;

/// <summary>Transient messages over a window.</summary>
public interface IToasts
{
    /// <summary>Shows <paramref name="text"/> for the default 5 s.</summary>
    void Show(string text);

    /// <summary>Shows <paramref name="text"/> for <paramref name="seconds"/>; 0 keeps it until the next toast replaces it.</summary>
    void Show(string text, int seconds);
}
