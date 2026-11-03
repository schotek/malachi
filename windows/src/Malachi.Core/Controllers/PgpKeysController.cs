// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first (GTK keeps this in its Preferences page, macOS in its pane;
// the rules are ui/internal/signature, the page is docs/plans/signatures.md
// §6 and §11.3 "Preferences → Security"): the stored OpenPGP keys of
// Preferences → Security. Refresh reads pgp.keyList (also on
// notify.signaturesChanged); Import runs pgp.keyImport for a pasted key or a
// file and tells the dialog the failure's sentence; Confirm, Unconfirm and
// Remove run pgp.keyVerify and pgp.keyRemove (the page asked the questions
// first). Every outcome of a change is a toast, then the list is read again.
// Nothing of a key is logged: only method names and error codes. Create it,
// and call it, on the UI thread; every event is raised there.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Signatures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>The stored OpenPGP public keys (pgp.keyList, pgp.keyImport, pgp.keyVerify, pgp.keyRemove).</summary>
public sealed partial class PgpKeysController : IDisposable
{
    private readonly RpcClient client;
    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private int generation;

    /// <summary>A controller over <paramref name="client"/>, on the calling (UI) thread; nothing is read yet.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="logger">Receives method names and error codes, never a name or an address.</param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public PgpKeysController(RpcClient client, ILogger<PgpKeysController>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        this.client = client;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>The keys, the read or the failure of the read changed.</summary>
    public event EventHandler? Changed;

    /// <summary>A transient message: the toast after a change.</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>The keys of the last read, in the daemon's order.</summary>
    public IReadOnlyList<PgpKey> Keys { get; private set; } = [];

    /// <summary>The first read ended (well or not).</summary>
    public bool Loaded { get; private set; }

    /// <summary>The last read failed (the page says <see cref="SignatureSecurityPage.LoadFailed"/>; the list keeps what was read before).</summary>
    public bool LoadFailed { get; private set; }

    /// <summary>The page's rows now: sorted, with pills and menus.</summary>
    public IReadOnlyList<SignatureKeyRow> Rows(DateTimeOffset now, Func<DateTimeOffset, string> date) =>
        SignatureView.KeyRows(Keys, now, date);

    /// <summary>Asks <c>pgp.keyList</c>.</summary>
    public void Refresh()
    {
        scope.VerifyAccess();
        if (scope.IsClosed)
        {
            return;
        }
        var my = ++generation;
        scope.Perform(client, API.PgpKeyList, new EmptyParams(), outcome =>
        {
            if (my != generation)
            {
                return;
            }
            Loaded = true;
            if (outcome.TryGetValue(out var result, out var error))
            {
                Keys = result.Keys;
                LoadFailed = false;
            }
            else
            {
                LoadFailed = true;
                LogFailure(API.PgpKeyList.Name, error);
            }
            Raise();
        });
    }

    /// <summary>
    /// Imports a pasted key or a file (<see cref="SignatureView.PastedParams"/>,
    /// <see cref="SignatureView.FileParams"/>). <paramref name="done"/> gets
    /// "" when it went well (the toast says how many keys) or the sentence
    /// that tells why not (<see cref="SignatureText.ImportFailedText"/>), for
    /// the dialog to show.
    /// </summary>
    public void Import(PgpKeyImportParams parameters, Action<string> done)
    {
        ArgumentNullException.ThrowIfNull(parameters);
        ArgumentNullException.ThrowIfNull(done);
        scope.VerifyAccess();
        if (scope.IsClosed)
        {
            return;
        }
        scope.Perform(client, API.PgpKeyImport, parameters, outcome =>
        {
            if (outcome.TryGetValue(out var result, out var error))
            {
                scope.Raise(ToastRequested, this, SignatureText.ImportedText(result));
                Refresh();
                done("");
                return;
            }
            LogFailure(API.PgpKeyImport.Name, error);
            done(SignatureText.ImportFailedText(SignatureView.ImportFailureOf(error)));
        });
    }

    /// <summary>Confirm Key… after the question (<c>pgp.keyVerify</c> {verified: true}).</summary>
    public void Confirm(string fingerprint) => Verify(fingerprint, verified: true);

    /// <summary>Remove Confirmation (<c>pgp.keyVerify</c> {verified: false}).</summary>
    public void Unconfirm(string fingerprint) => Verify(fingerprint, verified: false);

    /// <summary>Remove Key… after the question (<c>pgp.keyRemove</c>).</summary>
    public void Remove(string fingerprint)
    {
        ArgumentNullException.ThrowIfNull(fingerprint);
        scope.VerifyAccess();
        if (scope.IsClosed)
        {
            return;
        }
        scope.PerformPastClose(client, API.PgpKeyRemove, new PgpKeyRemoveParams { Fingerprint = fingerprint }, outcome =>
        {
            if (outcome.TryGetValue(out _, out var error))
            {
                scope.Raise(ToastRequested, this, SignatureText.KeyRemovedText());
            }
            else
            {
                LogFailure(API.PgpKeyRemove.Name, error);
                scope.Raise(ToastRequested, this, SignatureText.KeyRemoveFailedText());
            }
            Refresh();
        });
    }

    /// <summary>Stops reporting; answers on their way are dropped.</summary>
    public void Dispose() => scope.Close();

    private void Verify(string fingerprint, bool verified)
    {
        ArgumentNullException.ThrowIfNull(fingerprint);
        scope.VerifyAccess();
        if (scope.IsClosed)
        {
            return;
        }
        scope.PerformPastClose(client, API.PgpKeyVerify, new PgpKeyVerifyParams { Fingerprint = fingerprint, Verified = verified }, outcome =>
        {
            if (outcome.TryGetValue(out _, out var error))
            {
                scope.Raise(ToastRequested, this, verified ? SignatureText.KeyConfirmedText() : SignatureText.ConfirmationRemovedText());
            }
            else
            {
                LogFailure(API.PgpKeyVerify.Name, error);
                scope.Raise(ToastRequested, this, SignatureText.KeyVerifyFailedText(verified));
            }
            Refresh();
        });
    }

    private void Raise() => scope.Guard(() => Changed?.Invoke(this, EventArgs.Empty));

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
