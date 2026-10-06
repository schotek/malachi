// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The key map of docs/windows-port.md §11.5 (a listed deviation of
// windows/README.md), over GTK's accelerators: ui/main.go addActions
// (app.compose Ctrl+N, app.preferences Ctrl+comma, app.quit Ctrl+Q, in
// every window), window.go MessageAccels (win.* of the main window:
// Delete, a, j, u, s, Ctrl+R, Ctrl+F), message_window.go messageShortcuts
// (msg.*: Delete, a, j, u, s), the Escape shortcut controllers of
// message_window.blp, embedded_window.blp and compose.blp, compose.blp's
// compose.send (Ctrl+Return) and compose.save (Ctrl+S),
// accounts_reorder.go (Ctrl+Up, Ctrl+Down) and F10, which GtkWindow gives
// to the primary menu (window.blp's MenuButton with primary: true); macOS:
// App/MainMenu.swift's key table with command-r and Actions.swift's
// bareKeyActions.
//
// The Windows keys: Ctrl+R replies (the ctrl-r setting, default "reply",
// gives it to Check for New Mail instead, as macOS's command-r does),
// Ctrl+Shift+R replies to all, Ctrl+Shift+F forwards, F5 checks for new
// mail, Ctrl+E searches besides Ctrl+F, and Ctrl+W closes a secondary
// window besides Escape. The single keys (Delete, A, J, U, S) do nothing
// while a text input has the focus, where they type (GTK
// setTypingAccels); the compose editor counts as one, and keeps Escape for
// its bridge (the bridge closes the window through its own message, as in
// GTK and macOS).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Settings;

namespace Malachi.Core.Presentation;

/// <summary>Which command a key runs where.</summary>
public static class ShortcutMap
{
    // Every window: the application's accelerators.
    private static readonly (KeyChord Chord, ShortcutCommand Command)[] Application =
    [
        (KeyChord.Ctrl(VirtualKey.N), ShortcutCommand.NewMessage),
        (KeyChord.Ctrl(VirtualKey.Comma), ShortcutCommand.Preferences),
        (KeyChord.Ctrl(VirtualKey.Q), ShortcutCommand.Quit),
    ];

    // The per-message keys of the main window and of a message window.
    private static readonly (KeyChord Chord, ShortcutCommand Command)[] Message =
    [
        (KeyChord.CtrlShift(VirtualKey.R), ShortcutCommand.ReplyAll),
        (KeyChord.CtrlShift(VirtualKey.F), ShortcutCommand.Forward),
        (KeyChord.Bare(VirtualKey.Delete), ShortcutCommand.Trash),
        (KeyChord.Bare(VirtualKey.A), ShortcutCommand.Archive),
        (KeyChord.Bare(VirtualKey.J), ShortcutCommand.Junk),
        (KeyChord.Bare(VirtualKey.U), ShortcutCommand.MarkUnread),
        (KeyChord.Bare(VirtualKey.S), ShortcutCommand.ToggleFlag),
    ];

    // F10 opens the primary menu from anywhere in the window, a text input
    // included (gtk_window_activate_menubar), where it types nothing.
    private static readonly (KeyChord Chord, ShortcutCommand Command)[] Main =
    [
        (KeyChord.Bare(VirtualKey.F5), ShortcutCommand.CheckForNewMail),
        (KeyChord.Ctrl(VirtualKey.F), ShortcutCommand.Search),
        (KeyChord.Ctrl(VirtualKey.E), ShortcutCommand.Search),
        (KeyChord.Bare(VirtualKey.F10), ShortcutCommand.MainMenu),
    ];

    private static readonly (KeyChord Chord, ShortcutCommand Command)[] Close =
    [
        (KeyChord.Bare(VirtualKey.Escape), ShortcutCommand.CloseWindow),
        (KeyChord.Ctrl(VirtualKey.W), ShortcutCommand.CloseWindow),
    ];

    private static readonly (KeyChord Chord, ShortcutCommand Command)[] Compose =
    [
        (KeyChord.Ctrl(VirtualKey.Enter), ShortcutCommand.Send),
        (KeyChord.Ctrl(VirtualKey.S), ShortcutCommand.SaveDraft),
    ];

    private static readonly (KeyChord Chord, ShortcutCommand Command)[] Reorder =
    [
        (KeyChord.Ctrl(VirtualKey.Up), ShortcutCommand.MoveUp),
        (KeyChord.Ctrl(VirtualKey.Down), ShortcutCommand.MoveDown),
    ];

    private static readonly KeyChord ReplyOrRefresh = KeyChord.Ctrl(VirtualKey.R);

