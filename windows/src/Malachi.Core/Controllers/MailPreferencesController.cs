// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailPreferencesController.swift
// (MailPreferencesController); GTK: ui/internal/window/preferences.go
// (bindMail).
//
// The Swift callbacks are events of the same words (onPreferences is
// PreferencesChanged, onEnabled EnabledChanged, onDescription
// DescriptionChanged, onToast ToastRequested); the state they report is
// observable as well, for bindings. Swift's set(checkInterval:),
// set(remoteContent:) and set(offlineDays:) are SetCheckInterval,
// SetRemoteContent and SetOfflineDays. Every call goes through the
// controller's ControllerScope (docs/windows-port.md §7).

using System;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The Mail group of the preferences (preferences.go <c>bindMail</c>)
/// without the widgets: the daemon owns these preferences, so the group is
/// loaded with <c>config.get</c> and every change goes back with
/// <c>config.set</c> as a read-modify-write of the whole set.
/// </summary>
/// <remarks>
/// The group is insensitive until the load succeeds and while a save is in
/// flight; a failed load puts the error into the group's description, a
/// failed save shows a toast and reverts the pop-ups to the last state the
/// daemon confirmed. The page renders from <see cref="PreferencesChanged"/>
/// (null until the daemon answered) and <see cref="EnabledChanged"/>, and
/// maps values onto pop-up positions with <see cref="MailSelection"/>.
/// Create it, and call it, on the UI thread.
/// </remarks>
public sealed partial class MailPreferencesController : ObservableObject, IDisposable
{
    private readonly RpcClient client;
    private readonly ControllerScope scope;
    private readonly ILogger logger;

    // Bumped by every load and save; a reply of an older call is dropped so
    // that it cannot overwrite a newer one (the GTK dialog cannot get there
    // because the group is insensitive during a call; the controller is
    // callable regardless).
    private int op;

    /// <summary>A controller over <paramref name="client"/>, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="logger">Receives method names and error codes, never values.</param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public MailPreferencesController(RpcClient client, ILogger<MailPreferencesController>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        this.client = client;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>
    /// Called with the values to render (Swift <c>onPreferences</c>): after
    /// the load, after every save (the daemon's echo, which may differ from
    /// what was sent) and on a failed save (the previous values again, so
    /// the pop-ups revert).
    /// </summary>
    public event EventHandler<Preferences?>? PreferencesChanged;

    /// <summary>Called on every change of the group's sensitivity (Swift <c>onEnabled</c>).</summary>
    public event EventHandler<bool>? EnabledChanged;

    /// <summary>
    /// Called with the text that replaces the group's description ("Stored
    /// by the mail daemon.") when the load fails (Swift <c>onDescription</c>).
    /// </summary>
    public event EventHandler<string>? DescriptionChanged;

    /// <summary>Called with the text of a toast, a failed save (Swift <c>onToast</c>).</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>The last preference set the daemon confirmed; null until <c>config.get</c> answered.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(Selection))]
    public partial Preferences? Preferences { get; private set; }

    /// <summary>The group's sensitivity: loaded and no call in flight.</summary>
    [ObservableProperty]
    public partial bool IsEnabled { get; private set; }

    /// <summary>The pop-up positions of <see cref="Preferences"/>; null until it is loaded.</summary>
    public MailSelection? Selection => Preferences is { } p ? new MailSelection(p) : null;

    /// <summary>The page closed: late replies are dropped (Swift <c>closed</c>).</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>Drops every reply still in flight; nothing is emitted afterwards.</summary>
    public void Close()
    {
        scope.VerifyAccess();
        scope.Close();
    }

    /// <summary>Closes the controller.</summary>
    public void Dispose() => Close();

