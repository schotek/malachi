// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/MessageViewController+Invitation.swift
// (InvitationCardPresenter: show, clear, changed; InvitationSources: source,
// mainWindow, showInCalendar); GTK: none yet (docs/calendar.md §5.7).
//
// The invitation card of a message on display: the reading pane and a
// message window (MessageView) and a conversation card (ConversationCard)
// each hold a presenter, which they feed with the card of message.get
// (Message.Invitation) on every render. The presenter makes an
// InvitationController over the calendar source of the view's window
// (AppState.StartedCalendarSourceFor: started on first use; the main
// window's while the view is in none yet), keeps it while the same message
// shows, follows the source's snapshot (the calendars' and accounts' names),
// and routes Join Video Call through the host's link confirmation and Show
// in Calendar to the main window (MainWindow.ShowInCalendar). The card of
// an answer stays until message.get brings another one: what was fed last
// is compared with the new card by its contents (the contract's JSON, as
// Swift's == compares the struct).

using System;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Microsoft.UI.Xaml;

namespace Malachi.App.Reader;

/// <summary>One invitation card and its controller.</summary>
internal sealed class InvitationCardPresenter
{
    private readonly AppState state;
    private readonly Func<Window?> window;
    private InvitationController? controller;

    // The card message.get gave last (an answer's card stays until
    // message.get brings another), as the contract's JSON.
    private byte[]? fed;
    private BoardObserverToken? token;

    /// <summary>A presenter of <paramref name="view"/> in the window <paramref name="window"/> gives.</summary>
    public InvitationCardPresenter(InvitationCardView view, AppState state, Func<Window?> window)
    {
        ArgumentNullException.ThrowIfNull(view);
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(window);
        View = view;
        this.state = state;
        this.window = window;
    }

    /// <summary>The card it draws.</summary>
    public InvitationCardView View { get; }

    /// <summary>Join Video Call: the URL through the host's link confirmation (no listed links).</summary>
    public Action<string>? OpenLink { get; set; }

    /// <summary>The card was drawn again (its height may have changed).</summary>
    public Action? Changed { get; set; }

    /// <summary>
    /// Shows the card of <paramref name="m"/> (message.get) for message
    /// <paramref name="s"/>, or nothing; <paramref name="cardOn"/> is the
    /// account's "Show the Invitation Card above the Message in Mail".
    /// </summary>
    public void Show(MessageSummary s, Message? m, bool cardOn)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (m is null && controller is { } kept && kept.MessageId == s.Id && kept.AccountId == s.AccountId)
        {
            // A render before message.get answered again (an entry let go):
            // the card stays as it was.
            return;
        }
        if (InvitationReading.Read(m, cardOn) is not { } card || m!.Summary.Id != s.Id || Source() is not { } source)
        {
            Clear();
            return;
        }
        if (controller is { } c && c.MessageId == s.Id && c.AccountId == s.AccountId && ReferenceEquals(c.Source, source) && c.CardOn == cardOn)
        {
            var json = JsonCoding.Encode(card);
            if (fed is null || !json.AsSpan().SequenceEqual(fed))
            {
                fed = json;
                c.Replace(card);
            }
            return;
        }
        Clear();
        var next = new InvitationController(source, s.AccountId, s.Id, card, cardOn, AppState.CalendarEnv());
        fed = JsonCoding.Encode(card);
        controller = next;
        next.Changed += (_, _) => Render();
        next.OpenLinkRequested += (_, url) => OpenLink?.Invoke(url);
        next.ShowInCalendarRequested += (_, e) => ShowInCalendar(e.EventId, next.Card.RecurrenceId, e.Day);
        // The calendars' and accounts' names come with the source's
        // snapshot: the card follows it.
        token = source.Observe(change =>
        {
            if (change is Cal.SourceChange.SnapshotChanged)
            {
                Render();
            }
        });
        Render();
    }

    /// <summary>Hides the card and lets its controller go (late answers are dropped).</summary>
    public void Clear()
    {
        token?.Cancel();
        token = null;
        controller?.Close();
        controller = null;
        fed = null;
        if (View.Visibility == Visibility.Visible || View.Controller is not null)
        {
            View.Show(null);
            Changed?.Invoke();
        }
    }

    /// <summary>The view went for good: the card's controller goes (as <see cref="Clear"/>).</summary>
    public void Close() => Clear();

    private void Render()
    {
        if (controller is null)
        {
            return;
        }
        View.Show(controller);
        Changed?.Invoke();
    }

    // InvitationSources.source: the source of the window the view is in,
    // else of the main window; null while there is none.
    private ICalendarDataSource? Source()
    {
        var w = window() ?? state.MainWindow;
        return w is null ? null : state.StartedCalendarSourceFor(w);
    }

    // InvitationSources.showInCalendar: the main window comes forward in the
    // Calendar at the event's day with the event's detail.
    private void ShowInCalendar(CalendarEventId? eventId, DateOrTime? recurrenceId, DateTimeOffset? day) =>
        state.MainWindow?.ShowInCalendar(eventId, recurrenceId, day);
}
