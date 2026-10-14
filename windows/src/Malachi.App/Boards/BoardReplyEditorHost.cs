// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardReplyEditorHost.swift
// (BoardReplyEditorHost: slot, retry, editsInline, focusReply, update,
// suspend, resume, prepare, adopted, makePane, release, finishAll,
// resumeAll, focus, keyboardInLivePane, ComposePaneHost, BoardReplyPane);
// GTK: window/board_reply_editor.go (initBoardReplies, updateBoardReplies,
// makeBoardReply, releaseBoardReply, endBoardReply,
// restoreBoardReplyFocus, focusBoardReply, focusBoardPane,
// revealBoardReply, suspendBoardReplies, FinishBoardReplies,
// ResumeBoardReplies, CloseBoardReplies).
//
// The board's inline reply editor for the main window, the WinUI side: it
// makes the ComposePanes (Inline, owner Board) that Core's BoardReplyPanes
// asks for, gives the live one's view to the detail's reply slot
// (BoardReplySlot) and forwards the panes' ends to Core. Every decision about
// a pane — when it is made, retired, saved, kept, closed or abandoned — is
// Core's (BoardReplyPanes, tested there). Made only over the daemon's board:
// the invented samples have no draft behind them and keep their static
// block (BoardSampleReply).
//
// Windows: there is one detail view, which the page moves between the
// List's pane and the panel over Columns and Today, so the live pane moves
// with it and no slot shows "elsewhere". The pane registers with the compose
// controller through a handle (IComposeWindowHandle) for the account list;
// the handle's quit save is the default (nothing), the board's own quit step
// settles it first. Ctrl+Enter and Ctrl+S while the editor's WebView2 has
// the keyboard reach the main window's router, which runs Send and Save
// Draft of the live pane (EditorPane); with the keyboard elsewhere in the
// pane its own scoped accelerators run them.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Compose;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.Foundation;

namespace Malachi.App.Boards;

/// <summary>The board's inline reply editor of the main window.</summary>
public sealed class BoardReplyEditorHost : IBoardInlineReply, IComposePaneHost, IDisposable
{
    // How often, and how many times, the keyboard waits for an editor that is
    // not ready yet (Swift and GTK: 30 × 0.1 s).
    private const int FocusTries = 30;
    private static readonly TimeSpan FocusRetry = TimeSpan.FromMilliseconds(100);

    private readonly BoardActions actions;
    private readonly BoardDetailView detail;
    private readonly AppState state;
    private readonly ComposeController composer;
    private readonly IToasts toasts;
    private readonly Func<Window?> window;
    private readonly BoardReplyEditorController loader;
    private readonly BoardReplyPanes panes;
    private readonly Dictionary<ComposePane, PaneHandle> handles = [];
    private readonly Dictionary<ComposePane, double> heights = [];

    // Reply asked for the editor of this case before its pane existed.
    private BoardCaseId? focusPending;

    // The page is out of sight (Mail mode, a hidden window, quitting).
    private bool suspended;
    private bool closed;

    /// <summary>The host of the main window's board.</summary>
    /// <param name="actions">The case actions (the controller, Discard's way).</param>
    /// <param name="detail">The one detail view; its reply slot shows the panes.</param>
    /// <param name="state">The application (the client, the settings, the alerts).</param>
    /// <param name="composer">The compose controller (the account list).</param>
    /// <param name="toasts">The window's toasts.</param>
    /// <param name="window">The main window (the panes' dialogs).</param>
    public BoardReplyEditorHost(
        BoardActions actions, BoardDetailView detail, AppState state, ComposeController composer, IToasts toasts, Func<Window?> window)
    {
        ArgumentNullException.ThrowIfNull(actions);
        ArgumentNullException.ThrowIfNull(detail);
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(composer);
        ArgumentNullException.ThrowIfNull(toasts);
        ArgumentNullException.ThrowIfNull(window);
        this.actions = actions;
        this.detail = detail;
        this.state = state;
        this.composer = composer;
        this.toasts = toasts;
        this.window = window;
        loader = new BoardReplyEditorController(state.Client, state.Logs.CreateLogger<BoardReplyEditorController>());
        panes = new BoardReplyPanes(loader, logger: state.Logs.CreateLogger<BoardReplyPanes>())
        {
            Make = MakePane,
            Detach = Release,
            OnToast = toasts.Show,
            OnChange = Changed,
            OnAdopt = Adopted,
        };
        Slot = new BoardReplySlot(this, actions, state.BoardReply);
        Slot.KeyboardLost += (_, _) => detail.FocusReplyButton();
        detail.ViewportChanged += (_, _) => ApplyVisibleHeight();
        // A live pane keeps its case selected across a style switch or a
        // narrowing (BoardController.KeepsSelection; Swift paneIsLive, GTK
        // SetPaneLive).
        actions.Controller.PaneLive = id => !closed && panes.LiveKey is { } key && key.CaseId == id;
    }

