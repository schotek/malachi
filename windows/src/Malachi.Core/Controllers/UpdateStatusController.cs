// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first (GTK keeps this in its window, macOS in its AppState; the
// flow is ui/internal/updates/updates.go's package comment and
// docs/plans/update-check.md): the application's copy of the update status
// (docs/api.md §4.20), one for all windows. The app calls Refresh when the
// connection comes (again after a reconnect) and on every
// notify.updateChanged; every update.* call answers the status after it,
// which replaces the copy. Every read and write bumps a generation: an
// older answer that ends after a newer call is dropped. A failed call is
// logged (method and code) and keeps what was read before; the status
// itself carries the failures of a check, so nothing here has a text for
// the user. A daemon without the update check (an older one) leaves Status
// null: no banner, no line.
//
// The question of a security release (UpdateCheck.PromptFor) is asked at
// most once per version in this process, whatever the daemon says later:
// PendingPrompt offers it, BeginPrompt takes it (false when it was taken
// already), PromptAnswered tells the daemon (update.dismiss "prompted") so
// that no other window or client asks again. Create it, and call it, on the
// UI thread.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Transport;
using Malachi.Core.Updates;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>The application's update status (update.*, notify.updateChanged).</summary>
public sealed partial class UpdateStatusController : IDisposable
{
    private readonly RpcClient client;
    private readonly Func<string> locale;
    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private readonly HashSet<string> prompted = new(StringComparer.Ordinal);
    private int generation;
    private int checks;

    /// <summary>A controller over <paramref name="client"/>, on the calling (UI) thread; nothing is read yet.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="locale">The language the client's own texts are in ("cs", "en"), asked at each call.</param>
    /// <param name="logger">Receives method names and error codes.</param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public UpdateStatusController(RpcClient client, Func<string> locale, ILogger<UpdateStatusController>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(locale);
        this.client = client;
        this.locale = locale;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary><see cref="Status"/> or <see cref="Checking"/> changed.</summary>
    public event EventHandler? Changed;

    /// <summary>The last status the daemon answered; null before the first and for a daemon without the update check.</summary>
    public UpdateStatus? Status { get; private set; }

    /// <summary>A Check Now asked by this client is running (its button waits).</summary>
    public bool Checking => checks > 0;

    /// <summary>
    /// The security question to ask now: <see cref="UpdateCheck.PromptFor"/>
    /// of the status, unless this process asked it for that version already.
    /// </summary>
    public UpdatePrompt? PendingPrompt =>
        UpdateCheck.PromptFor(Status) is { } p && !prompted.Contains(p.Version) ? p : null;

    /// <summary>Asks <c>update.status</c> again.</summary>
    public void Refresh()
    {
        scope.VerifyAccess();
        if (scope.IsClosed)
        {
            return;
        }
        Call(API.UpdateStatus, new UpdateStatusParams { Locale = Locale() });
    }

    /// <summary>Check Now (<c>update.check</c>); nothing while <see cref="UpdateCheck.CanCheckNow"/> says no.</summary>
    public void CheckNow()
    {
        scope.VerifyAccess();
        if (scope.IsClosed || Checking || !UpdateCheck.CanCheckNow(Status))
        {
            return;
        }
        checks++;
        scope.Guard(() => Changed?.Invoke(this, EventArgs.Empty));
        Call(API.UpdateCheck, new UpdateCheckParams { Locale = Locale() }, () => checks--);
    }

    /// <summary>The preference's switch (<c>update.setPrefs</c>).</summary>
    public void SetEnabled(bool enabled)
    {
        scope.VerifyAccess();
        if (scope.IsClosed || (Status is { } s && s.Enabled == enabled))
        {
            return;
        }
        Call(API.UpdateSetPrefs, new UpdateSetPrefsParams { Enabled = enabled, Locale = Locale() });
    }

    /// <summary>The banner's Remind Me Later and Skip This Version (<c>update.dismiss</c>).</summary>
    public void Dismiss(string version, UpdateDismissAction action)
    {
        ArgumentNullException.ThrowIfNull(version);
        scope.VerifyAccess();
        if (scope.IsClosed)
        {
            return;
        }
        Call(API.UpdateDismiss, new UpdateDismissParams { Version = version, Action = action, Locale = Locale() });
    }

    /// <summary>A notice's Close (<c>update.dismissNotice</c>).</summary>
    public void DismissNotice(string id)
    {
        ArgumentNullException.ThrowIfNull(id);
        scope.VerifyAccess();
        if (scope.IsClosed)
        {
            return;
        }
        Call(API.UpdateDismissNotice, new UpdateDismissNoticeParams { Id = id, Locale = Locale() });
    }

    /// <summary>
    /// Takes the security question of <paramref name="version"/>: true the
    /// first time in this process, after which <see cref="PendingPrompt"/>
    /// no longer offers it; false when it was taken already.
    /// </summary>
    public bool BeginPrompt(string version)
    {
        ArgumentNullException.ThrowIfNull(version);
        scope.VerifyAccess();
        return prompted.Add(version);
    }

    /// <summary>
    /// The question of <paramref name="version"/> was answered, whatever the
    /// answer: <c>update.dismiss {version, "prompted"}</c>, so that no other
    /// window or client asks again. Sent even when the app quits right after.
    /// </summary>
    public void PromptAnswered(string version)
    {
        ArgumentNullException.ThrowIfNull(version);
        scope.VerifyAccess();
        prompted.Add(version);
        var my = ++generation;
        scope.PerformPastClose(
            client,
            API.UpdateDismiss,
            new UpdateDismissParams { Version = version, Action = UpdateDismissAction.Prompted, Locale = Locale() },
            outcome => Done(API.UpdateDismiss.Name, my, outcome));
    }

    /// <summary>Stops reporting; answers on their way are dropped.</summary>
    public void Dispose() => scope.Close();

    // The language for the daemon's texts; null (English) for none.
    private string? Locale() => locale() is { Length: > 0 } l ? l : null;

    private void Call<TParams>(RpcMethod<TParams, UpdateStatus> method, TParams parameters, Action? ended = null)
    {
        var my = ++generation;
        scope.Perform(client, method, parameters, outcome =>
        {
            ended?.Invoke();
            Done(method.Name, my, outcome);
        });
    }

    private void Done(string method, int my, Outcome<UpdateStatus> outcome)
    {
        if (outcome.TryGetValue(out var status, out var error))
        {
            if (my == generation)
            {
                Status = status;
            }
        }
        else
        {
            LogFailure(method, error);
        }
        scope.Guard(() => Changed?.Invoke(this, EventArgs.Empty));
    }

    private void LogFailure(string method, Exception? e)
    {
        if (logger.IsEnabled(LogLevel.Information))
        {
            LogCallFailed(logger, method, e is RpcException r ? r.Error.Code.Value : 0);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "{Method} failed (code {Code})")]
    private static partial void LogCallFailed(ILogger logger, string method, int code);
}
