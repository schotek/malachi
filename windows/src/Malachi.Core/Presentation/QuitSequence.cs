// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppDelegate.swift
// (applicationShouldTerminate: stop the connection and the daemon, sweep
// the open directory, then terminate); GTK: ui/main.go (ConnectShutdown:
// sweep, rpc.Close, sup.Stop). The Windows addition of docs/windows-port.md
// §0 comes first: Quit saves the dirty drafts of every compose window
// (ComposeController.SaveForQuitAsync) and asks the close question only of
// the windows whose save failed; a Cancel there abandons the Quit, as a
// Cancel of the same question abandons a window's close. After that
// nothing can stop it: the windows hide, the daemon this app started is
// stopped (up to 15 s while its syncers log out, off the UI thread as on
// macOS), what the app holds is released, and the application exits.
//
// A second Quit while the first asks is the same Quit. A session end
// (WM_ENDSESSION, the terminal's CTRL_CLOSE) skips the drafts and the
// questions, also when it arrives while a question is up: the system gives
// the app a few seconds. A step that throws is logged and the next one
// runs: the app always exits. Controllers' rule of §7.1: no
// ConfigureAwait(false), the steps run on the UI thread.

using System;
using System.Collections.Generic;
using System.Diagnostics.CodeAnalysis;
using System.Threading.Tasks;
using Malachi.Core.Controllers;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Presentation;

/// <summary>The app's way out (Quit). UI-thread-affine.</summary>
public sealed partial class QuitSequence
{
    private readonly QuitSteps steps;
    private readonly ILogger logger;
    private Phase phase;
    private Task<bool>? asking;
    private Task<bool>? stopping;

    /// <summary>A sequence over <paramref name="steps"/>.</summary>
    public QuitSequence(QuitSteps steps, ILogger<QuitSequence>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(steps);
        this.steps = steps;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
    }

    private enum Phase
    {
        Idle,
        Saving,
        Stopping,
        Done,
    }

    /// <summary>A Quit is underway: saving, asking, or past the point of no return.</summary>
    public bool IsQuitting => phase != Phase.Idle;

    /// <summary>Past the point of no return: the app is stopping or has exited.</summary>
    public bool IsStopping => phase is Phase.Stopping or Phase.Done;

    /// <summary>
    /// Quits for <paramref name="reason"/>. True when the app is exiting;
    /// false when a user's Quit was abandoned (a close question answered
    /// Cancel, or the drafts could not be saved at all), and the app runs
    /// on.
    /// </summary>
    public Task<bool> QuitAsync(QuitReason reason = QuitReason.User)
    {
        switch (phase)
        {
            case Phase.Stopping or Phase.Done:
                return stopping!;
            case Phase.Saving when reason == QuitReason.SessionEnd:
                // The session ends while a question is up: it gets no answer.
                return Stop();
            case Phase.Saving:
                return asking!;
        }
        if (reason == QuitReason.SessionEnd)
        {
            return Stop();
        }
        phase = Phase.Saving;
        asking = SaveThenStopAsync();
        return asking;
    }

    [SuppressMessage("Design", "CA1031", Justification = "A failure to save must abandon the Quit, never crash it.")]
    private async Task<bool> SaveThenStopAsync()
    {
        try
        {
            IReadOnlyList<IComposeWindowHandle> failed = steps.SaveDrafts is { } save ? await save() : [];
            foreach (var window in failed)
            {
                if (phase != Phase.Saving)
                {
                    break;
                }
                var ask = steps.AskClose ?? (static w => w.CloseForQuitAsync());
                if (!await ask(window))
                {
                    if (phase == Phase.Saving)
                    {
                        LogAbandoned(logger);
                        phase = Phase.Idle;
                        asking = null;
                        return false;
                    }
                    break;
                }
            }
        }
        catch (Exception e)
        {
            if (phase == Phase.Saving)
            {
                // Nothing tells which drafts are safe: the app runs on, and
                // the next Quit tries again.
                LogSaveFailed(logger, e);
                phase = Phase.Idle;
                asking = null;
                return false;
            }
        }
        return await Stop();
    }

    // The point of no return, once.
    private Task<bool> Stop()
    {
        if (stopping is null)
        {
            phase = Phase.Stopping;
            stopping = StopAsync();
        }
        return stopping;
    }

    [SuppressMessage("Design", "CA1031", Justification = "Every step runs and the app exits, whatever one of them throws.")]
    private async Task<bool> StopAsync()
    {
        Step(steps.BeginStopping, "begin stopping");
        if (steps.StopDaemon is { } stop)
        {
            try
            {
                await stop();
            }
            catch (Exception e)
            {
                LogStepFailed(logger, "stop the daemon", e);
            }
        }
        Step(steps.Release, "release");
        phase = Phase.Done;
        Step(steps.Exit, "exit");
        return true;
    }

    [SuppressMessage("Design", "CA1031", Justification = "Every step runs and the app exits, whatever one of them throws.")]
    private void Step(Action? step, string name)
    {
        try
        {
            step?.Invoke();
        }
        catch (Exception e)
        {
            LogStepFailed(logger, name, e);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "quit abandoned: a compose window stays open")]
    private static partial void LogAbandoned(ILogger logger);

    [LoggerMessage(Level = LogLevel.Error, Message = "quit abandoned: the drafts could not be saved")]
    private static partial void LogSaveFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Error, Message = "quit: {Step} failed")]
    private static partial void LogStepFailed(ILogger logger, string step, Exception error);
}
