// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration+Board.swift (wireBoard,
// revealInMail, revealFailed) and the source's part of
// MainWindowController+Board.swift (startBoard, wireBoardSource,
// makeBoardActions, showInMail); GTK: window/board.go (newDataSource,
// boardChanged, boardAccountsChanged, boardConnectionChanged) and
// board_show_in_mail.go (showInMail, revealState, decideReveal,
// revealRowKeys, finish).
//
// The main window's board: its controller over the daemon's source, or
// over the invented samples when MALACHI_BOARD_SAMPLES=1 (read once, at
// start; the owner's development aid). The daemon's source starts the
// first time the board shows and then follows the daemon's notifications
// (notify.boardChanged, notify.accountsChanged) and the connection. The
// case actions reach the mail through here: Reply opens the ordinary reply
// (a comment on an issue tracker's account) for the case's message, and
// Show in Mail goes back to Mail, selects the message's current folder
// (message.get first: the case's own folder can be stale) and, once its
// first page is listed, the message's row, as a click would; whatever
// keeps the row from showing (the message gone, a folder the sidebar does
// not have, a search on screen, a listing that never finishes) opens the
// message in its own window instead. GTK polls the list's state for the
// listing, which this port does as well. A Show in Mail whose answer comes
// after the user moved on (left Mail for the board again, selected another
// case, clicked Show in Mail again) does nothing, not even its toast.
//
// The board remembers its style and account filter: every change of the
// style writes board-last-style, every change of the account filter
// board-account-filter (BoardController reads them back as it shows).

using System;
using System.Threading.Tasks;
using Malachi.App.Boards;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;

namespace Malachi.App.Shell;

/// <summary>The board of the Integration.</summary>
public sealed partial class Integration
{
    /// <summary>MALACHI_BOARD_SAMPLES: "1" shows the invented sample board instead of the daemon's.</summary>
    public const string BoardSamplesVariable = "MALACHI_BOARD_SAMPLES";

    // How long Show in Mail waits for the folder's first page, and how often
    // it looks (board_show_in_mail.go revealTimeout, revealPollInterval).
    private static readonly TimeSpan RevealTimeout = TimeSpan.FromSeconds(10);
    private static readonly TimeSpan RevealPoll = TimeSpan.FromMilliseconds(100);

    private DaemonBoardSource? boardDaemonSource;
    private bool boardStarted;
    private bool boardEnded;
    private PendingReveal? reveal;

    // Bumped by every Show in Mail, by LeftMail and by EndBoard: an answer
    // of message.get for an older one is dropped.
    private int revealGeneration;
    private DispatcherQueueTimer? revealTimer;
    private ILogger? boardLogger;

    /// <summary>The main window's board: its view state and view model.</summary>
    public BoardController BoardController { get; private set; } = null!;

    /// <summary>The board runs on the invented samples (MALACHI_BOARD_SAMPLES=1).</summary>
    public bool BoardSamples { get; private set; }

    // The daemon's source once started; null before and with the samples.
    private DaemonBoardSource? StartedBoardSource => boardStarted ? boardDaemonSource : null;

    /// <summary>Starts the daemon's board the first time the board shows; it keeps running from then on.</summary>
    public void StartBoard()
    {
        if (boardStarted || boardDaemonSource is not { } source)
        {
            return;
        }
        boardStarted = true;
        source.Start();
    }

    /// <summary>
    /// Lists the daemon's board again, once started (the suggested reply
    /// linked a draft: BoardReplyController.OnRefresh); nothing with the
    /// samples.
    /// </summary>
    public void RefreshBoard() => StartedBoardSource?.Refresh();

    /// <summary>The window leaves Mail: a Show in Mail still waiting selects nothing in the hidden panes.</summary>
    public void LeftMail()
    {
        revealGeneration++;
        CancelReveal();
    }

    // Dispose: a Show in Mail still waiting stops polling the disposed
    // list, and a message.get answering later does nothing.
    private void EndBoard()
    {
        boardEnded = true;
        revealGeneration++;
        CancelReveal();
    }

