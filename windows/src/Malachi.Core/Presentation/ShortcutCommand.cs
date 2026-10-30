// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5): the named commands the
// keyboard reaches, one per GTK action with an accelerator (ui/main.go
// addActions: app.compose, app.preferences, app.quit; window.go
// MessageAccels and actions.go: win.refresh, win.search, win.trash,
// win.archive, win.junk, win.mark-unread, win.toggle-flag; the msg.* of
// message_window.go; compose.blp's compose.send and compose.save; the
// window.close of the Escape shortcut controllers; accounts_reorder.go's
// Ctrl+Up/Down; the primary menu's F10, GtkWindow's own key; the
// calendar's keys.go ShowCalendarKey and New Event; win.undo of
// window/undo.go), plus the Windows keys
// for Reply, Reply All and Forward (macOS's reply:, replyAll:, forward:).

namespace Malachi.Core.Presentation;

/// <summary>A command a keyboard shortcut runs.</summary>
public enum ShortcutCommand
{
    /// <summary>New Message (app.compose, Ctrl+N).</summary>
    NewMessage,

    /// <summary>Preferences (app.preferences, Ctrl+,).</summary>
    Preferences,

    /// <summary>Quit (app.quit, Ctrl+Q).</summary>
    Quit,

    /// <summary>Check for New Mail (win.refresh, F5; Ctrl+R with ctrl-r = refresh).</summary>
    CheckForNewMail,

    /// <summary>Search (win.search, Ctrl+F, Ctrl+E).</summary>
    Search,

    /// <summary>The main window's Mail mode (Ctrl+1, Board.BoardKeys; reaches the window from a WebView2 too).</summary>
    ShowMail,

    /// <summary>The main window's Board mode (Ctrl+2, Board.BoardKeys).</summary>
    ShowBoard,

    /// <summary>The main window's Calendar mode (Ctrl+3, the calendar's keys.go ShowCalendarKey).</summary>
    ShowCalendar,

    /// <summary>
    /// New Event (Ctrl+Shift+N, Cal.CalendarKeys' Primary+Alt+N, macOS's
    /// ⌥⌘N File ▸ New Event): the main window in every mode, where
    /// Board.Allows says whether it runs.
    /// </summary>
    NewEvent,

    /// <summary>Opens the primary menu (window.blp's MenuButton with primary: true, F10).</summary>
    MainMenu,

    /// <summary>
    /// Undo of the newest move of mail (win.undo, Ctrl+Z; window/undo.go
    /// undoLast): the main window in Mail and Board, never while a text
    /// input or an editor has the keyboard, whose own Ctrl+Z takes back
    /// typing.
    /// </summary>
    Undo,

    /// <summary>Reply (Ctrl+R with ctrl-r = reply).</summary>
    Reply,

    /// <summary>Reply All (Ctrl+Shift+R).</summary>
    ReplyAll,

    /// <summary>Forward (Ctrl+Shift+F).</summary>
    Forward,

    /// <summary>Move to Trash, or cancel the send of an outbox message (win.trash, Delete).</summary>
    Trash,

    /// <summary>Archive (win.archive, A).</summary>
    Archive,

    /// <summary>Mark as Junk (win.junk, J).</summary>
    Junk,

    /// <summary>Mark as Unread (win.mark-unread, U).</summary>
    MarkUnread,

    /// <summary>Star or unstar (win.toggle-flag, S).</summary>
    ToggleFlag,

    /// <summary>Closes a secondary window (Escape, Ctrl+W).</summary>
    CloseWindow,

    /// <summary>Send (compose.send, Ctrl+Enter).</summary>
    Send,

    /// <summary>Save Draft (compose.save, Ctrl+S).</summary>
    SaveDraft,

    /// <summary>Moves the selected account up (Ctrl+Up).</summary>
    MoveUp,

    /// <summary>Moves the selected account down (Ctrl+Down).</summary>
    MoveDown,
}
