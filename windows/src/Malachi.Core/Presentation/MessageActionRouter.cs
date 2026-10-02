// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Actions/MessageActionsController.swift
// (the MessageActions of the selection and the MessageActionDelegate of one
// message: flags, reply, replyAll, forward, trash, junk, archive,
// toggleFlag, markRead, markUnread, loadImages, trustSender,
// downloadPictures, retryOutbox, isDraft, editDraft, newMessage); GTK: the
// win.* actions of window.go registerActions acting on the selected row,
// and the msg.* actions of message_window.go acting on the window's
// message. Everything that touches the model or the daemon is the
// ActionsController's; this only resolves the selection (a conversation
// row's members, its subject for the confirmation, its star's target) and
// the window a confirmation goes on. The links and attachments, which
// macOS handles here too, are LinkOpener's and AttachmentOpener's.
// UI-thread-affine.

using System;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>The per-message actions of the main window (the selection) and of a message window (its message).</summary>
public sealed class MessageActionRouter
{
    private readonly ActionsController actions;
    private readonly IListSelection list;

    /// <param name="actions">The actions.</param>
    /// <param name="list">The message list's selection.</param>
    public MessageActionRouter(ActionsController actions, IListSelection list)
    {
        ArgumentNullException.ThrowIfNull(actions);
        ArgumentNullException.ThrowIfNull(list);
        this.actions = actions;
        this.list = list;
    }

    /// <summary>The main window, over which the selection's confirmations go (null: the alerts' own choice).</summary>
    public Func<object?>? MainWindow { get; set; }

    /// <summary>Opens a compose window (an address chip's New Message).</summary>
    public Action<ComposeParams>? Compose { get; set; }

    // The selection (window.go registerActions)

    /// <summary>What the main window's per-message commands allow now (actions.go <c>setMessageActionsSensitive</c>).</summary>
    public ActionFlags Flags => actions.ActionFlagsFor(list.SelectedRow);

    /// <summary>Reply to the selected row's message (a conversation row's newest folder member).</summary>
    public void Reply() => ForSelected(id => actions.OpenCompose(ComposeKind.Reply, id));

    /// <summary>Reply All to the selected row's message.</summary>
    public void ReplyAll() => ForSelected(id => actions.OpenCompose(ComposeKind.ReplyAll, id));

    /// <summary>
    /// Forward the selected row's message; "Forward Without Attachments?"
    /// goes on the main window.
    /// </summary>
    public void Forward() => ForSelected(id => actions.OpenCompose(ComposeKind.Forward, id, MainWindow?.Invoke()));

    /// <summary>win.trash: every message of the selected row, confirmed over the main window when the setting asks.</summary>
    public void Trash() => list.SelectedIds((row, ids) => actions.Trash(ids, ListController.RowSubject(row), MainWindow?.Invoke()));

    /// <summary>win.junk: every message of the selected row, always confirmed.</summary>
    public void Junk() => list.SelectedIds((row, ids) => actions.Junk(ids, ListController.RowSubject(row), MainWindow?.Invoke()));

    /// <summary>win.archive.</summary>
    public void Archive() => list.SelectedIds((_, ids) => actions.Archive(ids));

    /// <summary>win.toggle-flag: a conversation row stars every member, or unstars them when any is starred (<c>flagTarget</c>).</summary>
    public void ToggleFlag() => list.SelectedIds((row, ids) => actions.SetFlagged(ids, ListController.FlagTarget(row)));

    /// <summary>win.mark-read.</summary>
    public void MarkRead() => list.SelectedIds((_, ids) => actions.SetSeen(ids, true));

    /// <summary>win.mark-unread.</summary>
    public void MarkUnread() => list.SelectedIds((_, ids) => actions.SetSeen(ids, false));

    /// <summary>win.load-images, for the message the pane shows.</summary>
    public void LoadImages() => ForSelected(actions.LoadImages);

    /// <summary>win.trust-sender, for the message the pane shows.</summary>
    public void TrustSender() => ForSelected(actions.TrustSender);

    // One message (message_window.go, the msg.* group; the pane's banners and bar)

    /// <summary>
    /// What a message window's commands allow: from the message's live
    /// summary, not the one the window was opened with (the star and Mark as
    /// Read follow the model).
    /// </summary>
    public ActionFlags FlagsFor(MessageSummary summary)
    {
        ArgumentNullException.ThrowIfNull(summary);
        return actions.FlagsFor(actions.Summary(summary.Id) ?? summary);
    }

