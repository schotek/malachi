// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/APIAssistant/AssistantSessions.swift;
// GTK: ui/internal/apiassistant/registry.go (Sessions, NewSessions, add,
// keepEarly, remove, Wait, Len, Handle, Disconnected, earlyMax, earlyFor).
// The open sessions of the daemon's assistant by the daemon's session id:
// the application gives every notification to Handle from its dispatcher,
// and a notify.assistantEvent reaches its session. One per application (per
// connection: a session belongs to the connection that started it).
//
// Windows: thread-safe as Go's (a lock where Swift has the main actor), so
// the application may feed it from any thread; Go's Wait is WaitAsync. The
// clock of earlyFor is a TimeProvider, which the tests replace.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using WireEvent = Malachi.Core.Api.AssistantEvent;

namespace Malachi.Core.ApiAssistants;

/// <summary>
/// apiassistant.Sessions: the open sessions of the daemon's assistant, by the
/// daemon's session id.
/// </summary>
public sealed class AssistantSessions
{
    /// <summary>apiassistant.earlyMax: the events kept per id of a session not open here yet.</summary>
    public const int EarlyMax = 256;

    /// <summary>apiassistant.earlyFor: how long the events of an id nobody claimed are kept.</summary>
    public static readonly TimeSpan EarlyFor = TimeSpan.FromSeconds(30);

    private readonly Lock gate = new();
    private readonly Dictionary<string, DaemonAssistantSession> open = new(StringComparer.Ordinal);
    // The events of a session not open here yet: a turn sent with
    // assistant.sessionStart may notify before the start answers with the
    // id; Add replays them.
    private readonly Dictionary<string, Early> early = new(StringComparer.Ordinal);
    private readonly TimeProvider time;
    private TaskCompletionSource? stopsDone;
    private int stops;

    /// <summary>apiassistant.NewSessions: an empty registry.</summary>
    /// <param name="timeProvider">The clock of <see cref="EarlyFor"/>.</param>
    public AssistantSessions(TimeProvider? timeProvider = null)
    {
        time = timeProvider ?? TimeProvider.System;
    }

    /// <summary>apiassistant.Sessions.Len: the number of open sessions.</summary>
    public int Count
    {
        get
        {
            lock (gate)
            {
                return open.Count;
            }
        }
    }

    /// <summary>
    /// The ids whose events are kept for a session not open here yet (a turn
    /// sent with the start notifies before the start answers), in no order.
    /// </summary>
    public IReadOnlyCollection<string> EarlyIds
    {
        get
        {
            lock (gate)
            {
                return [.. early.Keys];
            }
        }
    }

    /// <summary>
    /// apiassistant.Sessions.Handle for a decoded notification: true when it
    /// was a <c>notify.assistantEvent</c> (handled, or kept for a session not
    /// open here yet); false for any other, which the caller dispatches on.
    /// </summary>
    public bool Handle(DaemonNotification notification) =>
        notification is DaemonNotification.AssistantEvent n && Handle(n.Payload);

    /// <summary>
    /// Takes one <c>notify.assistantEvent</c>: its session's, else kept for a
    /// session not open here yet. Always true (it was an assistant event).
    /// </summary>
    public bool Handle(WireEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        DaemonAssistantSession? s;
        lock (gate)
        {
            if (!open.TryGetValue(e.SessionId, out s) && e.SessionId.Length > 0)
            {
                KeepEarly(e);
            }
        }
        s?.Handle(e);
        return true;
    }

    /// <summary>
    /// apiassistant.Sessions.Wait: waits for the <c>assistant.sessionStop</c>
    /// calls under way (Terminate sends them in the background), at most
    /// <paramref name="timeout"/>: true when all are through. The application
    /// calls it as it quits, after it ended the panel's and the requests'
    /// sessions and before it closes the connection, so that the daemon gets
    /// each stop (it would end the sessions with the connection anyway; the
    /// call then just fails on the closed connection).
    /// </summary>
    public async Task<bool> WaitAsync(TimeSpan timeout)
    {
        Task done;
        lock (gate)
        {
            if (stops == 0)
            {
                return true;
            }
            stopsDone ??= new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            done = stopsDone.Task;
        }
        try
        {
            await done.WaitAsync(timeout, time).ConfigureAwait(false);
            return true;
        }
        catch (TimeoutException)
        {
            return false;
        }
    }

    /// <summary>
    /// apiassistant.Sessions.Disconnected: the connection to the daemon went,
    /// and every session with it (the daemon ends them itself); each ends
    /// with "api_stopped".
    /// </summary>
    public void Disconnected()
    {
        DaemonAssistantSession[] sessions;
        lock (gate)
        {
            sessions = [.. open.Values];
        }
        foreach (var s in sessions)
        {
            s.Disconnected();
        }
    }

    internal void Add(DaemonAssistantSession s)
    {
        Early? kept;
        lock (gate)
        {
            open[s.Id] = s;
            // Replayed below, outside the lock.
            early.Remove(s.Id, out kept);
        }
        if (kept is null)
        {
            return;
        }
        foreach (var e in kept.Events)
        {
            s.Handle(e);
        }
    }

    internal void Remove(DaemonAssistantSession s)
    {
        lock (gate)
        {
            if (open.TryGetValue(s.Id, out var cur) && ReferenceEquals(cur, s))
            {
                open.Remove(s.Id);
            }
        }
    }

    internal void StopBegan()
    {
        lock (gate)
        {
            stops++;
        }
    }

    internal void StopEnded()
    {
        TaskCompletionSource? done = null;
        lock (gate)
        {
            stops--;
            if (stops == 0)
            {
                done = stopsDone;
                stopsDone = null;
            }
        }
        done?.TrySetResult();
    }

    // Remembers an event of a session not open yet, and forgets the ids
    // nobody claimed in time. Under the lock.
    private void KeepEarly(WireEvent e)
    {
        var now = time.GetUtcNow();
        foreach (var id in early.Where(p => now - p.Value.At > EarlyFor).Select(p => p.Key).ToList())
        {
            early.Remove(id);
        }
        if (!early.TryGetValue(e.SessionId, out var kept))
        {
            kept = new Early(now);
            early[e.SessionId] = kept;
        }
        if (kept.Events.Count < EarlyMax)
        {
            kept.Events.Add(e);
        }
    }

    // The events of a session before it was added.
    private sealed class Early(DateTimeOffset at)
    {
        public DateTimeOffset At { get; } = at;

        public List<WireEvent> Events { get; } = [];
    }
}