    /// <summary>The detail's reply part (BoardDetailView.ReplyPart).</summary>
    public BoardReplySlot Slot { get; }

    /// <summary>The live pane, when its editor's page has the keyboard (the window's Ctrl+Enter and Ctrl+S).</summary>
    public ComposePane? EditorPane =>
        !suspended && panes.Live is PaneHandle h && h.Pane.EditorHasFocus() ? h.Pane : null;

    /// <inheritdoc/>
    Window? IComposePaneHost.HostWindow => window();

    /// <summary>Whether the keyboard is in the live pane (its editor, its recipient fields): the page's Escape then goes to the state pill.</summary>
    internal bool KeyboardInPane => !suspended && !closed && panes.Live is PaneHandle h && h.Pane.KeyboardInside;

    /// <summary>Whether a popup of the live pane is open (the recipients' suggestions, a menu of its bar): Escape closes that first.</summary>
    internal bool PanePopupOpen => !suspended && !closed && panes.Live is PaneHandle h && h.Pane.PopupOpen;

    /// <summary>Whether a pane still holds text that is not saved (GTK <c>Panes.HasUnsaved</c>), for the quit question.</summary>
    internal bool HasUnsaved => !closed && panes.Panes.Any(p => p.HasUnsavedText);

    /// <summary>Whether a reply the user sent waits for the send to answer (GTK <c>Panes.HasSending</c>), for the quit question.</summary>
    internal bool HasSending => !closed && panes.Panes.Any(p => p.IsSending);

    /// <summary>Whether the panes are out of sight (Mail mode, a hidden window, quitting).</summary>
    internal bool Suspended => suspended;

    /// <summary>What the reply slot of case <paramref name="id"/> shows.</summary>
    internal BoardReplyPanes.Slot SlotFor(BoardCaseId id) => suspended || closed ? new BoardReplyPanes.Slot.None() : panes.SlotFor(id);

    /// <summary>The slot's Try Again after the draft could not be opened.</summary>
    internal void Retry() => loader.Retry();

    /// <summary>The live pane's view, for the slot.</summary>
    internal static ComposePane View(IBoardReplyPane pane) => ((PaneHandle)pane).Pane;

    // Following the board

    /// <summary>
    /// The board changed (the slot calls this before it renders): Core
    /// hears which case is selected.
    /// </summary>
    internal void Update()
    {
        if (suspended || closed)
        {
            return;
        }
        var selected = detail.Shown is { } d ? actions.Case(d.Id) : null;
        if (focusPending is { } pending && pending != selected?.Id)
        {
            focusPending = null;
        }
        panes.Show(selected);
    }

    /// <summary>
    /// The board left sight (Mail mode, a hidden window): the live pane is
    /// retired (saved) and nothing loads until <see cref="Resume"/>.
    /// </summary>
    public void Suspend()
    {
        if (suspended || closed)
        {
            return;
        }
        suspended = true;
        focusPending = null;
        panes.Suspend();
        Slot.Render();
    }

    /// <summary>The board is in sight again: the selected case's reply loads again.</summary>
    public void Resume()
    {
        if (!suspended || closed)
        {
            return;
        }
        suspended = false;
        panes.Resume();
        Update();
        Slot.Render();
    }

    /// <summary>
    /// The application quits: every pane settles, at most
    /// <paramref name="wait"/>, while the connection still stands. False when
    /// a reply could not be saved or sent in that time (the caller asks
    /// before quitting, and <see cref="Resume"/>s when the user stays).
    /// </summary>
    public async Task<bool> FinishAllAsync(TimeSpan wait)
    {
        if (closed)
        {
            return true;
        }
        suspended = true;
        focusPending = null;
        var ok = await panes.FinishAllAsync(wait);
        Slot.Render();
        return ok;
    }

