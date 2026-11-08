// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the print part of macos/Sources/MalachiMail/MainWindow/
// MainWindowController.swift (printMail: and its validation) and of
// MessageActionsController's printSelected and printCalendar; GTK: the GTK
// port follows (docs/plans/toolbar-customization.md §8).
//
// Print (Ctrl+P, ShortcutCommand.Print; the toolbar's Print item of the
// toolbar customization binds to the same command): in Mail the
// selection (one message, a conversation row as the conversation oldest
// first, several rows each on a page, at most Print.MaxMessages), in the
// Calendar the selected event, else the day the view stands on (P8), on
// the Board nothing. More Actions and the message list's context menu
// have it too (an addition to P4, until the
// toolbar's item); never the primary menu "…" (PR #20).

using Malachi.App.Printing;
using Malachi.App.Reader;
using Malachi.Core.Boards;

namespace Malachi.App;

public sealed partial class MainWindow
{
    /// <summary>
    /// Wires Print to the reader's <paramref name="services"/> (its cache,
    /// and the router of the list's selection); ReaderHub calls it once.
    /// </summary>
    internal void InitializePrint(ReaderServices services)
    {
        var mailAllows = Commands.Print.CanExecute;
        Commands.Print.Gate = () => mode != Board.Mode.Board;
        Commands.Print.CanExecute = () => mode == Board.Mode.Calendar
            ? calendarController is not null
            : mailAllows?.Invoke() ?? false;
        Commands.Print.Handler = () =>
        {
            switch (mode)
            {
                case Board.Mode.Mail:
                    services.Router.PrintSelection((messages, layout) => PrintJob.Messages(this, services, messages, layout));
                    break;
                case Board.Mode.Calendar:
                    if (calendarController is { } calendar)
                    {
                        PrintJob.Calendar(this, services, calendar);
                    }
                    break;
                case Board.Mode.Board:
                default:
                    break;
            }
        };
    }
}
