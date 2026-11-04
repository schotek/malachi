// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/undo/text.go (Done, Button, Undone, Failed,
// Nothing): the toasts of a move and of its Undo, with the GTK msgids.

using System;
using Malachi.Core.I18n;

namespace Malachi.Core.Undos;

public static partial class Undo
{
    /// <summary>undo.Done: the text of the toast after the move of <paramref name="e"/>, what happened.</summary>
    public static string Done(Entry e)
    {
        ArgumentNullException.ThrowIfNull(e);
        var n = e.Count;
        switch (e.Kind)
        {
            case Kind.Archive or Kind.BoardArchive:
                return L10n.N("Archived %d message.", "Archived %d messages.", n);
            case Kind.Junk:
                // TRANSLATORS: the toast after messages went to the Junk
                // folder; its button takes it back.
                return L10n.N("Moved %d message to Junk.", "Moved %d messages to Junk.", n);
            case Kind.Move:
                if (e.TargetName.Length > 0)
                {
                    // TRANSLATORS: the toast after Move To; %d is the number
                    // of messages, %s the name of the folder they went to.
                    return L10n.N("Moved %d message to “%s”.", "Moved %d messages to “%s”.", n, n, e.TargetName);
                }
                // TRANSLATORS: the toast after Move To, when the folder has no name.
                return L10n.N("Moved %d message.", "Moved %d messages.", n);
            default:
                return L10n.N("Moved %d message to Trash.", "Moved %d messages to Trash.", n);
        }
    }

    /// <summary>
    /// undo.Button: the button of the toast that takes the move back, with
    /// the keyboard shortcut that does the same (<paramref name="shortcut"/>:
    /// "Ctrl+Z"; never translated); without a shortcut just Undo.
    /// </summary>
    public static string Button(string shortcut)
    {
        if (string.IsNullOrEmpty(shortcut))
        {
            // TRANSLATORS: the button of a toast that takes back what was
            // just done (an archive).
            return L10n.T("Undo");
        }
        // TRANSLATORS: the button of the toast after a message was moved (to
        // Trash, the archive, another folder); it moves it back. %s is the
        // keyboard shortcut that does the same, like Ctrl+Z.
        return L10n.T("Undo (%s)", shortcut);
    }

    /// <summary>undo.Undone: the toast after Undo took <paramref name="e"/> back.</summary>
    public static string Undone(Entry e)
    {
        ArgumentNullException.ThrowIfNull(e);
        // TRANSLATORS: the toast after Undo moved messages back to where
        // they were.
        return L10n.N("Moved %d message back.", "Moved %d messages back.", e.Count);
    }

    /// <summary>undo.Failed: the toast when Undo could not move the messages back.</summary>
    public static string Failed() =>
        // TRANSLATORS: Undo (Ctrl+Z) of a move to Trash or another folder
        // failed.
        L10n.T("Could not move the messages back.");

    /// <summary>undo.Nothing: the toast of Undo with nothing left to take back.</summary>
    public static string Nothing() =>
        // TRANSLATORS: Ctrl+Z when no move of mail is left to take back.
        L10n.T("Nothing to undo.");
}
