// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §7.5): a Swift callback cannot
// throw, and a trap ends the app loudly. A C# handler can throw, and inside
// a controller it would leave the controller's state half changed (a
// connection whose system.info is never asked) or end a loop that lives as
// long as the controller (the only reader of the client's states, the
// retry loop, the status line's redraw) with one line in the log: a silent
// zombie. RpcClient.Raise isolates its handlers for the same reason; the
// controllers in front of it raise theirs here.

using System;
using System.Diagnostics.CodeAnalysis;

namespace Malachi.Core.Controllers.Infrastructure;

/// <summary>
/// Hands a controller's outputs to its views so that a view's failure
/// cannot break the controller: every handler runs in its own
/// <c>try</c>, and what one throws is reported by the scope's
/// <see cref="ControllerScope.Pending"/> (logged at error level and kept for
/// the tests, which fail on it) instead of reaching the controller.
/// </summary>
public static class ControllerEvents
{
    /// <summary>
    /// Calls every handler of <paramref name="handlers"/> with
    /// <paramref name="value"/>, in order; one that throws is reported and
    /// the next is called all the same.
    /// </summary>
    [SuppressMessage("Design", "CA1031", Justification = "A view's handler must not break the controller.")]
    public static void Raise<T>(this ControllerScope scope, EventHandler<T>? handlers, object sender, T value)
    {
        ArgumentNullException.ThrowIfNull(scope);
        if (handlers is null)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            try
            {
                ((EventHandler<T>)handler).Invoke(sender, value);
            }
            catch (Exception e)
            {
                scope.Pending.Report(e);
            }
        }
    }

    /// <summary>
    /// Runs <paramref name="step"/>, one step of work that must go on after
    /// it (a stream's item, a timer's tick, a view's callback among
    /// others); what it throws is reported instead of ending that work.
    /// </summary>
    [SuppressMessage("Design", "CA1031", Justification = "One failed step must not end the work around it.")]
    public static void Guard(this ControllerScope scope, Action step)
    {
        ArgumentNullException.ThrowIfNull(scope);
        ArgumentNullException.ThrowIfNull(step);
        try
        {
            step();
        }
        catch (Exception e)
        {
            scope.Pending.Report(e);
        }
    }
}
