// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Download.swift (isPartNotDownloaded,
// methodUnsupported, partNotFoundAfterDownload, withDownload); GTK:
// ui/internal/window/download.go (partNotDownloaded, methodUnsupported,
// errPartNotFound, withDownload). The pure half of how a part or an
// attached message kept on the mail server is fetched; the download itself
// (message.download, one per message, the spinner) is MessageCache's.
//
// Swift tells the daemon's error by its type (error as? RPCError), Go by
// errors.As; here the daemon's error is an RpcException, as ComposeSources
// tells it, and MethodUnsupported reads it as RpcErrorText does. As in
// Swift, MethodUnsupported serves StorageUsageController as well.

using System;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Text;
using Malachi.Core.Transport;

namespace Malachi.Core.Model;

/// <summary>Attachments on demand: the rules of a fetch that may need a download first.</summary>
public static class Download
{
    // The message of PartNotFoundAfterDownload (Download.swift
    // partNotFoundDetail): technical English, for the log only; like the
    // daemon's messages it is never shown.
    private const string PartNotFoundDetail = "the downloaded message no longer lists the part";

    /// <summary>
    /// What <see cref="WithDownloadAsync{T}"/> throws, as an
    /// <see cref="RpcException"/>, when the downloaded message no longer lists
    /// the attachment (<see cref="AttachmentChips.PartAfterDownload"/> found
    /// none): nothing is fetched, since the old part id may name another file
    /// by now, and the action's toast says that the attachment no longer
    /// exists (partNotFound in <see cref="RpcErrorText"/>; download.go
    /// <c>errPartNotFound</c>).
    /// </summary>
    public static RpcError PartNotFoundAfterDownload { get; } =
        new() { Code = ErrorCode.PartNotFound, Message = PartNotFoundDetail };

    /// <summary>
    /// Whether <paramref name="error"/> is the daemon's partNotDownloaded
    /// (1504): the part is kept on the mail server only,
    /// <c>message.download</c> fetches it (download.go <c>partNotDownloaded</c>).
    /// </summary>
    public static bool IsPartNotDownloaded(Exception? error) =>
        error is RpcException { Code.Value: ErrorCode.PartNotDownloaded };

    /// <summary>
    /// Whether <paramref name="error"/> says the daemon does not offer the
    /// method at all: an older one (methodNotFound) or one without it yet
    /// (notImplemented) (download.go <c>methodUnsupported</c>, which reads
    /// the error as <see cref="RpcErrorText.DaemonError"/> does, through the
    /// wrapping). A forward then goes on without a download, and Disk Space
    /// Used is hidden (<see cref="Controllers.StorageUsageController"/>).
    /// </summary>
    public static bool MethodUnsupported(Exception? error) =>
        RpcErrorText.DaemonError(error)?.Code.Value is ErrorCode.MethodNotFound or ErrorCode.NotImplemented;

    /// <summary>
    /// Fetches something of attachment <paramref name="a"/> that may be on the
    /// mail server only (download.go <c>withDownload</c>):
    /// <paramref name="remote"/> (the chip said so) downloads the message
    /// first and fetches once; otherwise it fetches, and only when the daemon
    /// answers partNotDownloaded (the part went to the server since the chip
    /// was drawn) it downloads once and fetches once more. Any other error,
    /// and a second partNotDownloaded, is thrown as it is: never a loop.
    /// After a download the part is looked up again in the downloaded message
    /// (<see cref="AttachmentChips.PartAfterDownload"/>), since Microsoft 365
    /// may move part ids; when that message no longer lists it, nothing is
    /// fetched and an <see cref="RpcException"/> of
    /// <see cref="PartNotFoundAfterDownload"/> is thrown.
    /// <paramref name="download"/> answers the message after the download
    /// (null when unknown).
    /// </summary>
    public static async Task<T> WithDownloadAsync<T>(
        Attachment a, bool remote, Func<Attachment, Task<T>> fetch, Func<Task<Message?>> download)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(fetch);
        ArgumentNullException.ThrowIfNull(download);
        if (!remote)
        {
            try
            {
                return await fetch(a);
            }
            catch (RpcException e) when (IsPartNotDownloaded(e))
            {
                // On the server since the chip was drawn: one download, one retry.
            }
        }
        var m = await download();
        return await fetch(Listed(a, m));
    }

    // a as the downloaded message m lists it, or the error when it does not.
    private static Attachment Listed(Attachment a, Message? m) =>
        AttachmentChips.PartAfterDownload(a, m) ?? throw new RpcException(PartNotFoundAfterDownload);
}