    /// <summary>msg.reply.</summary>
    public void Reply(MessageId id) => actions.OpenCompose(ComposeKind.Reply, id);

    /// <summary>msg.reply-all.</summary>
    public void ReplyAll(MessageId id) => actions.OpenCompose(ComposeKind.ReplyAll, id);

    /// <summary>
    /// msg.forward (compose_open.go <c>openComposeFrom</c>):
    /// <paramref name="window"/> receives "Forward Without Attachments?" when
    /// the download of the attachments fails.
    /// </summary>
    public void Forward(MessageId id, object? window) => actions.OpenCompose(ComposeKind.Forward, id, window);

    /// <summary>msg.trash, confirmed over <paramref name="window"/>; Cancel Sending for an outbox message.</summary>
    public void Trash(MessageId id, object? window) => actions.Trash(id, window);

    /// <summary>msg.junk, confirmed over <paramref name="window"/>.</summary>
    public void Junk(MessageId id, object? window) => actions.Junk(id, window);

    /// <summary>msg.archive.</summary>
    public void Archive(MessageId id) => actions.Archive([id]);

    /// <summary>msg.toggle-flag.</summary>
    public void ToggleFlag(MessageId id) => actions.ToggleFlagged(id);

    /// <summary>msg.mark-read.</summary>
    public void MarkRead(MessageId id) => actions.MarkRead(id);

    /// <summary>msg.mark-unread.</summary>
    public void MarkUnread(MessageId id) => actions.MarkUnread(id);

    /// <summary>The bar's Load Images and msg.load-images.</summary>
    public void LoadImages(MessageId id) => actions.LoadImages(id);

    /// <summary>The bar's Always From This Sender and msg.trust-sender.</summary>
    public void TrustSender(MessageId id) => actions.TrustSender(id);

    /// <summary>
    /// The pictures bar's Download Pictures (remote.go
    /// <c>downloadPictures</c>); a failure is said through
    /// <paramref name="say"/>, in the window the click came from.
    /// </summary>
    public void DownloadPictures(MessageId id, Action<string>? say) => actions.DownloadPictures(id, say);

    /// <summary>The outbox banner's Retry.</summary>
    public void RetryOutbox(MessageId id) => actions.RetryOutbox(id);

    /// <summary>Whether <paramref name="summary"/> is a message of a Drafts folder (the draft banner).</summary>
    public bool IsDraft(MessageSummary summary)
    {
        ArgumentNullException.ThrowIfNull(summary);
        return actions.Mailbox.Model.InDrafts(summary);
    }

    /// <summary>
    /// The role of the folder <paramref name="summary"/> lies in (the bulk
    /// strip changes in the junk folder; window/bulk.go <c>bulkStripFor</c>);
    /// <see cref="FolderRole.None"/> for a folder the model does not know.
    /// </summary>
    public FolderRole FolderRoleOf(MessageSummary summary)
    {
        ArgumentNullException.ThrowIfNull(summary);
        return actions.Mailbox.Model.FolderRole(new FolderKey(summary.AccountId, summary.FolderId));
    }

    /// <summary>The draft banner's Edit (drafts.go <c>openDraft</c>).</summary>
    public void EditDraft(MessageId id) => actions.OpenDraft(id);

    /// <summary>
    /// A double click on the header of a conversation's card (message_view.go
    /// <c>openMessage</c>): the message opens as one on its row in the list
    /// does, in a window of its own, a draft in the compose window.
    /// </summary>
    public void OpenMessage(MessageSummary summary) => actions.OpenMessage(summary);

    /// <summary>
    /// An address chip's New Message (addresses.go <c>chip</c>): a new
    /// message to <paramref name="to"/>, from <paramref name="account"/>, the
    /// account of the message the chip sits on.
    /// </summary>
    public void NewMessage(Address to, AccountId account)
    {
        ArgumentNullException.ThrowIfNull(to);
        Compose?.Invoke(new ComposeParams { Kind = ComposeKind.New, AccountId = account, To = [to] });
    }

    // Runs fn on the selected row's message (a conversation row's newest
    // folder member; window.go selectedMessage).
    private void ForSelected(Action<MessageId> fn)
    {
        if (list.SelectedRow?.Message is { } s)
        {
            fn(s.Id);
        }
    }
}