    /// <summary>The window is gone for good: the panes still held are abandoned (GTK CloseBoardReplies).</summary>
    public void Dispose()
    {
        if (closed)
        {
            return;
        }
        closed = true;
        suspended = true;
        Slot.Close();
        panes.OnChange = null;
        panes.OnToast = null;
        panes.OnAdopt = null;
        var held = panes.Panes;
        panes.Dispose();
        foreach (var pane in held)
        {
            pane.Abandon();
            Release(pane);
        }
        loader.Dispose();
    }

    /// <summary>
    /// The detail moved between the list and the panel while the live pane
    /// had the keyboard (BoardPage.PlaceDetail): the keyboard goes back to
    /// its editor where the caret was, not to the state pill.
    /// </summary>
    internal void RefocusLive()
    {
        if (!closed && !suspended && panes.Live is PaneHandle h && h.Pane.XamlRoot is not null)
        {
            h.Pane.FocusEditor();
        }
    }

    // IBoardInlineReply

    /// <inheritdoc/>
    public bool EditsInline(BoardCaseId id) => !closed && actions.Case(id)?.Draft is not null;

    /// <inheritdoc/>
    public void FocusReply(BoardCaseId id)
    {
        if (closed)
        {
            return;
        }
        if (actions.Controller.State.Selection != id)
        {
            actions.Controller.Select(id);
        }
        if (panes.Live is PaneHandle live && panes.SlotFor(id) is BoardReplyPanes.Slot.Editor e && ReferenceEquals(e.Pane, live))
        {
            Focus(live.Pane, 0);
            return;
        }
        focusPending = id;
    }

    // What Core's rules ask

    private PaneHandle? MakePane(BoardReplyEditorController.Key key, ComposeParams p)
    {
        if (closed)
        {
            return null;
        }
        var pane = new ComposePane(state, composer, p, new ComposePane.Options
        {
            Layout = ComposePane.Layout.Inline,
            Owner = DraftOwner.Board,
        });
        var handle = new PaneHandle(pane);
        handles[pane] = handle;
        pane.Host = this;
        // Discard goes the board's way (after the draft controller's own
        // question): the draft it edits goes, with the case's link while the
        // case has it; a refusal keeps the pane and its text.
        var caseId = key.CaseId;
        pane.DiscardStored = async (account, draft) =>
        {
            panes.Discarding(handle, true);
            try
            {
                await actions.Controller.DiscardDraftAsync(caseId, draft, account);
            }
            catch
            {
                panes.Discarding(handle, false);
                throw;
            }
        };
        pane.SendFailed += (_, _) => panes.SendFailed(handle);
        // Escape in the editor's page (its bridge; the pane's popups first):
        // the keyboard goes to the state pill, nothing closes
        // (Board.EscapeFor's second step).
        pane.EscapePressed += (_, _) =>
        {
            if (!closed && !suspended && ReferenceEquals((panes.Live as PaneHandle)?.Pane, pane))
            {
                detail.FocusContent();
            }
        };
        pane.SetVisibleHeight(detail.Scroller.ViewportHeight);
        composer.Register(handle);
        return handle;
    }

    // Core is done with the pane: it leaves the composer and the slot.
    private void Release(IBoardReplyPane released)
    {
        if (released is not PaneHandle handle)
        {
            return;
        }
        var pane = handle.Pane;
        var hadFocus = KeyboardIn(pane);
        composer.Remove(handle);
        handles.Remove(pane);
        heights.Remove(pane);
        Slot.Remove(pane);
        pane.Host = null;
        if (hadFocus)
        {
            RestoreKeyboard();
        }
    }

    private void Changed()
    {
        if (!closed)
        {
            Slot.Render();
        }
    }