    /// <summary>The case actions of the main window's board, their ways into the mail installed.</summary>
    public BoardActions MakeBoardActions() => new(BoardController, BoardSamples)
    {
        OnReply = BoardReply,
        OnShowInMail = ShowInMail,
        CommentsOn = account => Mailbox.Model.Account(account)?.Can(Capability.Comment) == true,
        // As the compose window asks before it discards a message.
        ConfirmDiscard = () => state.Alerts.ConfirmDestructiveAsync(mainWindow, L10n.T("Discard this message?"), "", L10n.T("_Discard")),
    };

    // The controller over its source, and the notifications the daemon's
    // source follows once started (wireBoardSource).
    private void WireBoard()
    {
        boardLogger = state.Logs.CreateLogger<Integration>();
        BoardSamples = Environment.GetEnvironmentVariable(BoardSamplesVariable) == "1";
        IBoardSource source;
        if (BoardSamples)
        {
            source = InMemoryBoardSource.Dummy(samples: true, TimeProvider.System.GetUtcNow(), TimeZoneInfo.Local);
        }
        else
        {
            boardDaemonSource = new DaemonBoardSource(state.Client, logger: state.Logs.CreateLogger<DaemonBoardSource>());
            source = boardDaemonSource;
            tokens.Add(boardDaemonSource);
        }
        var settings = state.Settings;
        BoardController = new BoardController(
            source,
            defaultStyle: () => settings.BoardDefaultStyle,
            lastStyle: () => settings.BoardLastStyle,
            savedAccount: () => settings.BoardAccountFilter);
        BoardController.Changed += (_, changes) => RememberBoardView(changes);
        var hub = state.Notifications;
        tokens.Add(hub.AddBoardChanged(n => StartedBoardSource?.BoardChanged(n)));
        tokens.Add(hub.AddAccountsChanged(() => StartedBoardSource?.AccountsChanged()));
        tokens.Add(hub.AddConnectionState(s =>
            StartedBoardSource?.ConnectionChanged(s is ConnectionState.Connected or ConnectionState.InfoFailed)));
    }

    // The style and the account filter, for the next show and the next
    // launch (window/board.go rememberViewState). The filter is written only
    // once the accounts are known, so that the saved one is not lost before
    // the board could apply it.
    private void RememberBoardView(BoardController.Changes changes)
    {
        var s = state.Settings;
        var view = BoardController.State;
        if ((changes & BoardController.Changes.Style) != 0 && s.BoardLastStyle != view.Style)
        {
            s.BoardLastStyle = view.Style;
        }
        if ((changes & BoardController.Changes.Filters) != 0 && BoardController.Source.Snapshot.Accounts.Count > 0)
        {
            var account = view.Account?.Value ?? "";
            if (!string.Equals(s.BoardAccountFilter, account, StringComparison.Ordinal))
            {
                s.BoardAccountFilter = account;
            }
        }
    }

    // Reply: the ordinary reply window for the case's message (a comment
    // on an issue tracker's account, as ActionsController decides); the
    // message is looked up first when no list holds it.
    private void BoardReply(Board.Case c)
    {
        if (c.Reply is not { } target)
        {
            return;
        }
        Cache.LookUp(c.Account, target.Message, s =>
        {
            if (s is null)
            {
                mainWindow.Toasts.Show(Board.Text.ShowInMailFailed);
                return;
            }
            Actions.OpenCompose(ComposeKind.Reply, s.Id);
        });
    }

    // Show in Mail: the reply target, else the newest message.
    private void ShowInMail(Board.Case c)
    {
        if ((c.Reply?.Message ?? c.LatestMessage) is not { } message)
        {
            return;
        }
        var generation = ++revealGeneration;
        _ = ShowInMailAsync(c.Account, message, c.Reply?.Folder, c.Thread, generation, BoardController.State.Selection);
    }

    // Whether the answer of Show in Mail number generation still counts: no
    // newer one, Mail not left, the board not ended, the same case selected.
    private bool RevealCurrent(int generation, BoardCaseId? selection) =>
        !boardEnded && generation == revealGeneration && BoardController.State.Selection == selection;