    /// <summary>
    /// Runs <c>config.get</c>. On success the values are rendered and the
    /// group enabled; on failure the error goes into the group's description
    /// and the group stays insensitive (preferences.go <c>bindMail</c>).
    /// </summary>
    public void Load()
    {
        scope.VerifyAccess();
        if (IsClosed)
        {
            return;
        }
        var my = ++op;
        scope.Perform(client, API.ConfigGet, new EmptyParams(), outcome =>
        {
            if (my != op)
            {
                return;
            }
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogCallFailed(logger, API.ConfigGet.Name, RpcErrorText.Classify(error).Kind, RpcErrorText.DaemonError(error)?.Code.Value ?? 0);
                DescriptionChanged?.Invoke(this, RpcErrorText.Text(L10n.T("Loading mail settings"), error));
                return;
            }
            Preferences = res.Preferences;
            PreferencesChanged?.Invoke(this, res.Preferences);
            SetEnabled(true);
        });
    }

    /// <summary>Check for New Mail, in seconds (0 = manually; Swift <c>set(checkInterval:)</c>).</summary>
    public void SetCheckInterval(int seconds)
    {
        scope.VerifyAccess();
        if (Preferences is { } want)
        {
            Save(want with { SyncIntervalSeconds = seconds });
        }
    }

    /// <summary>Load Remote Images (Swift <c>set(remoteContent:)</c>).</summary>
    public void SetRemoteContent(RemoteContentPolicy policy)
    {
        scope.VerifyAccess();
        if (Preferences is { } want)
        {
            Save(want with { RemoteContent = policy });
        }
    }

    /// <summary>Keep Mail Offline For, in days (0 = everything; Swift <c>set(offlineDays:)</c>).</summary>
    public void SetOfflineDays(int days)
    {
        scope.VerifyAccess();
        if (Preferences is { } want)
        {
            Save(want with { OfflineDays = days });
        }
    }

    /// <summary>
    /// The Check for New Mail pop-up's position, for the page: a position
    /// outside <see cref="PreferenceChoices.IntervalChoices"/> is ignored
    /// (preferences.go <c>save</c>).
    /// </summary>
    public void SelectInterval(int index)
    {
        if (index >= 0 && index < PreferenceChoices.IntervalChoices.Count)
        {
            SetCheckInterval(PreferenceChoices.IntervalChoices[index]);
        }
    }

    /// <summary>The Load Remote Images pop-up's position; one outside the table is ignored.</summary>
    public void SelectRemoteContent(int index)
    {
        if (index >= 0 && index < PreferenceChoices.RemoteChoices.Count)
        {
            SetRemoteContent(PreferenceChoices.RemoteChoices[index]);
        }
    }

    /// <summary>The Keep Mail Offline For pop-up's position; one outside the table is ignored.</summary>
    public void SelectRetention(int index)
    {
        if (index >= 0 && index < PreferenceChoices.RetentionChoices.Count)
        {
            SetOfflineDays(PreferenceChoices.RetentionChoices[index]);
        }
    }

    // Runs config.set with the whole set. The group is insensitive
    // meanwhile; the daemon's echo is what the pop-ups show afterwards, or
    // the previous values again when the call failed.
    private void Save(Preferences want)
    {
        if (IsClosed)
        {
            return;
        }
        var my = ++op;
        SetEnabled(false);
        scope.Perform(client, API.ConfigSet, new ConfigSetParams { Preferences = want }, outcome =>
        {
            if (my != op)
            {
                return;
            }
            SetEnabled(true);
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogCallFailed(logger, API.ConfigSet.Name, RpcErrorText.Classify(error).Kind, RpcErrorText.DaemonError(error)?.Code.Value ?? 0);
                ToastRequested?.Invoke(this, RpcErrorText.Text(L10n.T("Saving mail settings"), error));
                PreferencesChanged?.Invoke(this, Preferences);
                return;
            }
            Preferences = res.Preferences;
            PreferencesChanged?.Invoke(this, res.Preferences);
        });
    }

    private void SetEnabled(bool on)
    {
        if (on == IsEnabled)
        {
            return;
        }
        IsEnabled = on;
        EnabledChanged?.Invoke(this, on);
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "{Method} failed: {Kind} {Code}")]
    private static partial void LogCallFailed(ILogger logger, string method, RpcErrorText.FailureKind kind, int code);
}
