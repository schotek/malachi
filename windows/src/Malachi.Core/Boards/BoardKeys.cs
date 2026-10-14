// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardKeys.swift; GTK: ui/internal/board/keys.go
// (KeyAction, Key, BoardKeys, KeysGroup, ShowMailKey, ShowBoardKey,
// DoneKey, KeyFor, EscapeTarget, EscapeFor).
//
// The board's keys and its Escape, the part that needs no WinUI: which
// shortcuts there are, with their texts for the shortcuts list and the
// menus, and where Escape goes. Every client binds the same keys: Primary
// is Ctrl on Windows. The single keys work only while the board shows and
// the keyboard is not in a text field (a reply editor, a recipient field,
// the instruction of Suggest Reply), so typing never acts on a case.

using System.Collections.Generic;
using Malachi.Core.I18n;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>What a key of the board does.</summary>
    public enum KeyAction
    {
        /// <summary>Switches the main window to Mail (Primary+1).</summary>
        ShowMail,

        /// <summary>Switches it to the Board (Primary+2).</summary>
        ShowBoard,

        /// <summary>Archive on the selected case (E).</summary>
        Archive,

        /// <summary>Done on the selected case, or Move Back to Board on a done one (D).</summary>
        Done,

        /// <summary>Opens Remind… on the selected case (R).</summary>
        Remind,
    }

    /// <summary>Where Escape goes on the board (<see cref="EscapeFor"/>).</summary>
    public enum EscapeTarget
    {
        /// <summary>Escape does nothing here (the List's own page).</summary>
        Nothing,

        /// <summary>Closes the open popup (a menu, a flyout, the recipients' suggestions) and nothing else.</summary>
        ClosePopup,

        /// <summary>Moves the keyboard from the reply editor or a recipient field to the detail's state pill; nothing closes.</summary>
        FocusStatePill,

        /// <summary>Closes the sliding detail panel (Columns, Today, a narrow List).</summary>
        ClosePanel,
    }

    /// <summary>A shortcut of the board.</summary>
    /// <param name="Action">What it does.</param>
    /// <param name="Primary">With the platform's primary modifier (Ctrl); else a single key without modifiers.</param>
    /// <param name="Rune">The key, lower case: '1', '2', 'e', 'd', 'r'.</param>
    /// <param name="BoardOnly">Only while the board shows and the keyboard is not in a text field; the mode keys work in both modes.</param>
    /// <param name="Title">The shortcut's text in the shortcuts list.</param>
    public sealed record Key(KeyAction Action, bool Primary, char Rune, bool BoardOnly, string Title);

    /// <summary>The board's shortcuts in the shortcuts list's order.</summary>
    public static IReadOnlyList<Key> BoardKeys() =>
    [
        new(KeyAction.ShowMail, true, '1', false, Text.ShowMailKey),
        new(KeyAction.ShowBoard, true, '2', false, Text.ShowBoardKey),
        new(KeyAction.Archive, false, 'e', true, Text.Archive),
        new(KeyAction.Done, false, 'd', true, Text.DoneKey),
        new(KeyAction.Remind, false, 'r', true, Text.Remind),
    ];

    /// <summary>The heading of the board's group in the shortcuts list.</summary>
    public static string KeysGroup => Text.BoardName;

    /// <summary>
    /// The action of a key pressed while the main window is in
    /// <paramref name="mode"/>: <paramref name="key"/> is the key (any case),
    /// <paramref name="primary"/> whether the primary modifier is held and
    /// <paramref name="other"/> whether any other modifier (Shift, Alt) is;
    /// <paramref name="inText"/> whether the keyboard is in a text field.
    /// Null when the key is not the board's.
    /// </summary>
    public static KeyAction? KeyFor(char key, bool primary, bool other, bool inText, Mode mode)
    {
        if (other)
        {
            return null;
        }
        if (key is >= 'A' and <= 'Z')
        {
            key = (char)(key + ('a' - 'A'));
        }
        foreach (var k in BoardKeys())
        {
            if (k.Rune != key || k.Primary != primary)
            {
                continue;
            }
            if (k.BoardOnly && (mode != Mode.Board || inText))
            {
                return null;
            }
            return k.Action;
        }
        return null;
    }

    /// <summary>
    /// Where Escape goes, in two steps: an open popup closes first; the
    /// keyboard in the reply editor or the recipient fields goes to the state
    /// pill; anywhere else on the page an open panel closes. Without a panel
    /// nothing happens.
    /// </summary>
    public static EscapeTarget EscapeFor(bool focusInEditorOrRecipients, bool popupOpen, bool panelOpen)
    {
        if (popupOpen)
        {
            return EscapeTarget.ClosePopup;
        }
        if (focusInEditorOrRecipients)
        {
            return EscapeTarget.FocusStatePill;
        }
        return panelOpen ? EscapeTarget.ClosePanel : EscapeTarget.Nothing;
    }

    public static partial class Text
    {
        /// <summary>The shortcuts list's text of Primary+1.</summary>
        public static string ShowMailKey =>
            // TRANSLATORS: the shortcuts window: the key that shows the mail in the
            // main window.
            L10n.T("Show Mail");

        /// <summary>The shortcuts list's text of Primary+2.</summary>
        public static string ShowBoardKey =>
            // TRANSLATORS: the shortcuts window: the key that shows the board in the
            // main window.
            L10n.T("Show Board");

        /// <summary>The shortcuts list's text of D.</summary>
        public static string DoneKey =>
            // TRANSLATORS: the shortcuts window: the key that marks the selected
            // case done, or moves a done one back to the board.
            L10n.T("Mark as Done or Move Back to Board");
    }
}