    private async Task ShowInMailAsync(
        AccountId account, MessageId message, FolderId? fallbackFolder, ThreadId? thread, int generation, BoardCaseId? selection)
    {
        MessageGetResult result;
        try
        {
            result = await state.Client.CallAsync(API.MessageGet, new MessageGetParams { AccountId = account, MessageId = message });
        }
        catch (RpcException e) when (e.Error.Code.Value is ErrorCode.MessageNotFound or ErrorCode.MessageGone)
        {
            if (RevealCurrent(generation, selection))
            {
                mainWindow.Toasts.Show(Board.Text.ShowInMailGone);
            }
            return;
        }
        catch (Exception e) when (e is RpcException or RpcClientException or TimeoutException or OperationCanceledException)
        {
            if (!boardEnded)
            {
                LogShowInMailFailed(boardLogger!, e.GetType().Name);
            }
            if (RevealCurrent(generation, selection))
            {
                mainWindow.Toasts.Show(Board.Text.ShowInMailFailed);
            }
            return;
        }
        if (!RevealCurrent(generation, selection))
        {
            return;
        }
        var summary = result.Message.Summary;
        var folder = summary.FolderId.Value.Length > 0 ? summary.FolderId : fallbackFolder ?? summary.FolderId;
        StartReveal(new FolderKey(account, folder), summary, summary.ThreadId ?? thread);
    }

    // revealState.start: Mail shows; the folder is selected and its first
    // page waited for, or the message opens in its own window at once when
    // Mail cannot show it.
    private void StartReveal(FolderKey folder, MessageSummary message, ThreadId? thread)
    {
        CancelReveal();
        mainWindow.SetMode(Board.Mode.Mail);
        if (mainWindow.Mode != Board.Mode.Mail)
        {
            return;
        }
        if (List.SearchActive || Mailbox.Model.Folder(folder) is null)
        {
            OpenRevealed(message);
            return;
        }
        Mailbox.SelectFolder(folder, fav: false);
        reveal = new PendingReveal(folder, message, thread, Environment.TickCount64 + (long)RevealTimeout.TotalMilliseconds);
        if (!Tick())
        {
            return;
        }
        revealTimer = state.Dispatcher.CreateTimer();
        revealTimer.Interval = RevealPoll;
        revealTimer.Tick += (_, _) =>
        {
            if (!Tick())
            {
                StopRevealTimer();
            }
        };
        revealTimer.Start();
    }

    // One look at the listing (revealState.tick); true while it still waits.
    private bool Tick()
    {
        if (reveal is not { } r)
        {
            return false;
        }
        var timedOut = Environment.TickCount64 >= r.Deadline;
        var model = Mailbox.Model;
        if (model.Search.Active || model.ListFolder != r.Folder)
        {
            // The user went elsewhere before the listing answered.
            reveal = null;
            return false;
        }
        if (model.Loading && !timedOut)
        {
            return true;
        }
        reveal = null;
        FinishReveal(r);
        return false;
    }

    // revealState.finish: the message's row (flat, a conversation's member,
    // or its conversation's) selected with the keyboard, else its window.
    private void FinishReveal(PendingReveal r)
    {
        var id = r.Message.Id;
        ListKey[] keys = r.Thread is { } tid
            ? [new ListKey(Message: id), new ListKey(tid, id), new ListKey(tid)]
            : [new ListKey(Message: id)];
        foreach (var key in keys)
        {
            if (List.RevealRow(key))
            {
                mainWindow.ShowRevealedMessage();
                return;
            }
        }
        OpenRevealed(r.Message);
    }

    private void OpenRevealed(MessageSummary message)
    {
        if (Reader is { } reader)
        {
            reader.Services.Windows.OpenMessage(message);
        }
    }

    private void CancelReveal()
    {
        reveal = null;
        StopRevealTimer();
    }

    private void StopRevealTimer()
    {
        revealTimer?.Stop();
        revealTimer = null;
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.get (show in mail): {Error}")]
    private static partial void LogShowInMailFailed(ILogger logger, string error);

    // Show in Mail's request waiting for the folder's listing (GTK revealState).
    private sealed record PendingReveal(FolderKey Folder, MessageSummary Message, ThreadId? Thread, long Deadline);
}
