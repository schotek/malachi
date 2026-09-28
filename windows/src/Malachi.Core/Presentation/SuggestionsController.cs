// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/RecipientSuggestionsController.swift
// (controlTextDidChange, textChanged, search, show, hide, move, accept,
// control(_:textView:doCommandBy:), cleanup); GTK: ui/internal/compose/
// suggest.go (suggestions: onChanged, search, show, hide, onKey, move,
// accept, suggestionRow). A presentation class macOS keeps in AppKit
// (docs/windows-port.md §7.4): the completion of one To, Cc or Bcc row
// without the popup. The token under the caret, the search, the insertion
// and the rules (Suggest) are Core's already; this adds what suggest.go
// keeps on its struct: the 150 ms pause in typing, the generation that
// drops the answers of a search the text has outrun, the selection with
// its wrap-around, and the keys the row hands over while the popup shows.
//
// The view (Malachi.App Compose/RecipientSuggestions) is a non-focusable
// Popup under the TextBox with a ListView of Rows; it calls TextChanged on
// every edit of the row, Hide when the row loses the focus, OnKey from the
// row's PreviewKeyDown, Accept for a click, and applies Accepted (the new
// text and caret) to the row. WinUI raises TextBox.TextChanged after the
// setter returns, so GTK's suppress flag around SetText becomes the text
// the acceptance produced: the TextChanged that carries it starts no
// search (the window still validates the row and marks the draft dirty,
// as GTK's SetText does).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Presentation;

/// <summary>
/// suggest.go <c>suggestions</c>: the recipient completion of one row.
/// Created by its window on the UI thread and UI-thread-affine
/// (docs/windows-port.md §7.1).
/// </summary>
public sealed partial class SuggestionsController : IDisposable
{
    private readonly RpcClient client;
    private readonly Func<AccountId> account;
    private readonly Func<(string Text, int Caret)> field;
    private readonly TimeProvider time;
    private readonly ILogger logger;
    private readonly ControllerScope scope;

    // gen: guards replies of a search the text has outrun.
    private ulong gen;

    // timer: the armed debounce; null when none is.
    private CancellationTokenSource? timer;

    // suppress: the text an acceptance put into the row, whose change
    // starts no search of its own.
    private string? suppressText;

    private bool closed;

