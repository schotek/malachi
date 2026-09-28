// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Actions/MessageActionsController.swift
// (the MessageActions half: the main window's actions on the selection);
// GTK: ui/internal/window/window.go (registerActions: forSelected and
// forRows; the reply buttons and the star's clicked handler). The main
// window's per-message commands (the header bar, the context menu, Delete,
// A, J, U, S) act on the list's selection: the reply and image actions on
// the selected row's message (a conversation row's newest folder member),
// the flag and move actions on every message the row stands for (all the
// folder members of a conversation row, fetched first when they are not
// known yet). Everything else is the ActionsController's; a confirmation
// goes on the main window.

using System;
using Malachi.App.Commands;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;

namespace Malachi.App.Main;

/// <summary>The main window's actions on the selected row.</summary>
internal sealed class SelectionActions
{
    private readonly ActionsController actions;
    private readonly ListController list;
    private readonly Func<Window?> window;

    /// <param name="actions">The per-message actions.</param>
    /// <param name="list">The list half, whose selection they act on.</param>
    /// <param name="window">The window a confirmation goes on.</param>
    public SelectionActions(ActionsController actions, ListController list, Func<Window?> window)
    {
        this.actions = actions;
        this.list = list;
        this.window = window;
    }

    /// <summary>Sets the handlers of the per-message commands of <paramref name="commands"/>; their CanExecute stays the flags'.</summary>
    public void Install(WindowCommands commands)
    {
        ArgumentNullException.ThrowIfNull(commands);
        commands.Reply.Handler = () => ForSelected(id => actions.OpenCompose(ComposeKind.Reply, id));
        commands.ReplyAll.Handler = () => ForSelected(id => actions.OpenCompose(ComposeKind.ReplyAll, id));
        // "Forward Without Attachments?" goes on the main window.
        commands.Forward.Handler = () => ForSelected(id => actions.OpenCompose(ComposeKind.Forward, id, window()));
        commands.Trash.Handler = () => list.SelectedIds((row, ids) => actions.Trash(ids, ListController.RowSubject(row), window()));
        commands.Junk.Handler = () => list.SelectedIds((row, ids) => actions.Junk(ids, ListController.RowSubject(row), window()));
        commands.Archive.Handler = () => list.SelectedIds((_, ids) => actions.Archive(ids));
        // On a conversation row the star acts on every member (flagTarget).
        commands.ToggleFlag.Handler = () => list.SelectedIds((row, ids) => actions.SetFlagged(ids, ListController.FlagTarget(row)));
        commands.MarkRead.Handler = () => list.SelectedIds((_, ids) => actions.SetSeen(ids, true));
        commands.MarkUnread.Handler = () => list.SelectedIds((_, ids) => actions.SetSeen(ids, false));
        commands.LoadImages.Handler = () => ForSelected(actions.LoadImages);
        commands.TrustSender.Handler = () => ForSelected(actions.TrustSender);
    }

    // window.go forSelected: the selected row's message.
    private void ForSelected(Action<MessageId> fn)
    {
        if (list.SelectedRow?.Message is { } s)
        {
            fn(s.Id);
        }
    }
}