    private void Adopted(IBoardReplyPane adopted)
    {
        if (focusPending is not { } id || adopted is not PaneHandle handle
            || panes.SlotFor(id) is not BoardReplyPanes.Slot.Editor e || !ReferenceEquals(e.Pane, adopted))
        {
            return;
        }
        focusPending = null;
        // Once the slot has put the view into the window.
        detail.DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            if (ReferenceEquals(panes.Live, handle))
            {
                Focus(handle.Pane, 0);
            }
        });
    }

    // The live pane's cap: the detail's viewport.
    private void ApplyVisibleHeight()
    {
        foreach (var pane in handles.Keys)
        {
            pane.SetVisibleHeight(detail.Scroller.ViewportHeight);
        }
    }

    // Focus and height

    private void Focus(ComposePane pane, int tries)
    {
        if (closed || suspended || !ReferenceEquals((panes.Live as PaneHandle)?.Pane, pane) || pane.XamlRoot is null)
        {
            return;
        }
        var ready = pane.FocusEditorStart();
        // Into view once, on the first try or when the editor took the
        // keyboard after a wait: not on every retry, which would pull the
        // detail back while the user scrolls.
        if (tries == 0 || ready)
        {
            pane.StartBringIntoView(new BringIntoViewOptions { AnimationDesired = false });
        }
        if (!ready && tries < FocusTries)
        {
            var timer = detail.DispatcherQueue.CreateTimer();
            timer.Interval = FocusRetry;
            timer.IsRepeating = false;
            timer.Tick += (t, _) =>
            {
                t.Stop();
                Focus(pane, tries + 1);
            };
            timer.Start();
        }
    }

    // A pane that had the keyboard went: the state pill takes it, once the
    // layout settled (GTK restoreBoardReplyFocus).
    private void RestoreKeyboard() =>
        detail.DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            if (!closed && !suspended)
            {
                detail.FocusContent();
            }
        });

    private static bool KeyboardIn(ComposePane pane)
    {
        if (pane.XamlRoot is not { } root)
        {
            return false;
        }
        for (var d = FocusManager.GetFocusedElement(root) as DependencyObject; d is not null; d = VisualTreeHelper.GetParent(d))
        {
            if (ReferenceEquals(d, pane))
            {
                return true;
            }
        }
        return false;
    }

    // IComposePaneHost

    /// <inheritdoc/>
    public void TitleChanged(ComposePane pane)
    {
    }

    /// <inheritdoc/>
    public void SendEnabledChanged(ComposePane pane, bool enabled)
    {
    }

    /// <inheritdoc/>
    public void Toast(string text) => toasts.Show(text);

    /// <inheritdoc/>
    public void Ended(ComposePane pane, ComposePane.EndKind kind)
    {
        ArgumentNullException.ThrowIfNull(pane);
        if (!handles.TryGetValue(pane, out var handle))
        {
            return;
        }
        // Before Core closes the pane, which lets go of its browser and
        // with it of the keyboard.
        var hadFocus = KeyboardIn(pane);
        BoardReplyPanes.PaneEnd end = kind switch
        {
            ComposePane.EndKind.Sent => new BoardReplyPanes.PaneEnd.Sent(pane.SentText ?? ""),
            ComposePane.EndKind.Discarded => new BoardReplyPanes.PaneEnd.Discarded(),
            ComposePane.EndKind.Lost => new BoardReplyPanes.PaneEnd.Lost(),
            _ => new BoardReplyPanes.PaneEnd.Closed(),
        };
        panes.Ended(handle, end);
        if (hadFocus)
        {
            RestoreKeyboard();
        }
    }

    /// <summary>
    /// The editor grew while it has the keyboard: the detail scrolls so the
    /// pane's bottom edge (its Send row) stays in sight.
    /// </summary>
    public void HeightChanged(ComposePane pane)
    {
        ArgumentNullException.ThrowIfNull(pane);
        var now = pane.EditorFrameHeight;
        var grew = !heights.TryGetValue(pane, out var before) || now > before;
        heights[pane] = now;
        if (!grew || !ReferenceEquals((panes.Live as PaneHandle)?.Pane, pane) || !pane.EditorHasFocus())
        {
            return;
        }
        detail.DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            if (pane.XamlRoot is null || pane.ActualHeight <= 0)
            {
                return;
            }
            pane.StartBringIntoView(new BringIntoViewOptions
            {
                AnimationDesired = false,
                TargetRect = new Rect(0, Math.Max(0, pane.ActualHeight - 1), 1, 1),
            });
        });
    }

    // What Core's rules and the compose controller need of the pane.
    private sealed class PaneHandle(ComposePane pane) : IBoardReplyPane, IComposeWindowHandle
    {
        public ComposePane Pane { get; } = pane;

        public bool HasUnsavedText => Pane.HasUnsavedText;

        public bool IsSending => Pane.IsSending;

        public bool IsLost => Pane.IsLost;

        public string ReplyTitle => Pane.TitleText;

        public Task<bool> SettleAsync() => Pane.SettleAsync();

        public void Close() => Pane.Close();

        public void Abandon() => Pane.Abandon();

        public void SetAccounts(IReadOnlyList<Account> accounts, bool placeholder) => Pane.SetAccounts(accounts, placeholder);

        public void Toast(string text) => Pane.Toast(text);

        public bool Edits(Draft draft) => Pane.Edits(draft);
    }
}
