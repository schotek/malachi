// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AttachmentChips.swift
// (chipNameChars, chipAttachments, partAvailable, attachedMessage, chipName,
// chipIconType, fileName, saveAllSummary); GTK: ui/internal/window/
// attachments.go (the same names) and embedded.go (attachedMessage).
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
/// <see cref="API.Limits.MaxAttachmentDataBytes"/> is out of reach.
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
    /// Whether <c>message.part</c> can deliver <paramref name="a"/>, and if
    /// not why, as the chip's tooltip (empty while the body is still on its
    /// way; attachments.go <c>partAvailable</c>). The daemon reads parts from
    /// the stored raw message only, and never beyond
    /// <see cref="API.Limits.MaxAttachmentDataBytes"/>; the size is exact
    /// once the body is fetched.
    /// </summary>
    public static (bool Ok, string Why) PartAvailable(Attachment a, MessageBodyResult? b)
    {
        ArgumentNullException.ThrowIfNull(a);
        if (b is null)
        {
            return (false, "");
        }
        switch (b.BodyState.Value)
        {
            case BodyState.Fetched:
                break;
            case BodyState.Pending:
                return (false, L10n.T("This message has not been downloaded yet."));
            case BodyState.TooBig:
                return (false, L10n.T("This message is too large to download."));
            case BodyState.Failed:
                return (false, L10n.T("This message could not be read."));
            default:
                return (false, "");
        }
        if (a.Size > API.Limits.MaxAttachmentDataBytes)
        {
            // TRANSLATORS: %s is a size such as "16.0 MiB".
            return (false, L10n.T("Attachments over %s cannot be opened or saved yet.", Format.FormatSize(API.Limits.MaxAttachmentDataBytes)));
        }
        return (true, "");
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
    /// cleaned for display again all the same (<see cref="DisplayText.Clean"/>),
    /// as every mail text the app shows, since it also names the previewer's
    /// window.
    /// </summary>
    public static string ChipName(Attachment a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var n = DisplayText.Clean(a.Filename).Trim();
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
