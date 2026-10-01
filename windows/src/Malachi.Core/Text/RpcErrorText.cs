// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Text/RPCErrorText.swift (rpcErrorText,
// endpointErrorText, tlsReasonText); GTK: ui/internal/widget/rpc.go
// (RPCErrorText, EndpointErrorText, tlsReasonText; unsubscribeFailed's
// sentence is ui/internal/bulkmail's, Bulk/BulkMail.cs).
//
// The sentences for failed calls. The daemon's message is technical English
// and only ever shown as a trailing detail; the daemon itself stays
// language-neutral. Swift tells a failed call by its type: RPCError (the
// daemon's error), RPCClient.ClientError (.notConnected and .disconnected
// "need a running mail backend", .timeout "timed out", .transport the plain
// "failed") and CancellationError ("timed out"); Go by client.ErrDisconnected,
// context.DeadlineExceeded and *api.Error, unwrapping as errors.Is/As do. In
// C# the daemon's error is the RpcError record, which a transport exception
// carries: the transport's exceptions tell this file what they are through
// IFailure, and a TimeoutException or an OperationCanceledException (the
// caller's cancellation, Swift's CancellationError) anywhere in the
// InnerException chain reads as "timed out".

using System;
using Malachi.Core.Api;
using Malachi.Core.Bulk;
using Malachi.Core.I18n;
using Malachi.Core.Wizard;

namespace Malachi.Core.Text;

/// <summary>The user-facing sentences for failed calls to the daemon.</summary>
public static class RpcErrorText
{
    /// <summary>What a failed call was, as the sentences tell failures apart.</summary>
    public enum FailureKind
    {
        /// <summary>Anything else: the plain "failed" (Swift ClientError.transport, an unknown error).</summary>
        Failed,

        /// <summary>
        /// No daemon to ask: not connected, or the connection was lost (Swift
        /// ClientError.notConnected and .disconnected, Go client.ErrDisconnected).
        /// </summary>
        NoBackend,

        /// <summary>The call ran out of time (Swift ClientError.timeout, Go context.DeadlineExceeded).</summary>
        TimedOut,

        /// <summary>The daemon answered with an error (<see cref="IFailure.DaemonError"/>).</summary>
        Daemon,
    }

    /// <summary>
    /// What an exception of the transport tells about a failed call. The
    /// transport's exceptions implement it: its client errors with
    /// <see cref="FailureKind.NoBackend"/>, <see cref="FailureKind.TimedOut"/>
    /// or <see cref="FailureKind.Failed"/>, its exception for an error the
    /// daemon answered with <see cref="FailureKind.Daemon"/> and the error.
    /// </summary>
    public interface IFailure
    {
        /// <summary>What the failure was.</summary>
        FailureKind Kind { get; }

        /// <summary>The daemon's error, for <see cref="FailureKind.Daemon"/>; null otherwise.</summary>
        RpcError? DaemonError { get; }
    }

    /// <summary>
    /// rpc.go RPCErrorText (Swift <c>rpcErrorText</c>): turns a failed call
    /// into a short sentence. <paramref name="what"/> is the (already
    /// translated) action in progressive form, e.g.
    /// <c>L10n.T("Saving the draft")</c>. Null is the plain "failed".
    /// </summary>
    public static string Text(string what, Exception? error)
    {
        ArgumentNullException.ThrowIfNull(what);
        var (kind, e) = Classify(error);
        return kind switch
        {
            FailureKind.NoBackend => L10n.T("%s needs a running mail backend", what),
            FailureKind.TimedOut => L10n.T("%s timed out", what),
            FailureKind.Daemon => Text(what, e),
            _ => L10n.T("%s failed", what),
        };
    }

    /// <summary>
    /// rpc.go RPCErrorText for an error the daemon answered with (Go's
    /// <c>*api.Error</c> branch); null is the plain "failed".
    /// </summary>
    public static string Text(string what, RpcError? error)
    {
        ArgumentNullException.ThrowIfNull(what);
        if (error is null)
        {
            return L10n.T("%s failed", what);
        }
        switch (error.Code.Value)
        {
            case ErrorCode.NotImplemented:
            case ErrorCode.MethodNotFound: // an older daemon lacks the method
                return L10n.T("%s is not available yet", what);
            case ErrorCode.Conflict:
                return L10n.T("%s conflicted with another change", what);
            case ErrorCode.InvalidArgument:
                return L10n.T("%s was rejected: %s", what, error.Message);
            case ErrorCode.DraftNotFound:
                return L10n.T("The draft no longer exists");
            case ErrorCode.AttachmentNotFound:
            case ErrorCode.PartNotFound: // of a draft, or of a message
                return L10n.T("The attachment no longer exists");
            case ErrorCode.AttachmentTooBig:
                return L10n.T("The attachment is too big");
            case ErrorCode.SanitizeFailed:
                return L10n.T("%s failed: formatted text cannot be saved yet", what);
            case ErrorCode.AccountNotFound:
                return L10n.T("%s failed: unknown account", what);
            case ErrorCode.KeyringError:
                return L10n.T("%s failed: the system keyring is unavailable", what);
            case ErrorCode.AuthRequired:
                // TRANSLATORS: %s is an action such as "Testing the connection"
                return L10n.T("%s failed: sign-in required", what);
            case ErrorCode.AuthFailed:
                return L10n.T("%s failed: the server rejected the user name or password", what);
            case ErrorCode.NetworkError:
                return L10n.T("%s failed: the server could not be reached", what);
            case ErrorCode.ServerError:
                return L10n.T("%s failed: the server returned an error", what);
            case ErrorCode.TlsError:
                // A refused certificate says why on its own (the outbox banner
                // of a failed message shows it).
                return TlsReasonText(error) ?? L10n.T("%s failed: the secure connection could not be established", what);
            case ErrorCode.ServerTimeout:
                return L10n.T("%s failed: the server did not respond in time", what);
            case ErrorCode.Offline:
                // TRANSLATORS: %s is an action such as "Opening the attachment".
                return L10n.T("%s failed: no network connection", what);
            case ErrorCode.Unavailable:
                // A paused account, or a move the server has not seen yet.
                return L10n.T("%s failed: try again in a moment", what);
            case ErrorCode.PartNotDownloaded:
                return L10n.T("%s failed: the attachment is not on this computer", what);
            case ErrorCode.MessageGone:
                return L10n.T("%s failed: the message is no longer on the server", what);
            case ErrorCode.UnsubscribeFailed:
                // The sentence stands on its own (widget.RPCErrorText).
                return BulkMail.Refused();
            default:
                return L10n.T("%s failed", what);
        }
    }

