// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: how marking a file written out of a message ended
// (IMarkOfTheWeb). The counterpart of the read-back of the quarantine
// attribute in macos/Sources/MalachiMail/Attachments/AttachmentActions.swift
// (quarantine), which knows only "stuck" and "did not stick".

namespace Malachi.Core.Platform;

/// <summary>How marking a file with the Mark of the Web ended.</summary>
public enum ZoneMarkOutcome
{
    /// <summary>
    /// Attachment Services checked the file (the antivirus scan and the
    /// attachment policy) and wrote its zone, which reads back.
    /// </summary>
    Marked,

    /// <summary>
    /// The zone was written directly and reads back: Attachment Services is
    /// not there to use (it cannot be created, or not told about the file),
    /// so there was no scan through it either, or its check passed without
    /// writing a zone.
    /// </summary>
    MarkedDirectly,

    /// <summary>
    /// Attachment Services gave a verdict against the file: an antivirus
    /// reported it, or the attachment policy blocks its type. The file is
    /// still there (marked directly where the stream could be written); it
    /// must never be opened.
    /// </summary>
    Rejected,

    /// <summary>
    /// Attachment Services ran, but its check failed without a verdict
    /// (access denied, an antivirus that could not scan): the zone was
    /// written directly where it could be. The file may not have been
    /// scanned, so a file for opening is not opened; a saved file is the
    /// user's all the same.
    /// </summary>
    CheckFailed,

    /// <summary>
    /// The file is gone after the check: an antivirus or the attachment
    /// policy removed it.
    /// </summary>
    Removed,

    /// <summary>
    /// The administrator turned zone information off (the Attachment
    /// Manager policy <c>SaveZoneInformation</c> = 1): the file was checked,
    /// but no zone is written, and none is written directly either.
    /// </summary>
    PolicyDisabled,

    /// <summary>
    /// No zone reads back and none could be written (a file system without
    /// alternate data streams: FAT32, exFAT, some network shares).
    /// </summary>
    NotMarked,
}
