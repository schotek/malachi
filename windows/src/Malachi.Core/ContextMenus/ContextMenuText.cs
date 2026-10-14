// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/ContextMenu/ContextMenuText.swift; GTK:
// ui/internal/contextmenu/text.go (Labels, Label, Question, EmptyQuestion,
// MovingText, MarkingReadText, EmptyingText).
//
// The texts of the context menus. Go's Translator is L10n here: every text
// goes through it with the GTK msgid. Some labels carry the GTK mnemonic
// underscore, as the same items do in the window's other menus; the views
// show it as an access key (MnemonicLabel).

using Malachi.Core.I18n;

namespace Malachi.Core.ContextMenus;

public static partial class ContextMenu
{
    /// <summary>contextmenu.Labels: the states that choose an item's label.</summary>
    public readonly record struct Labels
    {
        /// <summary>Reply is Comment (an issue tracker's account).</summary>
        public bool Comment { get; init; }

        /// <summary>ToggleFlag is Unstar.</summary>
        public bool Flagged { get; init; }

        /// <summary>Trash is Cancel Sending.</summary>
        public bool Outbox { get; init; }

        /// <summary>EmptyFolder is Empty Junk.</summary>
        public bool Junk { get; init; }

        /// <summary>Favourite is Remove from Favourites.</summary>
        public bool Favourite { get; init; }

        /// <summary>Fold is Expand.</summary>
        public bool Collapsed { get; init; }
    }

    /// <summary>
    /// contextmenu.Label: an item's text, most of them the msgids of the
    /// window's other controls; "" for a value that is no item.
    /// </summary>
    public static string Label(ItemId id, Labels l) => id switch
    {
        // TRANSLATORS: In the menu of a message in the list: opens it in a window of its own.
        ItemId.OpenMessage => L10n.T("Open in New Window"),
        ItemId.EditDraft => L10n.T("Edit"),
        ItemId.Reply => l.Comment ? L10n.T("Comment") : L10n.T("Reply"),
        ItemId.ReplyAll => L10n.T("Reply All"),
        ItemId.Forward => L10n.T("Forward"),
        ItemId.MarkRead => L10n.T("Mark as _Read"),
        ItemId.MarkUnread => L10n.T("Mark as _Unread"),
        ItemId.ToggleFlag => l.Flagged ? L10n.T("Unstar") : L10n.T("Star"),
        ItemId.ChangeStatus => L10n.T("Change Status"),
        ItemId.Archive => L10n.T("Archive"),
        ItemId.MoveTo => L10n.T("Move To"),
        ItemId.Junk => L10n.T("Mark as Junk"),
        ItemId.Trash => l.Outbox ? L10n.T("Cancel Sending") : L10n.T("Move to Trash"),
        ItemId.Assistant => L10n.T("Assistant"),
        ItemId.CheckMail => L10n.T("Check for New Mail"),
        // TRANSLATORS: In the menu of a folder in the sidebar.
        ItemId.MarkAllRead => L10n.T("Mark All as Read"),
        // TRANSLATORS: In the menu of the Trash or the Junk folder: deletes its messages for good.
        ItemId.EmptyFolder => l.Junk ? L10n.T("Empty Junk") : L10n.T("Empty Trash"),
        ItemId.Favourite => l.Favourite ? L10n.T("Remove from Favourites") : L10n.T("Add to Favourites"),
        ItemId.Fold => l.Collapsed ? L10n.T("Expand") : L10n.T("Collapse"),
        _ => "",
    };

    /// <summary>contextmenu.Question: a confirmation.</summary>
    /// <param name="Heading">Its heading.</param>
    /// <param name="Body">Its text.</param>
    /// <param name="Confirm">The label of the button that goes ahead, with its GTK mnemonic.</param>
    public sealed record Question(string Heading, string Body, string Confirm);

    /// <summary>
    /// contextmenu.EmptyQuestion: asked before EmptyFolder deletes the
    /// <paramref name="n"/> messages the folder shows (<c>folder.empty</c>):
    /// for good, and only what Malachi Mail keeps, so it says that older
    /// messages kept on the server only stay there.
    /// </summary>
    public static Question EmptyQuestion(bool junk, int n) => new(
        // TRANSLATORS: The heading of the question before the Trash (the Junk folder) is emptied.
        junk ? L10n.T("Empty the Junk Folder?") : L10n.T("Empty the Trash?"),
        // TRANSLATORS: %d is the number of messages the folder shows.
        L10n.N(
            "%d message will be deleted for good. Older messages that are only on the mail server stay there.",
            "%d messages will be deleted for good. Older messages that are only on the mail server stay there.",
            n),
        // TRANSLATORS: The button that empties the Trash or the Junk folder.
        L10n.T("_Empty"));

    /// <summary>contextmenu.MovingText: the action of Move To in an error toast ("… failed").</summary>
    public static string MovingText(int n) => n == 1
        // TRANSLATORS: What failed when a message could not be moved to another folder.
        ? L10n.T("Moving the message")
        // TRANSLATORS: What failed when messages could not be moved to another folder; %d is their number.
        : L10n.N("Moving %d message", "Moving %d messages", n);

    /// <summary>contextmenu.MarkingReadText: the action of Mark All as Read in an error toast.</summary>
    public static string MarkingReadText() =>
        // TRANSLATORS: What failed when a folder could not be marked read.
        L10n.T("Marking the folder as read");

    /// <summary>contextmenu.EmptyingText: the action of EmptyFolder in an error toast.</summary>
    public static string EmptyingText(bool junk) => junk
        // TRANSLATORS: What failed when the Junk folder could not be emptied.
        ? L10n.T("Emptying the Junk folder")
        // TRANSLATORS: What failed when the Trash could not be emptied.
        : L10n.T("Emptying the Trash");
}