    // The browser's own keys, swallowed before a focused WebView2 sees them
    // (defence in depth: AreBrowserAcceleratorKeysEnabled=false already
    // turns them off for real input; spikes2/INPUT-SPIKES.md §5): reload,
    // find, print, the developer tools and caret browsing. Never an
    // editing key of the compose editor's bridge (Ctrl+B, I, U, K).
    private static readonly HashSet<KeyChord> Browser =
    [
        KeyChord.Bare(VirtualKey.F3),
        KeyChord.Bare(VirtualKey.F5),
        KeyChord.Bare(VirtualKey.F7),
        KeyChord.Bare(VirtualKey.F12),
        KeyChord.Ctrl(VirtualKey.R),
        KeyChord.CtrlShift(VirtualKey.R),
        KeyChord.Ctrl(VirtualKey.F),
        KeyChord.Ctrl(VirtualKey.G),
        KeyChord.Ctrl(VirtualKey.P),
        KeyChord.CtrlShift(VirtualKey.I),
        KeyChord.CtrlShift(VirtualKey.J),
    ];

    /// <summary>
    /// The command <paramref name="chord"/> runs in <paramref name="context"/>,
    /// or null when it runs none there (the key then goes on to the focused
    /// element: a single key types into a text input).
    /// </summary>
    public static ShortcutCommand? Resolve(KeyChord chord, ShortcutContext context)
    {
        if (chord == ReplyOrRefresh && context.Window is WindowKind.Main or WindowKind.Message)
        {
            if (context.CtrlR == CtrlR.Refresh)
            {
                // A message window has no Check for New Mail (the main
                // window's win.refresh; macOS disables it there too).
                return context.Window == WindowKind.Main ? ShortcutCommand.CheckForNewMail : null;
            }
            return ShortcutCommand.Reply;
        }
        if (context.Window == WindowKind.Main && context.EditorFocused)
        {
            // The board's inline reply editor (BoardReplyEditorHost): while
            // its page has the keyboard, Send and Save Draft are the
            // compose window's keys. Nowhere else in the main window, so
            // they are none of its chords (its accelerators): elsewhere in
            // the pane its own scoped accelerators run them.
            foreach (var (key, command) in Compose)
            {
                if (key == chord)
                {
                    return command;
                }
            }
        }
        foreach (var (key, command) in Table(context.Window))
        {
            if (key != chord)
            {
                continue;
            }
            if (chord.IsBare && context.TextInputFocused && IsSingleKey(command))
            {
                return null;
            }
            if (command == ShortcutCommand.CloseWindow && chord.Key == VirtualKey.Escape && context.EditorFocused)
            {
                return null;
            }
            return command;
        }
        return null;
    }

    /// <summary>
    /// Every key that runs a command in a window of <paramref name="window"/>
    /// (for the window's keyboard accelerators), whatever the focus and the
    /// ctrl-r setting.
    /// </summary>
    public static IReadOnlyList<KeyChord> Chords(WindowKind window)
    {
        var chords = Table(window).Select(e => e.Chord).ToList();
        if (window is WindowKind.Main or WindowKind.Message)
        {
            chords.Add(ReplyOrRefresh);
        }
        return chords.Distinct().ToList();
    }

    /// <summary>
    /// A command of a single key (Delete, A, J, U, S): lifted while a text
    /// input has the focus (GTK setTypingAccels, macOS bareKeyActions).
    /// </summary>
    public static bool IsSingleKey(ShortcutCommand command) => command is ShortcutCommand.Trash
        or ShortcutCommand.Archive or ShortcutCommand.Junk or ShortcutCommand.MarkUnread or ShortcutCommand.ToggleFlag;

    /// <summary>
    /// One of the browser's own keys, which the router swallows before a
    /// focused WebView2 sees it even when it runs no command.
    /// </summary>
    public static bool IsBrowserKey(KeyChord chord) => Browser.Contains(chord);

    private static IEnumerable<(KeyChord Chord, ShortcutCommand Command)> Table(WindowKind window)
    {
        IEnumerable<(KeyChord, ShortcutCommand)> own = window switch
        {
            WindowKind.Main => Main.Concat(Message),
            WindowKind.Message => Message.Concat(Close),
            WindowKind.Embedded => Close,
            WindowKind.Compose => Compose.Concat(Close),
            WindowKind.Preferences => Reorder.Concat(Close),
            WindowKind.Wizard => Close,
            // The attachment previewer closes as Sushi and Quick Look do.
            WindowKind.Other => Close,
            _ => [],
        };
        return Application.Concat(own);
    }
}
