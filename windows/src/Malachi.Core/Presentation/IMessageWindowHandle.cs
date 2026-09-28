// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of what macos/Sources/MalachiMail/Windows/WindowRegistry.swift and
// MessageWindows.swift ask of an NSWindowController (showWindow, close, its
// messageView, refreshStar); GTK: MessageWindow and EmbeddedWindow
// (Present, Close, view, setSeen and setStar). The app's message and
// attached-message windows implement it.

namespace Malachi.Core.Presentation;

/// <summary>A message window or an attached message's window, as the registry sees it.</summary>
public interface IMessageWindowHandle
{
    /// <summary>The message view in the window.</summary>
    ReaderController Reader { get; }

    /// <summary>Brings the window to the front (GTK <c>Present</c>).</summary>
    void Present();

    /// <summary>Closes the window.</summary>
    void Close();

    /// <summary>
    /// The flags of its message may have changed: the commands, the star and
    /// the trash button's tooltip follow (message_window.go <c>setSeen</c>,
    /// actions.go <c>refreshStars</c>). An attached message's window has none.
    /// </summary>
    void RefreshActions();
}
