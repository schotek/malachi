// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/AttachmentChipView.swift
// (init: the label, the size, the tooltips by availability, nesting and
// executability; showMenu: View for an attached message, Open disabled for
// a program) and MessageViewController.swift (renderAttachments); GTK:
// ui/internal/window/attachments.go (renderAttachments, buildChip,
// chipMenu, buildSaveAll). What one chip shows and allows, decided here so
// the WinUI split button only draws it. Names and types are server data:
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
/// <param name="Available">Whether <c>message.part</c> can deliver it; an unavailable chip is disabled.</param>
/// <param name="Nested">An attached message: a click shows it in its own window, the menu adds View.</param>
/// <param name="CanOpen">Whether Open applies: never for a program or script.</param>
public sealed record AttachmentChip(
    Attachment Attachment,
    MessageSummary Message,
    string Name,
    string Label,
    string SizeText,
    string Tooltip,
    string ArrowTooltip,
    bool Available,
    bool Nested,
    bool CanOpen)
{
    /// <summary>
    /// The chip of <paramref name="a"/> (attachments.go <c>buildChip</c>):
    /// <paramref name="available"/> and <paramref name="why"/> as
    /// <see cref="AttachmentChips.PartAvailable"/> says (or the reason of an
    /// attached message's parts), <paramref name="dangerous"/> whether the
    /// platform would run it.
    /// </summary>
    public static AttachmentChip For(Attachment a, MessageSummary message, bool available, string why, bool dangerous)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(message);
        ArgumentNullException.ThrowIfNull(why);
        var name = AttachmentChips.ChipName(a);
        var nested = AttachmentChips.AttachedMessage(a);
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
        return new AttachmentChip(
            a, message, name,
            ChipText.MiddleEllipsis(name, AttachmentChips.ChipNameChars),
            a.Size > 0 ? Format.FormatSize(a.Size) : "",
            tooltip, arrow, available, nested, !dangerous);
    }

    /// <summary>
    /// The chips of a message view (attachments.go <c>renderAttachments</c>):
    /// nothing until <c>message.get</c> answered, otherwise every attachment
    /// but the pictures the HTML body on display shows; in an attached
    /// message's view (<paramref name="nestedView"/>) every chip is
    /// unavailable, its parts having no numbers. Save All follows with two
    /// or more chips that are all available.
    /// </summary>
    public static (IReadOnlyList<AttachmentChip> Chips, IReadOnlyList<Attachment> SaveAll) For(
        MessageSummary s, LoadedMessage? lm, bool nestedView, IFileTypePolicy? policy)
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
            var (ok, why) = AttachmentChips.PartAvailable(a, lm.Body);
            if (nestedView)
            {
                // The parts of an attached message have no numbers; nothing
                // can fetch them (message.embedded in docs/api.md).
                ok = false;
                why = L10n.T("Files inside an attached message cannot be opened or saved yet.");
            }
            allOk &= ok;
            var dangerous = policy?.IsDangerous(a.Filename, a.ContentType) ?? DangerousTypes.IsDangerous(a.Filename, a.ContentType);
            chips.Add(For(a, s, ok, why, dangerous));
        }
        return (chips, atts.Count >= 2 && allOk ? atts : []);
    }
}
