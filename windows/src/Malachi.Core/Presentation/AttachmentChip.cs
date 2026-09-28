// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/AttachmentChipView.swift
// (init: the label, the size, the tooltips by the part's state, nesting and
// executability, the server symbol or the spinner of a part kept on the
// mail server; showMenu: View for an attached message, Open disabled for a
// program) and MessageViewController.swift (renderAttachments); GTK:
// ui/internal/window/attachments.go (renderAttachments, buildChip,
// remoteIndicator, chipMenu, buildSaveAll). What one chip shows and allows,
// decided here so the WinUI split button only draws it. Names and types are server data:
// plain text. A program is judged by the platform's policy (DangerousTypes
// and AssocIsDangerous, IFileTypePolicy) where GTK has
// executableAttachment and macOS its name lists and UTType conformance.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Text;

namespace Malachi.Core.Presentation;

/// <summary>One attachment under the headers of a message.</summary>
/// <param name="Attachment">What the chip stands for.</param>
/// <param name="Message">The message it belongs to (the containing message's summary in an attached message's view).</param>
/// <param name="Name">Its whole name (<see cref="AttachmentChips.ChipName"/>).</param>
/// <param name="Label">The name on the chip, its middle cut at <see cref="AttachmentChips.ChipNameChars"/>.</param>
/// <param name="SizeText">Its size, "" when unknown.</param>
/// <param name="Tooltip">The tooltip of its main part.</param>
/// <param name="ArrowTooltip">The tooltip of its arrow.</param>
/// <param name="State">
/// What <c>message.part</c> can do with it (<see cref="AttachmentChips.PartStateOf"/>):
/// a chip that cannot have its part (<see cref="PartState.Waiting"/>,
/// <see cref="PartState.Unavailable"/>) is disabled; one on the mail server
/// only (<see cref="PartState.Remote"/>) is enabled, and its actions download
/// the message first.
/// </param>
/// <param name="Nested">An attached message: a click shows it in its own window, the menu adds View.</param>
/// <param name="CanOpen">Whether Open applies: never for a program or script.</param>
/// <param name="ServerTooltip">
/// The tooltip of the server symbol after the size of a part on the mail
/// server only; "" for any other part.
/// </param>
/// <param name="Downloading">
/// The message is being downloaded: the chip of a part on the mail server
/// shows a spinner instead of the server symbol.
/// </param>
public sealed record AttachmentChip(
    Attachment Attachment,
    MessageSummary Message,
    string Name,
    string Label,
    string SizeText,
    string Tooltip,
    string ArrowTooltip,
    PartState State,
    bool Nested,
    bool CanOpen,
    string ServerTooltip = "",
    bool Downloading = false)
{
    /// <summary>
    /// Whether the chip's actions can run: the part is stored, or on the
    /// mail server and downloaded on the way.
    /// </summary>
    public bool Available => State is PartState.Local or PartState.Remote;

    /// <summary>
    /// Whether the part is on the mail server only (attachments.go
    /// <c>partRemote</c>): the chip shows the server symbol or the spinner,
    /// and every action downloads the message first.
    /// </summary>
    public bool OnServer => State == PartState.Remote;

    /// <summary>
    /// The chip of <paramref name="a"/> (attachments.go <c>buildChip</c>):
    /// <paramref name="state"/> and <paramref name="why"/> as
    /// <see cref="AttachmentChips.PartStateOf"/> says (or the reason of an
    /// attached message's parts), <paramref name="dangerous"/> whether the
    /// platform would run it, <paramref name="downloading"/> whether the
    /// message is being downloaded (<see cref="Controllers.IReaderCache.ShowsDownload"/>).
    /// </summary>
    public static AttachmentChip For(Attachment a, MessageSummary message, PartState state, string why, bool dangerous, bool downloading = false)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(message);
        ArgumentNullException.ThrowIfNull(why);
        var name = AttachmentChips.ChipName(a);
        var nested = AttachmentChips.AttachedMessage(a);
        var available = state is PartState.Local or PartState.Remote;
        string tooltip;
        var arrow = L10n.T("More Actions");
        if (!available)
        {
            tooltip = why;
            arrow = why;
        }
        else if (nested)
        {
            // Rendered by the daemon, read-only: the safest thing to do with
            // it, whatever it is called.
            tooltip = name;
        }
        else if (dangerous)
        {
            // The preview never runs it; Open stays disabled in the menu.
            tooltip = L10n.T("Programs and scripts are not opened directly; save the file and decide yourself.");
        }
        else
        {
            tooltip = name;
        }
        var remote = state == PartState.Remote;
        return new AttachmentChip(
            a, message, name,
            ChipText.MiddleEllipsis(name, AttachmentChips.ChipNameChars),
            a.Size > 0 ? Format.FormatSize(a.Size) : "",
            tooltip, arrow, state, nested, !dangerous,
            remote ? why : "",
            remote && downloading);
    }

    /// <summary>
    /// The chips of a message view (attachments.go <c>renderAttachments</c>):
    /// nothing until <c>message.get</c> answered, otherwise every attachment
    /// but the pictures the HTML body on display shows; in an attached
    /// message's view (<paramref name="nestedView"/>) every chip is
    /// unavailable, its parts having no numbers. Save All follows with two
    /// or more chips that are all available, stored or on the mail server
    /// (<see cref="AttachmentChips.AnyRemote"/> says whether it downloads
    /// first). <paramref name="downloading"/>: the message is being
    /// downloaded, the chips of its parts on the server show the spinner.
    /// </summary>
    public static (IReadOnlyList<AttachmentChip> Chips, IReadOnlyList<Attachment> SaveAll) For(
        MessageSummary s, LoadedMessage? lm, bool nestedView, IFileTypePolicy? policy, bool downloading = false)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (lm?.Msg is not { } m)
        {
            return ([], []);
        }
        var atts = AttachmentChips.ChipAttachments(m.Attachments, lm.Body);
        if (atts.Count == 0)
        {
            return ([], []);
        }
        var chips = new List<AttachmentChip>(atts.Count);
        var allOk = true;
        foreach (var a in atts)
        {
            var (state, why) = AttachmentChips.PartStateOf(a, lm.Body);
            if (nestedView)
            {
                // The parts of an attached message have no numbers; nothing
                // can fetch them (message.embedded in docs/api.md).
                state = PartState.Unavailable;
                why = L10n.T("Files inside an attached message cannot be opened or saved yet.");
            }
            allOk &= state is PartState.Local or PartState.Remote;
            var dangerous = policy?.IsDangerous(a.Filename, a.ContentType) ?? DangerousTypes.IsDangerous(a.Filename, a.ContentType);
            chips.Add(For(a, s, state, why, dangerous, downloading));
        }
        return (chips, atts.Count >= 2 && allOk ? atts : []);
    }
}