    /// <param name="client">The transport for <c>contact.search</c>.</param>
    /// <param name="account">The sender identity's id: its address books are asked.</param>
    /// <param name="field">
    /// The row's text and its caret as a Unicode scalar offset (what a GTK
    /// entry reports; the view converts <c>TextBox.SelectionStart</c> with
    /// <see cref="Suggest.ScalarOffset"/>), read again when an answer comes.
    /// </param>
    /// <param name="time">The clock of the pause in typing; the system's by default.</param>
    /// <param name="pending">Where the background work is counted (tests wait on it); a tracker of its own by default.</param>
    /// <param name="logger">Method names and errors only, never what was typed.</param>
    public SuggestionsController(
        RpcClient client,
        Func<AccountId> account,
        Func<(string Text, int Caret)> field,
        TimeProvider? time = null,
        PendingWork? pending = null,
        ILogger<SuggestionsController>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(account);
        ArgumentNullException.ThrowIfNull(field);
        this.client = client;
        this.account = account;
        this.field = field;
        this.time = time ?? TimeProvider.System;
        this.logger = logger ?? (ILogger)NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>
    /// What the popup shows changed: it appeared with new
    /// <see cref="Rows"/>, its selection moved, or it went
    /// (<see cref="IsVisible"/> false).
    /// </summary>
    public event EventHandler? Changed;

    /// <summary>
    /// A suggestion was accepted: the view puts the new text into the row
    /// and the caret after the inserted address.
    /// </summary>
    public event EventHandler<SuggestionAcceptance>? Accepted;

    /// <summary>What the popup shows, in order (suggest.go <c>contacts</c>).</summary>
    public IReadOnlyList<Contact> Contacts { get; private set; } = [];

    /// <summary>The rows of <see cref="Contacts"/>, as the popup draws them.</summary>
    public IReadOnlyList<SuggestionRow> Rows { get; private set; } = [];

    /// <summary>The selected row; -1 when none is.</summary>
    public int SelectedIndex { get; private set; } = -1;

    /// <summary>Whether the popup shows (the window's Escape must not close the window then).</summary>
    public bool IsVisible { get; private set; }

    /// <summary>Whether a search waits for the pause in typing (for tests).</summary>
    public bool IsArmed => timer is not null;

    /// <summary>
    /// onChanged: every edit of the row. Finds the token under the caret
    /// and, after <see cref="Suggest.SuggestDebounce"/> without typing, asks
    /// for suggestions; a token shorter than
    /// <see cref="Suggest.SuggestMinChars"/> hides the popup instead. The
    /// change an acceptance made starts nothing.
    /// </summary>
    public void TextChanged()
    {
        scope.VerifyAccess();
        if (closed)
        {
            return;
        }
        var (text, caret) = field();
        if (suppressText is { } accepted)
        {
            suppressText = null;
            if (string.Equals(text, accepted, StringComparison.Ordinal))
            {
                return;
            }
        }
        CancelTimer();
        var (token, _) = Suggest.TokenAt(text, caret);
        if (ScalarText.CountBefore(token, token.Length) < Suggest.SuggestMinChars)
        {
            Hide();
            return;
        }
        var armed = new CancellationTokenSource();
        var cancelled = armed.Token;
        timer = armed;
        scope.RunDetached(async lifetime =>
        {
            using var wait = CancellationTokenSource.CreateLinkedTokenSource(lifetime, cancelled);
            try
            {
                await Task.Delay(Suggest.SuggestDebounce, time, wait.Token);
            }
            catch (OperationCanceledException)
            {
                return; // typed on, hidden, or closed
            }
            if (cancelled.IsCancellationRequested || !ReferenceEquals(timer, armed))
            {
                return;
            }
            timer = null;
            Search(token);
        });
    }

    /// <summary>
    /// hide: closes the popup and forgets any search in flight or waiting
    /// (the row lost the focus, the identity changed, Escape).
    /// </summary>
    public void Hide()
    {
        scope.VerifyAccess();
        CancelTimer();
        gen++;
        if (!IsVisible)
        {
            return;
        }
        IsVisible = false;
        Raise(Changed);
    }

    /// <summary>
    /// onKey: drives the popup from the row while it shows. Down and Up move
    /// the selection (wrapping), Enter and Tab accept the selected
    /// suggestion, Escape hides the popup; true when the key was taken.
    /// Every key while the popup is hidden, and every key with Ctrl or Alt,
    /// goes on to the row.
    /// </summary>
    public bool OnKey(SuggestionKey key, bool ctrlOrAlt)
    {
        scope.VerifyAccess();
        if (!IsVisible || ctrlOrAlt)
        {
            return false;
        }
        switch (key)
        {
            case SuggestionKey.Down:
                Move(1);
                return true;
            case SuggestionKey.Up:
                Move(-1);
                return true;
            case SuggestionKey.Enter or SuggestionKey.Tab:
                if (SelectedIndex >= 0)
                {
                    Accept(SelectedIndex);
                    return true;
                }
                return false;
            case SuggestionKey.Escape:
                Hide();
                return true;
            default:
                return false;
        }
    }

    /// <summary>
    /// accept: replaces the token under the caret with suggestion
    /// <paramref name="index"/> and puts the caret after the separator,
    /// ready for the next recipient (<see cref="Accepted"/>). The popup
    /// hides.
    /// </summary>
    public void Accept(int index)
    {
        scope.VerifyAccess();
        if (closed || index < 0 || index >= Contacts.Count)
        {
            return;
        }
        var c = Contacts[index];
        var (text, caret) = field();
        var (_, range) = Suggest.TokenAt(text, caret);
        var (newText, newCaret) = Suggest.ReplaceToken(text, range, new Address { Name = c.Name, Email = c.Address });
        Hide();
        suppressText = newText;
        scope.Raise(Accepted, this, new SuggestionAcceptance(newText, Suggest.IndexAtScalarOffset(newCaret, newText)));
    }

    /// <summary>
    /// The window is closing: the popup goes, and nothing arrives late
    /// (macOS <c>cleanup</c>, GTK cleanup's <c>hide</c>). Idempotent.
    /// </summary>
    public void Dispose()
    {
        if (closed)
        {
            return;
        }
        Hide();
        closed = true;
        scope.Close();
    }

    // move steps the selection, wrapping around.
    private void Move(int delta)
    {
        var n = Contacts.Count;
        if (n == 0)
        {
            return;
        }
        var i = Math.Max(SelectedIndex, 0);
        SelectedIndex = (((i + delta) % n) + n) % n;
        Raise(Changed);
    }

    // search asks the backend for the token and shows the answer, unless the
    // row has moved on meanwhile. A failure is logged, never shown:
    // completion is a convenience, and the row still takes what is typed.
    private void Search(string token)
    {
        gen++;
        var ticket = gen;
        var parameters = new ContactSearchParams { AccountId = account(), Query = token, Limit = Suggest.SuggestLimit };
        scope.Perform(client, API.ContactSearch, parameters, outcome =>
        {
            if (ticket != gen || closed)
            {
                return;
            }
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogSearchFailed(logger, error!);
                Hide();
                return;
            }
            var (text, caret) = field();
            if (!string.Equals(Suggest.TokenAt(text, caret).Token, token, StringComparison.Ordinal))
            {
                return;
            }
            Show(res.Contacts);
        });
    }

    // show fills the popup with the first suggestion selected, so Enter
    // takes it at once; an empty answer hides it.
    private void Show(IReadOnlyList<Contact> contacts)
    {
        if (contacts.Count == 0)
        {
            Contacts = [];
            Rows = [];
            SelectedIndex = -1;
            Hide();
            return;
        }
        Contacts = contacts;
        Rows = [.. contacts.Select(SuggestionRow.For)];
        SelectedIndex = 0;
        IsVisible = true;
        Raise(Changed);
    }

    private void CancelTimer()
    {
        timer?.Cancel();
        timer = null;
    }

    // ControllerEvents.Raise for a plain event: each handler in its own
    // guard, so that a view's failure never breaks the controller.
    private void Raise(EventHandler? handlers)
    {
        if (handlers is null)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "contact.search failed")]
    private static partial void LogSearchFailed(ILogger logger, Exception error);
}
