// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposePane.swift
// (ComposePaneHost); GTK: ui/internal/compose/pane.go (the Pane's hooks
// OnTitle, OnSendEnabled, OnToast, OnEnd, OnHeight and its dialogParent).
// What hosts a ComposePane: the compose window (ComposeWindow) or, inline,
// the board's case detail. The pane tells it of what the window around it
// shows (the title, Send), of toasts and of its end; everything else (the
// draft, the fields, the editor) is the pane's.

using Microsoft.UI.Xaml;

namespace Malachi.App.Compose;

/// <summary>What hosts a <see cref="ComposePane"/> (ComposePaneHost).</summary>
public interface IComposePaneHost
{
    /// <summary>
    /// The window the pane's dialogs and file pickers go on (paneWindow,
    /// dialogParent); null while there is none, when the pane asks nothing.
    /// </summary>
    Window? HostWindow { get; }

    /// <summary><see cref="ComposePane.TitleText"/> changed (the subject was edited).</summary>
    void TitleChanged(ComposePane pane);

    /// <summary>The draft controller enabled or disabled Send (<c>IComposeForm.SetSendEnabled</c>).</summary>
    void SendEnabledChanged(ComposePane pane, bool enabled);

    /// <summary>A short message for the user (<c>IComposeForm.Toast</c>).</summary>
    void Toast(string text);

    /// <summary>
    /// The draft controller decided the pane goes (<c>IComposeForm.CloseWindow</c>),
    /// or, for the board, that the draft was deleted elsewhere
    /// (<see cref="ComposePane.EndKind.Lost"/>). Called once.
    /// </summary>
    void Ended(ComposePane pane, ComposePane.EndKind kind);

    /// <summary>Inline layout: the editor took another height.</summary>
    void HeightChanged(ComposePane pane);
}
