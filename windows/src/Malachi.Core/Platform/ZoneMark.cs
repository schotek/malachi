// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the result of marking a file written out of a message
// (IMarkOfTheWeb). The rules of macos/Sources/MalachiMail/Attachments/
// AttachmentActions.swift (quarantine, writeForViewing): a file for opening
// is not opened unless the mark is on it; a file the user saved is theirs
// regardless.

namespace Malachi.Core.Platform;

/// <summary>What marking a file with the Mark of the Web did.</summary>
public sealed record ZoneMark
{
    /// <summary>How it ended.</summary>
    public required ZoneMarkOutcome Outcome { get; init; }

    /// <summary>
    /// The zone the file's <c>Zone.Identifier</c> stream names afterwards
    /// (3 Internet, 4 Restricted sites), null without one.
    /// </summary>
    public int? ZoneId { get; init; }

    /// <summary>
    /// The HRESULT of Attachment Services: of <c>IAttachmentExecute::Save</c>,
    /// 0 when it succeeded, or of creating it where it could not be created
    /// or told about the file; for the log (a code, never a name).
    /// </summary>
    public int SaveResult { get; init; }

    /// <summary>
    /// Whether a file written for opening may be handed to its application:
    /// the check passed (or there was none to run) and the mark reads back,
    /// or the administrator turned zone information off (then no file could
    /// ever carry one, and the policy is theirs). Never after a verdict or a
    /// check that failed.
    /// </summary>
    public bool MayOpen =>
        Outcome is ZoneMarkOutcome.Marked or ZoneMarkOutcome.MarkedDirectly or ZoneMarkOutcome.PolicyDisabled;

    /// <summary>
    /// Whether the file is still where it was written. A saved file that
    /// is gone counts as not saved (Save All's summary).
    /// </summary>
    public bool FileKept => Outcome is not ZoneMarkOutcome.Removed;
}
