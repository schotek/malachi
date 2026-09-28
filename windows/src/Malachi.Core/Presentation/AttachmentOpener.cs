// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Attachments/AttachmentActions.swift
// (open, preview, partData, fetchForViewing, writeForViewing, saveAs,
// saveAll, saveInto, writeUnique, quarantine); GTK: ui/internal/window/
// attachments.go (openAttachment, previewAttachment, writeAttachment,
// launchFile, saveAttachment, saveAllAttachments, saveInto). What a chip's
// click, Open, Save As… and Save All do, minus the pickers and the shell,
// which macOS keeps in AppKit (docs/windows-port.md §7.4, §10). The part
// comes through IReaderCache.PartDataAsync (download.go partData): a part
// the chip showed on the mail server (remote) is downloaded first, and one
// the daemon moved there since the chip was drawn after its
// partNotDownloaded; the chips show the spinner meanwhile.
//
// - Open: a program or script is refused with GTK's toast, judged by the
//   platform's policy (DangerousTypes and AssocIsDangerous) on what the
//   message lists before the fetch, on the name and type the daemon served
//   after it, and on the name the file really got; otherwise the part is
//   written into a fresh private directory of the open directory, marked
//   with the Mark of the Web (the Restricted zone), and handed to its
//   application only when the mark reads back and no check spoke against
//   it (ZoneMark.MayOpen), as macOS opens only a quarantined file.
// - Preview: the bytes, in memory, for the app's own previewer; a program
//   gets its panel only, and its bytes are not even fetched.
// - Save As: the picker (which confirms an overwrite), then the bytes
//   written over the chosen file and marked (a program saved gets the
//   Internet zone, MarkOfTheWeb); only a failure gets a toast.
// - Save All: a folder, then every part one message.part at a time, never
//   overwriting (" (2)", " (3)", …), marked; one summary toast. What the
//   policy names is left out (a Windows deviation, windows/README.md): the
//   shell parses a shortcut, a library or a search connector for its icon
//   and location as soon as the folder is shown (CVE-2025-24054 is one of
//   that class), and the Mark of the Web governs running a file, not that.
//   It is judged on the listed name and type, again on those the daemon
//   served and on the name the file would get; a second toast says how
//   many were left out and that Save As saves one. One run per message at
//   a time: the button is disabled while it lasts (buildSaveAll), and
//   since a re-render rebuilds the button and another view may show the
//   same message, the run is kept here, by message, not on the button.
//   When some are on the mail server (anyRemote) the message is downloaded
//   once after the folder is chosen; if that fails nothing is written and
//   the toast says why, and a part the downloaded message no longer lists
//   (Microsoft 365 renumbers) counts as failed.
//
// A file an antivirus or the attachment policy removed counts as not saved.
// Log lines carry part ids and exception types only: a file's path carries
// the attachment's name, which is mail content.

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Presentation;

/// <summary>Opens, previews and saves attachments.</summary>
public sealed partial class AttachmentOpener
{
    // attachments.go saveInto: a name that appeared between the check and the
    // create is tried again, a few times.
    private const int CreateAttempts = 8;

    private readonly IReaderCache parts;
    private readonly OpenDir openDir;
    private readonly IMarkOfTheWeb mark;
    private readonly IFileTypePolicy policy;
    private readonly ILauncher launcher;
    private readonly IAttachmentPickers pickers;
    private readonly IReadOnlySet<char>? lookAlikes;
    private readonly ILogger logger;

    // The messages whose Save All is on its way (from the folder picker to
    // the summary toast).
    private readonly HashSet<MessageId> savingAll = [];

