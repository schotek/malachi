// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first (GTK keeps this in its details dialog, macOS in its
// popover's controller; the rules are ui/internal/signature, the flow is
// docs/plans/signatures.md §6 and §11.3): the details of one message's
// signature as the badge's flyout shows them. It starts from the summary
// message.get carried (so a daemon that answers notImplemented to
// message.signature still has something to show), reads message.signature,
// and runs the three buttons: Check Revocation (message.signatureRevocation,
// the client waits 20 s), Import Key (pgp.keyImport with the key the message
// offers) and Confirm Key (pgp.keyVerify, after the flyout asked its
// question). notify.signaturesChanged reads the details again (Refresh).
//
// Texts come from SignatureText; a failure of Check Revocation or Import Key
// is told by the class in its error data (RevocationFailedText,
// ImportFailedText), another failure by the plain sentence of that class.
// Nothing of a certificate or key is logged: only method names and error
// codes. Create it, and call it, on the UI thread; every event is raised
// there.

using System;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Signatures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>The details of one message's signature (message.signature, message.signatureRevocation, pgp.keyImport, pgp.keyVerify).</summary>
public sealed partial class SignatureDetailsController : IDisposable
{
    private readonly RpcClient client;
    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private int generation;

    /// <summary>A controller for the signature of <paramref name="messageId"/>, on the calling (UI) thread; nothing is read yet.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="messageId">The message.</param>
    /// <param name="summary">The summary <c>message.get</c> carried; the details until <c>message.signature</c> answers.</param>
    /// <param name="from">The message's From address, for the explanation of a signature of someone else.</param>
    /// <param name="logger">Receives method names and error codes, never a name or an address.</param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public SignatureDetailsController(
        RpcClient client,
        MessageId messageId,
        SignatureSummary summary,
        string from,
        ILogger<SignatureDetailsController>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(summary);
        ArgumentNullException.ThrowIfNull(from);
        this.client = client;
        MessageId = messageId;
        From = from;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        Details = new SignatureDetails
        {
            Kind = summary.Kind,
            Status = summary.Status,
            Signer = summary.Signer,
            Notes = summary.Notes,
            PartId = summary.PartId,
            MessageId = messageId,
        };
    }

    /// <summary>The details, a toast or the busy action changed.</summary>
    public event EventHandler? Changed;

    /// <summary>A transient message (the flyout shows it as a line; the window may make it a toast).</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>The message.</summary>
    public MessageId MessageId { get; }

    /// <summary>The message's From address.</summary>
    public string From { get; }

    /// <summary>The details: the summary's until the daemon answers.</summary>
    public SignatureDetails Details { get; private set; }

    /// <summary>The action that runs (its button says so and is disabled); <see cref="SignatureAction.None"/> otherwise.</summary>
    public SignatureAction Busy { get; private set; }

    /// <summary>The sentence of the last failed action ("" when the last one went well).</summary>
    public string Problem { get; private set; } = "";

    /// <summary>The flyout's model over the details now; <paramref name="date"/> formats a date and time like the message headers.</summary>
    public SignatureDetailsView View(Func<DateTimeOffset, string> date) =>
        SignatureView.DetailsFor(Details, From, Busy, date);

    /// <summary>Reads <c>message.signature</c>; a null answer (no verdict) and a failure keep what is shown.</summary>
    public void Load()
    {
        scope.VerifyAccess();
        if (scope.IsClosed)
        {
            return;
        }
        var my = ++generation;
        scope.Perform(client, API.MessageSignature, new MessageSignatureParams { MessageId = MessageId }, outcome =>
        {
            if (my != generation)
            {
                return;
            }
            if (outcome.TryGetValue(out var result, out var error))
            {
                if (result.Signature is { } details)
                {
                    Details = details;
                }
            }
            else
            {
                LogFailure(API.MessageSignature.Name, error);
            }
            Raise();
        });
    }

    /// <summary>notify.signaturesChanged: trust changed, read the details again (quietly, with no busy mark).</summary>
    public void Refresh() => Load();

    /// <summary>Check Revocation (<c>message.signatureRevocation</c>); nothing while an action runs or the daemon cannot check.</summary>
    public void CheckRevocation()
    {
        scope.VerifyAccess();
        if (scope.IsClosed || Busy != SignatureAction.None || !SignatureView.CanCheckRevocation(Details))
        {
            return;
        }
        Begin(SignatureAction.CheckRevocation);
        scope.Perform(client, API.MessageSignatureRevocation, new MessageSignatureRevocationParams { MessageId = MessageId }, outcome =>
        {
            if (outcome.TryGetValue(out var result, out var error))
            {
                if (result.Signature is { } details)
                {
                    generation++; // an older read ends after this one
                    Details = details;
                }
                End("");
                return;
            }
            LogFailure(API.MessageSignatureRevocation.Name, error);
            End(SignatureText.RevocationFailedText(SignatureView.RevocationFailureOf(error)));
        });
    }

    /// <summary>Import Key (<c>pgp.keyImport</c> with the key the message offers); nothing while an action runs or there is no key.</summary>
    public void ImportKey()
    {
        scope.VerifyAccess();
        if (scope.IsClosed || Busy != SignatureAction.None || SignatureView.ImportParams(Details) is not { } parameters)
        {
            return;
        }
        Begin(SignatureAction.ImportKey);
        scope.PerformPastClose(client, API.PgpKeyImport, parameters, outcome =>
        {
            if (outcome.TryGetValue(out var result, out var error))
            {
                scope.Raise(ToastRequested, this, SignatureText.ImportedText(result));
                End("");
                Load();
                return;
            }
            LogFailure(API.PgpKeyImport.Name, error);
            End(SignatureText.ImportFailedText(SignatureView.ImportFailureOf(error)));
        });
    }

    /// <summary>
    /// Confirm Key (<c>pgp.keyVerify</c> {verified: true}), after the flyout
    /// asked <see cref="SignatureText.ConfirmKeyQuestion"/> and the user
    /// accepted; nothing while an action runs or there is no key to confirm.
    /// </summary>
    public void ConfirmKey()
    {
        scope.VerifyAccess();
        if (scope.IsClosed || Busy != SignatureAction.None || SignatureView.ConfirmParams(Details) is not { } parameters)
        {
            return;
        }
        Begin(SignatureAction.ConfirmKey);
        scope.PerformPastClose(client, API.PgpKeyVerify, parameters, outcome =>
        {
            if (outcome.TryGetValue(out _, out var error))
            {
                scope.Raise(ToastRequested, this, SignatureText.KeyConfirmedText());
                End("");
                Load();
                return;
            }
            LogFailure(API.PgpKeyVerify.Name, error);
            End(SignatureText.KeyVerifyFailedText(verified: true));
        });
    }

    /// <summary>Stops reporting; answers on their way are dropped.</summary>
    public void Dispose() => scope.Close();

    private void Begin(SignatureAction action)
    {
        Busy = action;
        Problem = "";
        Raise();
    }

    private void End(string problem)
    {
        Busy = SignatureAction.None;
        Problem = problem;
        Raise();
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
