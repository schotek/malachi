// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the quarantine of macos/Sources/MalachiMail/Attachments/
// AttachmentActions.swift (quarantine, writeForViewing, writeUnique: the
// attribute is set, read back, and a file for opening without it is not
// opened; a saved file is the user's regardless); the GTK UI has no such
// mark. On Windows it is the Mark of the Web through Attachment Services
// (docs/windows-port.md §10, the deviation row of windows/README.md), which
// also runs the antivirus check and the attachment policy.
//
// The zone, measured on Windows 11 26100 with the default attachment
// policy: Microsoft's e-mail client sample calls no SetSource ("email
// sources are not verifiable"), and Save then writes ZoneId=4 (Restricted
// sites). That zone's policy blocks the high-risk types (CheckPolicy fails
// with INET_E_SECURITY_PROBLEM for exactly what AssocIsDangerous names:
// .exe .js .ps1 .hta .lnk .url .iso .vhdx .msi …) and Save deletes such a
// file outright. Files for opening are never of those types, so they get
// zone 4. A saved file of such a type, which the user asked for, is given
// the source "about:internet" instead, as Chromium does: Save keeps it,
// still scans it, and writes ZoneId=3 with HostUrl=about:internet, so
// running it goes through SmartScreen and the security prompt.
//
// The name judged is always the path's own: measured, a Save of
// "probe3.exe" told it was "probe3.pdf" got the Restricted zone from the
// policy check of the .pdf, and Save then deleted the .exe.
//
// Where Attachment Services cannot be created or told about the file (the
// class is missing), the zone is written directly and the file may open,
// as it would without the service. Where Save ran and failed without a
// verdict, the stream is written directly too, but the file may not have
// been scanned, so a file for opening is not opened: the direct write then
// serves what the user saved (Save As, Save All), which stays theirs.

using System;
using System.Collections.Frozen;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Platform;
using Malachi.Platform.Windows.Launch;
using Windows.Win32.Foundation;

namespace Malachi.Platform.Windows.Attachments;

/// <summary>
/// Marks the files written out of a message with the Mark of the Web
/// through <c>IAttachmentExecute</c> on an STA thread, reads the
/// <c>Zone.Identifier</c> stream back, and writes it directly where
/// Attachment Services could not.
/// </summary>
public sealed class MarkOfTheWeb : IMarkOfTheWeb
{
    /// <summary>
    /// Malachi Mail's client GUID for Attachment Services, which keeps the
    /// prompts a user chose not to see again under it. Fixed forever.
    /// </summary>
    public static readonly Guid ClientGuid = new("470b4a3e-f3ee-4164-9315-5265b7395b63");

    /// <summary>
    /// The source that puts a file in the Internet zone, as Chromium names
    /// it when it has no usable address.
    /// </summary>
    public const string InternetSource = "about:internet";

    // Save's verdicts: the attachment policy blocked the file, or an
    // antivirus reported it (E_FAIL, as Chromium reads it, and the Win32
    // errors of a virus found or deleted).
    private static readonly FrozenSet<int> Verdicts = new[]
    {
        HRESULT.INET_E_SECURITY_PROBLEM.Value,
        unchecked((int)0x80004005), // E_FAIL
        unchecked((int)0x800700E1), // HRESULT_FROM_WIN32(ERROR_VIRUS_INFECTED)
        unchecked((int)0x800700E2), // HRESULT_FROM_WIN32(ERROR_VIRUS_DELETED)
    }.ToFrozenSet();

    private readonly IAttachmentServices services;
    private readonly Func<bool> zoneInformationDisabled;

    /// <summary>Attachment Services and the machine's policy.</summary>
    public MarkOfTheWeb()
        : this(new AttachmentExecute(), ZoneInformationPolicy.Disabled)
    {
    }

    internal MarkOfTheWeb(IAttachmentServices services, Func<bool> zoneInformationDisabled)
    {
        this.services = services;
        this.zoneInformationDisabled = zoneInformationDisabled;
    }

