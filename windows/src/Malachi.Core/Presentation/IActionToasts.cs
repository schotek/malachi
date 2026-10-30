// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// GTK: the window's Adw.ToastOverlay as window/undo.go uses it for the
// toast of a move (AddToast of a toast with a button, Dismiss of the
// previous one). The main window's overlay (Malachi.App ToastHost) is one;
// a controller of Core shows its toasts with a button through it.

namespace Malachi.Core.Presentation;

/// <summary>An overlay that shows toasts with a button and takes a shown one away again. UI-thread-affine.</summary>
public interface IActionToasts
{
    /// <summary>
    /// Shows <paramref name="toast"/> as <see cref="ToastPresenter.Show(Toast)"/>
    /// does and returns the toast shown, for <see cref="Dismiss"/>.
    /// </summary>
    Toast Show(Toast toast);

    /// <summary>Takes <paramref name="toast"/> away, shown or waiting; one already gone is left alone.</summary>
    void Dismiss(Toast toast);
}
