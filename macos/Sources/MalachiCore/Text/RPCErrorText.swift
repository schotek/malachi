// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The user-facing sentences for failed calls (ui/internal/widget/rpc.go).
// The backend's `message` is technical English and only ever shown as a
// trailing detail; the backend itself stays language-neutral.

/// Turns a client error into a short sentence (rpc.go `RPCErrorText`).
/// `what` is the (already translated) action in progressive form, e.g.
/// `L10n.T("Saving the draft")`. A lost or missing connection is
/// `RPCClient.ClientError.notConnected` / `.disconnected` (the GTK client's
/// `ErrDisconnected`); a timeout is `.timeout` or a cancelled task (the GTK
/// client's `context.DeadlineExceeded`). nil, which a typed nil `*api.Error`
/// becomes on the Go side, is the plain "failed".
public func rpcErrorText(_ what: String, _ error: (any Error)?) -> String {
    if let e = error as? RPCClient.ClientError {
        switch e {
        case .notConnected, .disconnected:
            return L10n.T("%s needs a running mail backend", what)
        case .timeout:
            return L10n.T("%s timed out", what)
        case .transport:
            break
        }
    }
    if error is CancellationError {
        return L10n.T("%s timed out", what)
    }
    if let e = error as? RPCError {
        switch e.code {
        case .notImplemented:
            return L10n.T("%s is not available yet", what)
        case .conflict:
            return L10n.T("%s conflicted with another change", what)
        case .invalidArgument:
            return L10n.T("%s was rejected: %s", what, e.message)
        case .draftNotFound:
            return L10n.T("The draft no longer exists")
        case .attachmentNotFound:
            return L10n.T("The attachment no longer exists")
        case .attachmentTooBig:
            return L10n.T("The attachment is too big")
        case .sanitizeFailed:
            return L10n.T("%s failed: formatted text cannot be saved yet", what)
        case .accountNotFound:
            return L10n.T("%s failed: unknown account", what)
        case .keyringError:
            return L10n.T("%s failed: the system keyring is unavailable", what)
        case .authFailed:
            return L10n.T("%s failed: the server rejected the user name or password", what)
        case .networkError:
            return L10n.T("%s failed: the server could not be reached", what)
        case .serverError:
            return L10n.T("%s failed: the server returned an error", what)
        case .tlsError:
            return L10n.T("%s failed: the secure connection could not be established", what)
        case .serverTimeout:
            return L10n.T("%s failed: the server did not respond in time", what)
        default:
            break
        }
    }
    return L10n.T("%s failed", what)
}

/// The sentence for one endpoint of account.test (rpc.go
/// `EndpointErrorText`).
public func endpointErrorText(_ e: RPCError?) -> String {
    guard let e else {
        return L10n.T("Failed")
    }
    switch e.code {
    case .authFailed:
        return L10n.T("The server rejected the user name or password")
    case .networkError:
        return L10n.T("The server could not be reached")
    case .serverError:
        return L10n.T("The server returned an error")
    case .tlsError:
        return L10n.T("The secure connection could not be established")
    case .serverTimeout:
        return L10n.T("The server did not respond in time")
    case .notImplemented:
        return L10n.T("Not supported yet")
    case .authRequired:
        return L10n.T("Sign in to this account again")
    case .unavailable:
        return L10n.T("The sign-in service is not available")
    case .invalidArgument:
        // TRANSLATORS: %s is a technical message from the mail backend.
        return L10n.T("Rejected: %s", e.message)
    default:
        // TRANSLATORS: %s is a technical message from the mail backend.
        return L10n.T("Failed: %s", e.message)
    }
}