    /// <summary>
    /// rpc.go EndpointErrorText: the sentence for one endpoint of
    /// <c>account.test</c>, and for why an account is offline or in error.
    /// </summary>
    public static string EndpointErrorText(RpcError? e)
    {
        if (e is null)
        {
            return L10n.T("Failed");
        }
        switch (e.Code.Value)
        {
            case ErrorCode.AuthFailed:
                return L10n.T("The server rejected the user name or password");
            case ErrorCode.NetworkError:
                return L10n.T("The server could not be reached");
            case ErrorCode.ServerError:
                return L10n.T("The server returned an error");
            case ErrorCode.TlsError:
                return TlsReasonText(e) ?? L10n.T("The secure connection could not be established");
            case ErrorCode.ServerTimeout:
                return L10n.T("The server did not respond in time");
            case ErrorCode.NotImplemented:
                return L10n.T("Not supported yet");
            case ErrorCode.AuthRequired:
                return L10n.T("Sign in to this account again");
            case ErrorCode.Unavailable:
                return L10n.T("The sign-in service is not available");
            case ErrorCode.KeyringError:
                // TRANSLATORS: endpoint/account problem
                return L10n.T("The system keyring is unavailable");
            case ErrorCode.Offline:
                // TRANSLATORS: endpoint/account problem
                return L10n.T("No network connection");
            case ErrorCode.InvalidArgument:
                // TRANSLATORS: %s is a technical message from the mail backend.
                return L10n.T("Rejected: %s", e.Message);
            default:
                // TRANSLATORS: %s is a technical message from the mail backend.
                return L10n.T("Failed: %s", e.Message);
        }
    }

    /// <summary>
    /// rpc.go tlsReasonText: the sentence for the reason of a
    /// <c>tlsError</c>'s details (docs/api.md §2); null when the error
    /// carries none, or for a handshake failure, which the callers' general
    /// sentence describes.
    /// </summary>
    public static string? TlsReasonText(RpcError e)
    {
        ArgumentNullException.ThrowIfNull(e);
        if (CertTrust.Details(e) is not { } p)
        {
            return null;
        }
        return p.Reason.Value switch
        {
            TlsErrorReason.Untrusted => L10n.T("The server's certificate is not from a trusted authority"),
            TlsErrorReason.HostnameMismatch => L10n.T("The server's certificate is for another name"),
            TlsErrorReason.Expired => L10n.T("The server's certificate has expired"),
            TlsErrorReason.NotYetValid => L10n.T("The server's certificate is not valid yet"),
            TlsErrorReason.Invalid => L10n.T("The server's certificate is not valid"),
            TlsErrorReason.PinMismatch => L10n.T("The server presented a different certificate than the one you trust"),
            TlsErrorReason.StarttlsUnavailable => L10n.T("The server does not offer STARTTLS"),
            TlsErrorReason.TlsRequired => L10n.T("The server requires TLS before signing in"),
            TlsErrorReason.Handshake => null,
            // CertTrust turned every unknown reason into "other".
            // TRANSLATORS: the system's certificate check refused the server's
            // certificate for a reason it does not name (e.g. "not standards
            // compliant" on macOS).
            _ => L10n.T("The system does not accept the server's certificate"),
        };
    }

    /// <summary>
    /// What a failed call was (Swift's type checks, Go's errors.Is/As): the
    /// first exception of the InnerException chain that is an
    /// <see cref="IFailure"/>, a <see cref="TimeoutException"/> or an
    /// <see cref="OperationCanceledException"/> decides; anything else is
    /// <see cref="FailureKind.Failed"/>.
    /// </summary>
    public static (FailureKind Kind, RpcError? Error) Classify(Exception? error)
    {
        for (var e = error; e is not null; e = e.InnerException)
        {
            switch (e)
            {
                case IFailure { Kind: FailureKind.Daemon } f:
                    // A daemon error without the error is nothing to read.
                    return f.DaemonError is { } daemon ? (FailureKind.Daemon, daemon) : (FailureKind.Failed, null);
                case IFailure f:
                    return (f.Kind, null);
                case TimeoutException or OperationCanceledException:
                    return (FailureKind.TimedOut, null);
            }
        }
        return (FailureKind.Failed, null);
    }

    /// <summary>
    /// The daemon's error a failed call carries (Swift <c>error as?
    /// RPCError</c>, Go <c>errors.As(err, &amp;e)</c>); null for a failure of
    /// the client or anything else.
    /// </summary>
    public static RpcError? DaemonError(Exception? error) => Classify(error).Error;
}