    /// <inheritdoc/>
    public Task<ZoneMark> MarkAsync(string path, AttachmentUse use, CancellationToken cancellationToken = default)
    {
        CheckPath(path);
        return StaThread.RunAsync(() => Mark(path, use), cancellationToken);
    }

    /// <summary>
    /// Whether the attachment policy of the Restricted sites zone, the zone
    /// of mail, blocks a file of this name: <c>CheckPolicy</c> fails (with
    /// <c>INET_E_SECURITY_PROBLEM</c>). It prompts for everything else
    /// (<c>S_FALSE</c>), and cannot tell prompting from allowing apart once
    /// the method is projected, so "blocked or not" is what it says.
    /// </summary>
    public Task<bool> PolicyBlocksAsync(string fileName, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(fileName);
        return StaThread.RunAsync(() => services.PolicyBlocks(fileName), cancellationToken);
    }

    /// <summary>The work of <see cref="MarkAsync"/>, on the calling STA thread.</summary>
    internal ZoneMark Mark(string path, AttachmentUse use)
    {
        // The file's own name, for the policy check and Save alike.
        var fileName = Path.GetFileName(path);
        var internet = use == AttachmentUse.Save && services.PolicyBlocks(fileName);
        var source = internet ? InternetSource : null;
        var zone = internet ? ZoneIdentifier.Internet : ZoneIdentifier.Restricted;
        var save = services.Save(path, fileName, source);
        if (!File.Exists(path))
        {
            return new ZoneMark { Outcome = ZoneMarkOutcome.Removed, SaveResult = save.HResult };
        }
        var verdict = save.Ran && Verdicts.Contains(save.HResult);
        var failed = save.Ran && save.HResult != 0 && !verdict;
        var read = ZoneIdentifier.Read(path);
        if (zoneInformationDisabled())
        {
            // The administrator's choice: nothing written behind it.
            return new ZoneMark
            {
                Outcome = verdict ? ZoneMarkOutcome.Rejected
                    : failed ? ZoneMarkOutcome.CheckFailed
                    : ZoneMarkOutcome.PolicyDisabled,
                ZoneId = read,
                SaveResult = save.HResult,
            };
        }
        if (save == AttachmentSaveResult.Saved && IsMark(read))
        {
            return new ZoneMark { Outcome = ZoneMarkOutcome.Marked, ZoneId = read, SaveResult = save.HResult };
        }
        if (!IsMark(read))
        {
            try
            {
                ZoneIdentifier.Write(path, zone, source);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException or NotSupportedException)
            {
                // A file system without streams; the read-back says so.
            }
            read = ZoneIdentifier.Read(path);
        }
        var outcome = verdict ? ZoneMarkOutcome.Rejected
            : failed ? ZoneMarkOutcome.CheckFailed
            : IsMark(read) ? ZoneMarkOutcome.MarkedDirectly
            : ZoneMarkOutcome.NotMarked;
        return new ZoneMark { Outcome = outcome, ZoneId = read, SaveResult = save.HResult };
    }

    // A zone that marks the file as coming from outside: Internet or
    // Restricted sites.
    private static bool IsMark(int? zone) => zone is ZoneIdentifier.Internet or ZoneIdentifier.Restricted;

    // A file's own content: the stream is written beside it, so the path
    // cannot name a stream itself. The messages leave the path out: it
    // carries the attachment's name, which is mail content.
    private static void CheckPath(string path)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        if (!Path.IsPathFullyQualified(path))
        {
            throw new ArgumentException("not a fully qualified path", nameof(path));
        }
        var name = Path.GetFileName(path);
        if (name.Length == 0)
        {
            throw new ArgumentException("not the path of a file", nameof(path));
        }
        if (name.Contains(':', StringComparison.Ordinal))
        {
            throw new ArgumentException("an alternate data stream, not a file", nameof(path));
        }
    }
}