    /// <param name="parts">Fetches the parts (<c>message.part</c>, after <c>message.download</c> where needed).</param>
    /// <param name="openDir">Where a part is written for opening.</param>
    /// <param name="mark">The Mark of the Web.</param>
    /// <param name="policy">What the platform would run.</param>
    /// <param name="launcher">Hands a written file to its application.</param>
    /// <param name="pickers">Asks where to save.</param>
    /// <param name="lookAlikes">The characters this machine's ANSI code page turns into reserved ones.</param>
    /// <param name="logger">Part ids and exception types only.</param>
    public AttachmentOpener(
        IReaderCache parts,
        OpenDir openDir,
        IMarkOfTheWeb mark,
        IFileTypePolicy policy,
        ILauncher launcher,
        IAttachmentPickers pickers,
        IReadOnlySet<char>? lookAlikes = null,
        ILogger<AttachmentOpener>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(parts);
        ArgumentNullException.ThrowIfNull(openDir);
        ArgumentNullException.ThrowIfNull(mark);
        ArgumentNullException.ThrowIfNull(policy);
        ArgumentNullException.ThrowIfNull(launcher);
        ArgumentNullException.ThrowIfNull(pickers);
        this.parts = parts;
        this.openDir = openDir;
        this.mark = mark;
        this.policy = policy;
        this.launcher = launcher;
        this.pickers = pickers;
        this.lookAlikes = lookAlikes;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
    }

    /// <summary>A toast in the window where the click was (attachments.go <c>say</c>).</summary>
    public Action<object?, string>? Toast { get; set; }

    /// <summary>The native handle of a window, for the shell's dialogs; 0 for none.</summary>
    public Func<object?, nint>? Owner { get; set; }

    /// <summary>A Save All began or ended for the message with this id (<see cref="IsSavingAll"/>).</summary>
    public event EventHandler<MessageId>? SavingAllChanged;

    /// <summary>Whether a Save All of message <paramref name="id"/> is on its way: its button stays disabled.</summary>
    public bool IsSavingAll(MessageId id) => savingAll.Contains(id);

    /// <summary>Whether Open applies to <paramref name="a"/>: never to a program or script.</summary>
    public bool CanOpen(Attachment a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return !policy.IsDangerous(a.Filename, a.ContentType);
    }

