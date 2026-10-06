// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardObservers.swift
// (BoardObserverToken); GTK: ui/internal/board/observers.go (ObserverToken).
// Used on the UI thread only, as Swift's @MainActor class.

using System;

namespace Malachi.Core.Board;

/// <summary>Removes a handler installed by an <c>Observe</c>; dropping the token does not.</summary>
public sealed class BoardObserverToken
{
    private Action? remove;

    internal BoardObserverToken(Action remove)
    {
        this.remove = remove;
    }

    /// <summary>Removes the handler; a second call does nothing.</summary>
    public void Cancel()
    {
        var r = remove;
        remove = null;
        r?.Invoke();
    }
}
