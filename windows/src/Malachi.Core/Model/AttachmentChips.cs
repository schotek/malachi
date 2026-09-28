// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AttachmentChips.swift
// (chipNameChars, chipAttachments, partState, anyRemote, partAfterDownload,
// attachedMessage, chipName, chipIconType, fileName, saveAllSummary); GTK:
// ui/internal/window/attachments.go (the same names), download.go
// (partAfterDownload) and embedded.go (attachedMessage). PartState, the
// enum of that Swift file, has a file of its own.
//
// The safety parts of that Swift file live in Malachi.Core.Platform and are
// used from there, not repeated: DangerousTypes.IsDangerous is
// executableExtensions, executableTypes and executableAttachment (with what
// Windows adds); WindowsFileNames is safeFileName, uniqueName and the name
// rules behind fileName; OpenDir.OpenMaxAge is openMaxAge. claimedTypes,
// the UTType conformance check of AttachmentActions.swift, is the Windows
// file-type policy (IFileTypePolicy: AssocIsDangerous over DangerousTypes).
// chipIconType answers a media type, as GTK does, where Swift answers a
// UTType; the guess from the file name is the platform's
// (contentTypeOfName), as gio.ContentTypeGuess is GTK's.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Platform;
using Malachi.Core.Text;

namespace Malachi.Core.Model;

/// <summary>
/// The pure helpers behind the attachment chips. Names and types are server
/// data and are shown as plain text; programs and scripts are never opened
/// directly (docs/security.md §4, <see cref="IFileTypePolicy"/>); the
/// content comes through <c>message.part</c>, so a part over
/// <see cref="API.Limits.MaxAttachmentDataBytes"/> is out of reach, and a
/// part kept on the mail server comes after <c>message.download</c>
/// (<see cref="PartStateOf"/>, <see cref="Download"/>).
/// </summary>
public static class AttachmentChips
{
    /// <summary>
    /// Bounds the file name on a chip, in characters; the middle is elided so
    /// the extension survives (attachments.go <c>chipNameChars</c>).
    /// </summary>
    public const int ChipNameChars = 14;

    /// <summary>
    /// The media type of a part that says nothing about itself, and the icon
    /// type of last resort.
    /// </summary>
    public const string OctetStream = "application/octet-stream";

    /// <summary>
    /// What gets a chip (attachments.go <c>chipAttachments</c>): every
    /// attachment, minus the parts the HTML on display shows inline (the cid:
    /// references the sanitiser kept, <c>inlineParts</c>). A text-only
    /// message, withheld HTML or an unreferenced Content-ID leaves the part
    /// listed like any other file.
    /// </summary>
    public static IReadOnlyList<Attachment> ChipAttachments(IReadOnlyList<Attachment> atts, MessageBodyResult? b)
    {
        ArgumentNullException.ThrowIfNull(atts);
        var html = LoadedMessageText.ShowsHtml(b);
        return atts.Where(a =>
            !(html && !string.IsNullOrEmpty(a.ContentId) && a.PartId.Length > 0
              && b!.InlineParts is { } inline && inline.TryGetValue(a.ContentId, out var part)
              && string.Equals(part, a.PartId, StringComparison.Ordinal))).ToList();
    }

    /// <summary>
    /// The state of <paramref name="a"/> and, for the chip's tooltip, why
    /// (attachments.go <c>partState</c>): empty while the body is on its way
    /// and for a stored part. The daemon reads parts from the stored raw
    /// message only, never beyond
    /// <see cref="API.Limits.MaxAttachmentDataBytes"/>; a part kept on the
    /// server, or any part of a body not downloaded yet, comes after
    /// <c>message.download</c>. Whether a part is on the server is the
    /// daemon's word (<see cref="Attachment.Remote"/>); the size is exact
    /// once the body is fetched (before that it is the transfer size from
    /// BODYSTRUCTURE). The order of the checks is the point: a message too
    /// large or unreadable, then a part over the cap, win over the server.
    /// </summary>
    public static (PartState State, string Why) PartStateOf(Attachment a, MessageBodyResult? b)
    {
        ArgumentNullException.ThrowIfNull(a);
        if (b is null)
        {
            return (PartState.Waiting, "");
        }
        switch (b.BodyState.Value)
        {
            case BodyState.TooBig:
                return (PartState.Unavailable, L10n.T("This message is too large to download."));
            case BodyState.Failed:
                return (PartState.Unavailable, L10n.T("This message could not be read."));
        }
        if (a.Size > API.Limits.MaxAttachmentDataBytes)
        {
            // TRANSLATORS: %s is a size such as "16.0 MiB".
            return (PartState.Unavailable, L10n.T("Attachments over %s cannot be opened or saved yet.", Format.FormatSize(API.Limits.MaxAttachmentDataBytes)));
        }
        if (a.IsRemote || b.BodyState == BodyState.Pending)
        {
            // TRANSLATORS: tooltip of the server icon on an attachment; "it" is the attachment.
            return (PartState.Remote, L10n.T("On the server only; it is downloaded when you open it"));
        }
        if (b.BodyState == BodyState.Fetched)
        {
            return (PartState.Local, "");
        }
        return (PartState.Waiting, "");
    }

    /// <summary>
    /// Whether any of <paramref name="atts"/> has to be downloaded first
    /// (attachments.go <c>anyRemote</c>): Save All then downloads the message
    /// once.
    /// </summary>
    public static bool AnyRemote(IReadOnlyList<Attachment> atts, MessageBodyResult? b)
    {
        ArgumentNullException.ThrowIfNull(atts);
        return atts.Any(a => PartStateOf(a, b).State == PartState.Remote);
    }