    /// <summary>
    /// A chip's Open (attachments.go <c>openAttachment</c>): the part written
    /// to a private file, marked, and handed to its application; a program or
    /// script is refused with a toast. <paramref name="remote"/> (the chip
    /// showed the part on the mail server) downloads the message first.
    /// </summary>
    public async Task OpenAsync(Attachment a, MessageSummary s, bool remote, object? window)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(s);
        if (!CanOpen(a))
        {
            RefuseProgram(window);
            return;
        }
        if (await FetchForViewingAsync(a, s, remote, window) is not { } res)
        {
            return;
        }
        var name = AttachmentChips.FileName(res, a, lookAlikes);
        if (policy.IsDangerous(name, res.ContentType))
        {
            LogServedAsProgram(logger, a.PartId);
            RefuseProgram(window);
            return;
        }
        string path;
        try
        {
            path = await Task.Run(() => openDir.Write(name, res.Data));
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or ArgumentException)
        {
            LogWriteFailed(logger, a.PartId, e.GetType().Name, e.HResult);
            Say(window, L10n.T("The attachment could not be opened"));
            return;
        }
        // The file's own name from here on: the open directory may have
        // shortened the one it was given.
        if (policy.IsDangerous(Path.GetFileName(path), res.ContentType))
        {
            LogServedAsProgram(logger, a.PartId);
            RefuseProgram(window);
            return;
        }
        var zone = await MarkAsync(path, AttachmentUse.Open, a.PartId);
        if (zone is not { MayOpen: true })
        {
            LogNotMarked(logger, a.PartId, zone?.Outcome.ToString() ?? "error", zone?.SaveResult ?? 0);
            Say(window, L10n.T("The attachment could not be opened"));
            return;
        }
        try
        {
            await launcher.OpenFileAsync(path, Owner?.Invoke(window) ?? 0);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogLaunchFailed(logger, a.PartId, e.GetType().Name, e.HResult);
            Say(window, L10n.T("The attachment could not be opened"));
        }
    }

    /// <summary>
    /// A chip's click (attachments.go <c>previewAttachment</c>): the bytes for
    /// the previewer, in memory; a program or script only by its metadata,
    /// judged before the fetch and again on the name and type the daemon
    /// served. <paramref name="remote"/> downloads the message first. The
    /// request carries the part the daemon served (a download on Microsoft
    /// 365 may move its id), so the previewer's Open and Save As… fetch that
    /// one. Null after a failure, which had its toast.
    /// </summary>
    public async Task<PreviewRequest?> PreviewAsync(Attachment a, MessageSummary s, bool remote, object? window)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(s);
        if (!CanOpen(a))
        {
            return new PreviewRequest(a, s, null, null, CanOpen: false);
        }
        if (await FetchForViewingAsync(a, s, remote, window) is not { } res)
        {
            return null;
        }
        var served = res.PartId.Length > 0 ? a with { PartId = res.PartId } : a;
        var name = AttachmentChips.FileName(res, a, lookAlikes);
        if (policy.IsDangerous(name, res.ContentType))
        {
            LogServedAsProgram(logger, a.PartId);
            return new PreviewRequest(served, s, null, null, CanOpen: false);
        }
        return new PreviewRequest(served, s, res.ContentType, res.Data, CanOpen: true);
    }

    /// <summary>
    /// A chip's Save As… (attachments.go <c>saveAttachment</c>): asks where,
    /// then fetches (downloading the message first when
    /// <paramref name="remote"/>), writes over the chosen file and marks it.
    /// Only a failure gets a toast; a dismissal nothing.
    /// </summary>
    public async Task SaveAsAsync(Attachment a, MessageSummary s, bool remote, object? window)
    {
        ArgumentNullException.ThrowIfNull(a);
        ArgumentNullException.ThrowIfNull(s);
        var path = await pickers.PickSaveFileAsync(window, L10n.T("Save Attachment"), AttachmentChips.FileName(null, a, lookAlikes));
        if (string.IsNullOrEmpty(path))
        {
            return; // dismissed
        }
        try
        {
            var res = await parts.PartDataAsync(s.AccountId, s.Id, a, remote);
            await Task.Run(() => Replace(path, res.Data));
            if (await MarkAsync(path, AttachmentUse.Save, a.PartId) is { FileKept: false })
            {
                throw new IOException("the attachment check removed the file");
            }
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogSaveFailed(logger, a.PartId, e.GetType().Name, e.HResult);
            Say(window, RpcErrorText.Text(L10n.T("Saving the attachment"), e));
        }
    }

    /// <summary>
    /// Save All (attachments.go <c>saveAllAttachments</c>): asks for a
    /// folder and writes every attachment into it, one <c>message.part</c> at
    /// a time, never overwriting: a name that exists gets " (2)" and so on.
    /// When some are on the mail server (<paramref name="remote"/>,
    /// <see cref="AttachmentChips.AnyRemote"/>) the message is downloaded
    /// once first; if that fails, nothing is written and the toast says why.
    /// One toast sums it up; nothing after a dismissal. A second Save All of
    /// the same message while the first lasts does nothing. Unlike GTK, what
    /// the file-type policy names is not saved (Save As saves it), and a
    /// toast of its own says how many were left out; when that is every
    /// attachment, no folder is asked for.
    /// </summary>
    public async Task SaveAllAsync(IReadOnlyList<Attachment> atts, MessageSummary s, bool remote, object? window)
    {
        ArgumentNullException.ThrowIfNull(atts);
        ArgumentNullException.ThrowIfNull(s);
        if (!savingAll.Add(s.Id))
        {
            return; // one run per message
        }
        SavingAllChanged?.Invoke(this, s.Id);
        try
        {
            await SaveAllOnceAsync(atts, s, remote, window);
        }
        finally
        {
            savingAll.Remove(s.Id);
            SavingAllChanged?.Invoke(this, s.Id);
        }
    }

    private async Task SaveAllOnceAsync(IReadOnlyList<Attachment> atts, MessageSummary s, bool remote, object? window)
    {
        // What the message lists as a program is left out before the fetch.
        var wanted = new List<Attachment>(atts.Count);
        foreach (var a in atts)
        {
            if (policy.IsDangerous(a.Filename, a.ContentType))
            {
                LogSkipped(logger, a.PartId);
            }
            else
            {
                wanted.Add(a);
            }
        }
        var skipped = atts.Count - wanted.Count;
        if (wanted.Count == 0 && skipped > 0)
        {
            Say(window, AttachmentChips.SaveAllSkipped(skipped));
            return;
        }
        var folder = await pickers.PickFolderAsync(window, L10n.T("Save Attachments"));
        if (string.IsNullOrEmpty(folder))
        {
            return; // dismissed
        }
        Message? downloaded = null; // the message after the download, if there was one
        if (remote)
        {
            try
            {
                downloaded = await parts.DownloadAsync(s.AccountId, s.Id);
            }
            catch (Exception e) when (e is not OutOfMemoryException)
            {
                LogDownloadFailed(logger, e.GetType().Name);
                Say(window, RpcErrorText.Text(L10n.T("Saving the attachments"), e));
                return;
            }
        }
        var saved = 0;
        var failed = 0;
        foreach (var a in wanted)
        {
            try
            {
                // Microsoft 365 may have renumbered the parts; one the
                // downloaded message no longer lists is not fetched.
                var part = AttachmentChips.PartAfterDownload(a, downloaded) ?? throw new RpcException(Download.PartNotFoundAfterDownload);
                if (await SaveIntoAsync(folder, s, part))
                {
                    saved++;
                }
                else
                {
                    skipped++;
                }
            }
            catch (Exception e) when (e is not OutOfMemoryException)
            {
                LogSaveFailed(logger, a.PartId, e.GetType().Name, e.HResult);
                failed++;
            }
        }
        // GTK's summary of what was tried, then what was left out.
        if (saved + failed > 0 || skipped == 0)
        {
            Say(window, AttachmentChips.SaveAllSummary(saved, failed));
        }
        if (skipped > 0)
        {
            Say(window, AttachmentChips.SaveAllSkipped(skipped));
        }
    }

    // attachments.go saveInto: fetches a (the message downloaded already
    // when it had to be; a part the daemon answers partNotDownloaded for
    // after all gets its one download and retry) and creates it in folder
    // under a name that is not taken yet, then marks it. False, with nothing
    // written, when the part turns out a program by the name and type the
    // daemon served or by the name the file would get.
    private async Task<bool> SaveIntoAsync(string folder, MessageSummary s, Attachment a)
    {
        var res = await parts.PartDataAsync(s.AccountId, s.Id, a, onServer: false);
        var name = AttachmentChips.FileName(res, a, lookAlikes);
        if (policy.IsDangerous(name, res.ContentType))
        {
            LogSkippedAfterFetch(logger, a.PartId);
            return false;
        }
        var path = await Task.Run(() => CreateUnique(folder, name, res.Data, n => policy.IsDangerous(n, res.ContentType)));
        if (path is null)
        {
            LogSkippedAfterFetch(logger, a.PartId);
            return false;
        }
        if (await MarkAsync(path, AttachmentUse.Save, a.PartId) is { FileKept: false })
        {
            throw new IOException("the attachment check removed the file");
        }
        return true;
    }

    // message.part for the attachment being opened or previewed, the
    // message downloaded first when remote; null after a failure, which had
    // its toast.
    private async Task<MessagePartResult?> FetchForViewingAsync(Attachment a, MessageSummary s, bool remote, object? window)
    {
        try
        {
            return await parts.PartDataAsync(s.AccountId, s.Id, a, remote);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogFetchFailed(logger, a.PartId, e.GetType().Name);
            Say(window, RpcErrorText.Text(L10n.T("Opening the attachment"), e));
            return null;
        }
    }

    // The mark, or null when it could not even be tried (the path is not a
    // file's own).
    private async Task<ZoneMark?> MarkAsync(string path, AttachmentUse use, string partId)
    {
        try
        {
            return await mark.MarkAsync(path, use);
        }
        catch (ArgumentException e)
        {
            LogMarkFailed(logger, partId, e.GetType().Name);
            return null;
        }
    }

    private void RefuseProgram(object? window) =>
        Say(window, L10n.T("Programs and scripts are not opened directly; save the file and decide yourself."));

    private void Say(object? window, string text) => Toast?.Invoke(window, text);

    // Writes over what the user chose in the save dialog, which confirmed it.
    private static void Replace(string path, byte[] data)
    {
        try
        {
            File.WriteAllBytes(path, data);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            throw FileErrors.WithoutPath(e, "could not save the attachment");
        }
    }

    // attachments.go saveInto's loop: a free name, created exclusively, again
    // when another file took it between the check and the create. Null,
    // with nothing created, when the free name is one the policy names
    // (dangerous): the checks before judged the name the part asked for,
    // and a " (2)" with a cut to length gives the file another, as Open's
    // third look judges the name the open directory gave.
    private static string? CreateUnique(string folder, string name, byte[] data, Func<string, bool> dangerous)
    {
        Exception? last = null;
        for (var attempt = 0; attempt < CreateAttempts; attempt++)
        {
            var candidate = WindowsFileNames.UniqueName(name, n => Path.Exists(Path.Combine(folder, n)));
            if (dangerous(candidate))
            {
                return null;
            }
            var path = Path.Combine(folder, candidate);
            try
            {
                using (var stream = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.None))
                {
                    stream.Write(data);
                }
                return path;
            }
            catch (IOException e) when (Path.Exists(path))
            {
                last = e; // taken meanwhile
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                throw FileErrors.WithoutPath(e, "could not save the attachment");
            }
        }
        throw FileErrors.WithoutPath(last ?? new IOException(), "no free name for the attachment");
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.part {PartId} failed: {Kind}")]
    private static partial void LogFetchFailed(ILogger logger, string partId, string kind);

    [LoggerMessage(Level = LogLevel.Information, Message = "attachment {PartId} turned out a program after the fetch; not opened")]
    private static partial void LogServedAsProgram(ILogger logger, string partId);

    [LoggerMessage(Level = LogLevel.Warning, Message = "writing attachment {PartId} for opening failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogWriteFailed(ILogger logger, string partId, string kind, int hResult);

    [LoggerMessage(Level = LogLevel.Warning, Message = "attachment {PartId} not opened: mark {Outcome} 0x{SaveResult:X8}")]
    private static partial void LogNotMarked(ILogger logger, string partId, string outcome, int saveResult);

    [LoggerMessage(Level = LogLevel.Warning, Message = "marking attachment {PartId} failed: {Kind}")]
    private static partial void LogMarkFailed(ILogger logger, string partId, string kind);

    [LoggerMessage(Level = LogLevel.Warning, Message = "opening attachment {PartId} failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogLaunchFailed(ILogger logger, string partId, string kind, int hResult);

    [LoggerMessage(Level = LogLevel.Warning, Message = "saving attachment {PartId} failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogSaveFailed(ILogger logger, string partId, string kind, int hResult);

    [LoggerMessage(Level = LogLevel.Information, Message = "attachment {PartId} is a program; left out of Save All")]
    private static partial void LogSkipped(ILogger logger, string partId);

    [LoggerMessage(Level = LogLevel.Information, Message = "attachment {PartId} turned out a program after the fetch; left out of Save All")]
    private static partial void LogSkippedAfterFetch(ILogger logger, string partId);

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.download for Save All failed: {Kind}")]
    private static partial void LogDownloadFailed(ILogger logger, string kind);
}
