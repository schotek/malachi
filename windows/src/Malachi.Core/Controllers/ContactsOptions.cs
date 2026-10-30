// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ContactsOptions.swift
// (ContactsOptions, ContactsCalls); GTK: ui/internal/contactsctl client.go
// (Options, call, ErrorMessage, AsError, ConflictCard, ConsentData,
// WriteTimeout).
//
// What the contacts controllers share: the daemon they call
// (IContactsCaller), the strftime writer of birthdays, the clock, the
// debounce timer, the sentence of a failed call and the count of the calls
// under way. The controllers (ContactsListController,
// ContactsDetailController, ContactsLookupController,
// ContactsPreferencesController) are ports of the Go reference's
// WindowController, ContactController, Lookup and PreferencesController:
// classes of the UI thread that keep the state of the Contacts window, of
// the contact shown and edited, of the chips' lookups and of Preferences →
// Contacts, build what the views show from the pure ContactBook and report
// through events.
//
// Windows: Swift's ContactsCalls is the PendingWork the calls are tracked
// in (tests wait on its IdleAsync); a call runs after the caller's turn
// (Task.Yield on the UI thread's context) and its answer comes back there,
// as ControllerScope's work does. The debounce is After, a timer on the
// injected TimeProvider by default, which the tests replace with their own.
// Only the method and the error code are logged, never a contact's text.

using System;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>contactsctl.Options: what every contacts controller runs with; the window builds one and hands it to each.</summary>
/// <remarks>Create and use it on the UI thread.</remarks>
public sealed partial class ContactsOptions
{
    /// <summary>contactsctl.WriteTimeout: what a write to the server is given.</summary>
    public static readonly TimeSpan WriteTimeout = TimeSpan.FromSeconds(60);

    private readonly ILogger logger;

    /// <summary>Options over <paramref name="caller"/>.</summary>
    /// <param name="caller">The daemon.</param>
    /// <param name="format">Writes birthdays (<see cref="ContactBook.CultureStrftime"/>; the tests <see cref="ContactBook.EnglishStrftime"/>).</param>
    /// <param name="time">The clock and the debounce timer; the system's when null.</param>
    /// <param name="pending">Counts the calls under way; one of its own when null.</param>
    /// <param name="logger">Receives method names and codes, never a contact's text.</param>
    public ContactsOptions(
        IContactsCaller caller, ContactBook.Strftime format, TimeProvider? time = null, PendingWork? pending = null, ILogger? logger = null)
    {
        ArgumentNullException.ThrowIfNull(caller);
        ArgumentNullException.ThrowIfNull(format);
        Caller = caller;
        Format = format;
        Time = time ?? TimeProvider.System;
        Pending = pending ?? new PendingWork();
        this.logger = logger ?? NullLogger.Instance;
        After = TimerAfter;
    }

    /// <summary>The daemon.</summary>
    public IContactsCaller Caller { get; }

    /// <summary>Writes birthdays.</summary>
    public ContactBook.Strftime Format { get; }

    /// <summary>The clock.</summary>
    public TimeProvider Time { get; }

    /// <summary>The calls and questions under way (Swift <c>ContactsCalls</c>).</summary>
    public PendingWork Pending { get; }

    /// <summary>Runs an action after a delay on the UI thread (a debounce); the tests set their own.</summary>
    public Action<TimeSpan, Action> After { get; set; }

    /// <summary>
    /// The usual sentence of a failed call with the action <c>what</c>
    /// (<see cref="RpcErrorText.Text(string, Exception?)"/>) for an error the
    /// contacts have no sentence of; null is <c>what</c> alone.
    /// </summary>
    public Func<string, Exception, string>? RpcText { get; set; }

    /// <summary>Now.</summary>
    public DateTimeOffset Now => Time.GetUtcNow();

    /// <summary>
    /// contactsctl.Options.ErrorMessage: the contacts' own sentence by code
    /// (<paramref name="account"/> is the account's label for consent), else
    /// <see cref="RpcText"/> with <paramref name="what"/>; "" for null. Never
    /// the daemon's message.
    /// </summary>
    public string ErrorMessage(Exception? error, string account = "", string what = "")
    {
        if (error is null)
        {
            return "";
        }
        if (ContactsText.ErrorText(AsRpcError(error).Code, account ?? "") is { } t)
        {
            return t;
        }
        var w = string.IsNullOrEmpty(what) ? ContactsText.WindowTitle : what;
        return RpcText is { } f ? f(w, error) : w;
    }

