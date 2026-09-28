// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/DraftOpen.swift
// (draftOpenUnsupported, draftOpenErrorText, draftSkippedText); GTK:
// ui/internal/window/drafts.go (draftOpenUnsupported, draftOpenErrorText,
// skippedText).

using System;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;
using Malachi.Core.Transport;

namespace Malachi.Core.Model;

/// <summary>
/// The texts of opening a message of the Drafts folder in the compose
/// window through <c>draft.open</c>.
/// </summary>
public static class DraftOpen
{
    /// <summary>
    /// drafts.go <c>draftOpenUnsupported</c>: a daemon that does not offer
    /// <c>draft.open</c> (the message then opens in its own window).
    /// </summary>
    public static bool DraftOpenUnsupported(Exception? error) =>
        error is RpcException { Code.Value: ErrorCode.MethodNotFound or ErrorCode.NotImplemented };

    /// <summary>
    /// drafts.go <c>draftOpenErrorText</c>: a body the syncer has not
    /// downloaded yet is a matter of a moment, anything else the usual
    /// sentence.
    /// </summary>
    public static string DraftOpenErrorText(Exception? error)
    {
        if (error is RpcException { Code.Value: ErrorCode.Unavailable })
        {
            return L10n.T("The draft has not been downloaded yet; try again in a moment");
        }
        return RpcErrorText.Text(L10n.T("Opening the draft"), error);
    }

    /// <summary>
    /// drafts.go <c>skippedText</c>: how many parts of a draft could not be
    /// taken along.
    /// </summary>
    public static string DraftSkippedText(int n) =>
        L10n.N("%d attachment of the draft could not be opened", "%d attachments of the draft could not be opened", n);
}
