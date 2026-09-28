// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5): what decides a key
// besides the key itself: the window, the ctrl-r setting (macOS's
// command-r), and where the keyboard focus is (GTK's setTypingAccels lifts
// the single-key shortcuts while a text entry has focus; the editor keeps
// Escape for its bridge).

using Malachi.Core.Settings;

namespace Malachi.Core.Presentation;

/// <summary>Where a key was pressed.</summary>
/// <param name="Window">The kind of window.</param>
/// <param name="CtrlR">What Ctrl+R does (the <c>ctrl-r</c> setting).</param>
/// <param name="TextInputFocused">A text input has the focus (a text box, the search box, the editor): single keys type.</param>
/// <param name="EditorFocused">The compose editor (a WebView2) has the focus: Escape goes to its bridge.</param>
public readonly record struct ShortcutContext(
    WindowKind Window,
    CtrlR CtrlR = CtrlR.Reply,
    bool TextInputFocused = false,
    bool EditorFocused = false);
