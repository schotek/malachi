// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardObservers.swift
// (BoardObservers); GTK: ui/internal/board/observers.go (Observers, Len).
//
// Several listeners for one of the board's application-wide objects (the
// board preferences, the triage run, its schedule): each Add returns a token
// that removes its handler. Public, as Go's, for the controllers and the
// tests (Swift keeps it internal to MalachiCore). Used on the UI thread only.

using System;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.Core.Boards;

/// <summary>The handlers, called in the order they were added.</summary>
public sealed class BoardObservers
{
    private readonly SortedDictionary<int, Action> handlers = [];
    private int next;

    /// <summary>The handlers installed (Go <c>Len</c>).</summary>
    public int Count => handlers.Count;

    /// <summary>Installs <paramref name="f"/>; the token removes it.</summary>
    public BoardObserverToken Add(Action f)
    {
        ArgumentNullException.ThrowIfNull(f);
        var id = next++;
        handlers[id] = f;
        return new BoardObserverToken(() => handlers.Remove(id));
    }

    /// <summary>
    /// Calls every handler. A handler may cancel its own token, or another's
    /// (that one is then not called); one added during the round is called
    /// from the next.
    /// </summary>
    public void Notify()
    {
        foreach (var id in handlers.Keys.ToList())
        {
            if (handlers.TryGetValue(id, out var f))
            {
                f();
            }
        }
    }
}
