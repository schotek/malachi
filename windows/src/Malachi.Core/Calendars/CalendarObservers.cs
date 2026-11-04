// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/DaemonCalendarSource.swift
// (CalendarObservers); Swift-first, no GTK counterpart yet.
//
// The observers of a calendar source, called in the order they were added,
// each with what changed (Cal.SourceChange). The token is the board's
// (BoardObserverToken), as Swift's CalendarObserverToken is a typealias of
// BoardObserverToken. Public, as BoardObservers, for the sources and the
// tests. Used on the UI thread only.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Boards;

namespace Malachi.Core.Calendars;

/// <summary>The handlers of a calendar source, called in the order they were added.</summary>
public sealed class CalendarObservers
{
    private readonly SortedDictionary<int, Action<Cal.SourceChange>> handlers = [];
    private int next;

    /// <summary>The handlers installed.</summary>
    public int Count => handlers.Count;

    /// <summary>Installs <paramref name="handler"/>; the token removes it, dropping the token does not.</summary>
    public BoardObserverToken Add(Action<Cal.SourceChange> handler)
    {
        ArgumentNullException.ThrowIfNull(handler);
        var id = next++;
        handlers[id] = handler;
        return new BoardObserverToken(() => handlers.Remove(id));
    }

    /// <summary>
    /// Calls every handler with <paramref name="change"/>. A handler may
    /// cancel its own token, or another's (that one is then not called); one
    /// added during the round is called from the next.
    /// </summary>
    public void Notify(Cal.SourceChange change)
    {
        ArgumentNullException.ThrowIfNull(change);
        foreach (var id in handlers.Keys.ToList())
        {
            if (handlers.TryGetValue(id, out var f))
            {
                f(change);
            }
        }
    }
}