    /// <summary>
    /// contactsctl.AsError: the daemon's error as it is, a call without a
    /// connection unavailable, one that ran out of time or was cancelled
    /// cancelled, anything else internalError (only the code counts).
    /// </summary>
    public static RpcError AsRpcError(Exception error)
    {
        var (kind, e) = RpcErrorText.Classify(error);
        return kind switch
        {
            RpcErrorText.FailureKind.Daemon when e is not null => e,
            RpcErrorText.FailureKind.NoBackend => new RpcError { Code = ErrorCode.Unavailable },
            RpcErrorText.FailureKind.TimedOut => new RpcError { Code = ErrorCode.Cancelled },
            _ => new RpcError { Code = ErrorCode.InternalError },
        };
    }

    /// <summary>contactsctl.ConflictCard: the server's version in a contactConflict; null otherwise.</summary>
    public static ContactCard? ConflictCard(Exception? error)
    {
        if (RpcErrorText.DaemonError(error) is not { } e || e.Code != ErrorCode.ContactConflict)
        {
            return null;
        }
        return Data(e, ApiJsonContext.Wire.ContactConflictData)?.Server is { Id.Value.Length: > 0 } s ? s : null;
    }

    /// <summary>contactsctl.ConsentData: a contactsConsentRequired's data; null otherwise.</summary>
    public static ContactsConsentRequiredData? ConsentData(Exception? error)
    {
        if (RpcErrorText.DaemonError(error) is not { } e || e.Code != ErrorCode.ContactsConsentRequired)
        {
            return null;
        }
        return Data(e, ApiJsonContext.Wire.ContactsConsentRequiredData) is { AccountId.Value.Length: > 0 } d ? d : null;
    }

    /// <summary>The error's code; null for a success.</summary>
    public static ErrorCode? Code<T>(Outcome<T> outcome) => outcome.Error is { } e ? AsRpcError(e).Code : null;

    /// <summary>
    /// contactsctl.call: runs the method after the caller's turn and hands
    /// <paramref name="done"/> the outcome on the UI thread;
    /// <paramref name="timeout"/> null is the method's own. Tracked in
    /// <see cref="Pending"/>.
    /// </summary>
    internal void Call<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan? timeout, Action<Outcome<TResult>> done)
    {
        Pending.Track(CallAsync(method, parameters, timeout ?? method.Timeout, done));
    }

    /// <summary><see cref="Call{TParams, TResult}(RpcMethod{TParams, TResult}, TParams, TimeSpan?, Action{Outcome{TResult}})"/> with the method's own timeout.</summary>
    internal void Call<TParams, TResult>(RpcMethod<TParams, TResult> method, TParams parameters, Action<Outcome<TResult>> done) =>
        Call(method, parameters, null, done);

    /// <summary>Runs a question (or any other wait) counted with the calls.</summary>
    internal void Spawn(Func<Task> body) => Pending.Track(SpawnAsync(body));

    private async Task CallAsync<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout, Action<Outcome<TResult>> done)
    {
        await Task.Yield();
        Outcome<TResult> outcome;
        try
        {
            outcome = Outcome.Success(await Caller.CallAsync(method, parameters, timeout, CancellationToken.None));
        }
#pragma warning disable CA1031 // Every failure of the call is its outcome, as Swift's Result is.
        catch (Exception e)
#pragma warning restore CA1031
        {
            var code = AsRpcError(e).Code.Value;
            if (logger.IsEnabled(LogLevel.Information))
            {
                LogCallFailed(logger, method.Name, code);
            }
            outcome = Outcome.Failure<TResult>(e);
        }
        done(outcome);
    }

    private static async Task SpawnAsync(Func<Task> body)
    {
        await Task.Yield();
        await body();
    }

    // The application's timer: a delay on the clock, then f on the UI thread.
    private void TimerAfter(TimeSpan delay, Action f)
    {
        _ = DelayThenAsync(delay, f);
    }

    private async Task DelayThenAsync(TimeSpan delay, Action f)
    {
        try
        {
            await Task.Delay(delay, Time);
            f();
        }
#pragma warning disable CA1031 // Reported, not lost: nobody awaits a timer.
        catch (Exception e)
#pragma warning restore CA1031
        {
            Pending.Report(e);
        }
    }

    private static T? Data<T>(RpcError e, System.Text.Json.Serialization.Metadata.JsonTypeInfo<T> info)
        where T : class
    {
        if (e.Data is not { ValueKind: JsonValueKind.Object } data)
        {
            return null;
        }
        try
        {
            return data.Deserialize(info);
        }
        catch (JsonException)
        {
            return null;
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "contacts call {Method} failed: {Code}")]
    private static partial void LogCallFailed(ILogger logger, string method, int code);
}