    /// <summary>
    /// The attachment <paramref name="a"/> stands for in
    /// <paramref name="m"/>, the message a download answered with (download.go
    /// <c>partAfterDownload</c>): Microsoft 365 rebuilds the message, so a
    /// part id may have moved, and a stale id could hand back another part.
    /// The attachment of <paramref name="m"/> with <paramref name="a"/>'s part
    /// id, file name and type; else the only one with its file name and type;
    /// else null, not found: nothing may be fetched under the old id (the MCP
    /// bridge refuses the same way). Without a message (nothing was
    /// downloaded), <paramref name="a"/>.
    /// </summary>
    public static Attachment? PartAfterDownload(Attachment a, Message? m)
    {
        ArgumentNullException.ThrowIfNull(a);
        if (m is null)
        {
            return a;
        }
        bool Same(Attachment b) =>
            string.Equals(b.Filename, a.Filename, StringComparison.Ordinal)
            && string.Equals(b.ContentType, a.ContentType, StringComparison.Ordinal);
        Attachment? match = null;
        var n = 0;
        foreach (var b in m.Attachments)
        {
            if (!Same(b))
            {
                continue;
            }
            if (string.Equals(b.PartId, a.PartId, StringComparison.Ordinal))
            {
                return b;
            }
            match = b;
            n++;
        }
        return n == 1 ? match : null;
    }

    /// <summary>
    /// Whether the attachment is a message (embedded.go
    /// <c>attachedMessage</c>): by its claimed type or its <c>.eml</c> name.
    /// </summary>
    public static bool AttachedMessage(Attachment a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return string.Equals(DangerousTypes.MediaType(a.ContentType), "message/rfc822", StringComparison.OrdinalIgnoreCase)
            || a.Filename.Trim().EndsWith(".eml", StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>
    /// The chip's label: the sanitised file name, or a placeholder for a part
    /// without one (attachments.go <c>chipName</c>). The daemon's name
    /// sanitiser already removes control and bidi characters; the name is
    /// cleaned for display again all the same (<see cref="DisplayText.CleanTrimmed"/>),
    /// as every mail text the app shows, since it also names the previewer's
    /// window; a name that draws nothing takes the placeholder.
    /// </summary>
    public static string ChipName(Attachment a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var n = DisplayText.CleanTrimmed(a.Filename);
        return n.Length > 0 ? n : L10n.T("Attachment");
    }

    /// <summary>
    /// The media type the chip's icon is chosen by (attachments.go
    /// <c>chipIconType</c>): the claimed one, in lower case and without its
    /// parameters, or a guess from the file name when the sender said
    /// nothing useful (no type, or <see cref="OctetStream"/>);
    /// <see cref="OctetStream"/> as the last resort.
    /// <paramref name="contentTypeOfName"/> is the platform's guess from a
    /// name (the <c>Content Type</c> Windows registers for its extension),
    /// null or empty when it has none.
    /// </summary>
    public static string ChipIconType(Attachment a, Func<string, string?>? contentTypeOfName = null)
    {
        ArgumentNullException.ThrowIfNull(a);
        var ct = DangerousTypes.MediaType(a.ContentType).ToLowerInvariant();
        if ((ct.Length == 0 || ct == OctetStream) && contentTypeOfName?.Invoke(a.Filename.Trim()) is { } guess)
        {
            guess = DangerousTypes.MediaType(guess).ToLowerInvariant();
            if (guess.Length > 0)
            {
                ct = guess;
            }
        }
        return ct.Length > 0 ? ct : OctetStream;
    }

    /// <summary>
    /// The name a fetched part is written under (attachments.go
    /// <c>fileName</c>): what <c>message.part</c> reported, else the listed
    /// name, else <see cref="WindowsFileNames.Fallback"/>, each made a name
    /// Windows keeps (<see cref="WindowsFileNames.FileName"/>) with this
    /// machine's <paramref name="lookAlikes"/>.
    /// </summary>
    public static string FileName(MessagePartResult? res, Attachment a, IReadOnlySet<char>? lookAlikes = null)
    {
        ArgumentNullException.ThrowIfNull(a);
        return WindowsFileNames.FileName(res?.Filename, a.Filename, lookAlikes);
    }

    /// <summary>The toast after Save All (attachments.go <c>saveAllSummary</c>).</summary>
    public static string SaveAllSummary(int saved, int failed)
    {
        if (failed == 0)
        {
            // TRANSLATORS: %d is the number of files written.
            return L10n.N("Saved %d attachment", "Saved %d attachments", saved);
        }
        // TRANSLATORS: the first %d is how many failed, the second how many there were.
        return L10n.T("%d of %d attachments could not be saved", failed, saved + failed);
    }

    /// <summary>
    /// The toast after a Save All that left out <paramref name="skipped"/>
    /// attachments the file-type policy names (<see cref="IFileTypePolicy"/>),
    /// and how one of them is saved all the same: on its own, with Save As.
    /// </summary>
    public static string SaveAllSkipped(int skipped)
    {
        // Windows-only string: GTK saves every attachment (windows/README.md).
        return L10n.N(
            "%d attachment was not saved; save programs and scripts with Save As…",
            "%d attachments were not saved; save programs and scripts with Save As…",
            skipped);
    }
}
