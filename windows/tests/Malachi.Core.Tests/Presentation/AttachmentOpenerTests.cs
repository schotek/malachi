// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of AttachmentOpener: ui/internal/window/attachments.go
// (openAttachment, previewAttachment, writeAttachment, saveAttachment,
// saveAllAttachments, saveInto) and macos AttachmentActions.swift (open with
// its second and third look, writeForViewing's quarantine, saveAs, saveAll,
// writeUnique), with the Windows mark in place of the quarantine: files in
// a temporary directory, the mark, the launcher and the pickers faked.
// Windows only: Save All leaves out what the file-type policy names. A part
// kept on the mail server comes after message.download (download.go
// partData, the cache's PartDataAsync, which the fake runs by the real
// rules).

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Platform;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Tests.Presentation.ReaderFixtures;

namespace Malachi.Core.Tests.Presentation;

public sealed class AttachmentOpenerTests : IDisposable
{
    private readonly TemporaryDirectory temp = new();
    private readonly FakeReaderCache cache = new();
    private readonly FakeMark mark = new();
    private readonly FakeLauncher launcher = new();
    private readonly FakePickers pickers = new();
    private readonly List<(object? Window, string Text)> toasts = [];
    private readonly AttachmentOpener opener;
    private readonly OpenDir openDir;

    public AttachmentOpenerTests()
    {
        openDir = new OpenDir(Path.Combine(temp.Path, "open"), new FakePrivateDirectories(), new FakeTimeProvider(DateTimeOffset.UtcNow));
        opener = new AttachmentOpener(cache, openDir, mark, new GtkPolicy(), launcher, pickers)
        {
            Toast = (w, text) => toasts.Add((w, text)),
            Owner = _ => 7,
        };
    }

    public void Dispose() => temp.Dispose();

    // The names of the files in a folder, in order.
    private static string[] Names(string folder) =>
        new DirectoryInfo(folder).GetFiles().Select(f => f.Name).Order(StringComparer.Ordinal).ToArray();

    private void Serve(string part, string name, string type, byte[] data) => cache.Parts[part] = new MessagePartResult
    {
        PartId = part,
        Filename = name,
        ContentType = type,
        Size = data.Length,
        Data = data,
    };

    [Fact]
    public async Task OpenWritesAPrivateFileMarksItAndHandsItOn()
    {
        Serve("2", "report.pdf", "application/pdf", [1, 2, 3]);
        await opener.OpenAsync(Attachment("2", "report.pdf"), Summary("m1"), remote: false, "w");
        var path = Assert.Single(launcher.Files);
        Assert.StartsWith(openDir.Path, path, StringComparison.Ordinal);
        Assert.Equal("report.pdf", Path.GetFileName(path));
        Assert.Equal([1, 2, 3], File.ReadAllBytes(path));
        Assert.Equal([(path, AttachmentUse.Open)], mark.Marked);
        Assert.Empty(toasts);
    }

    [Fact]
    public async Task AProgramIsNeverOpenedNorFetched()
    {
        Serve("2", "setup.exe", "application/x-msdownload", [0x4D, 0x5A]);
        await opener.OpenAsync(Attachment("2", "setup.exe", "application/x-msdownload"), Summary("m1"), remote: false, "w");
        Assert.Equal([("w", "Programs and scripts are not opened directly; save the file and decide yourself.")], toasts);
        Assert.Empty(cache.PartCalls);
        Assert.Empty(launcher.Files);
        Assert.False(opener.CanOpen(Attachment("2", "faktura.pdf.exe")));
        Assert.True(opener.CanOpen(Attachment("2", "faktura.pdf")));
    }

    [Fact]
    public async Task APartServedAsAProgramIsRefusedAfterTheFetch()
    {
        Serve("2", "invoice.js", "text/javascript", [1]);
        await opener.OpenAsync(Attachment("2", "invoice.txt", "text/plain"), Summary("m1"), remote: false, null);
        Assert.Equal(["2"], cache.PartCalls);
        Assert.Single(toasts);
        Assert.StartsWith("Programs and scripts", toasts[0].Text);
        Assert.Empty(launcher.Files);
        Assert.Empty(mark.Marked);
    }

    [Theory]
    [InlineData(ZoneMarkOutcome.Rejected)]
    [InlineData(ZoneMarkOutcome.CheckFailed)]
    [InlineData(ZoneMarkOutcome.Removed)]
    [InlineData(ZoneMarkOutcome.NotMarked)]
    public async Task AFileWithoutAGoodMarkIsNotOpened(ZoneMarkOutcome outcome)
    {
        mark.Outcome = outcome;
        Serve("2", "a.pdf", "application/pdf", [1]);
        await opener.OpenAsync(Attachment("2", "a.pdf"), Summary("m1"), remote: false, null);
        Assert.Empty(launcher.Files);
        Assert.Equal(["The attachment could not be opened"], toasts.Select(t => t.Text));
    }

    [Fact]
    public async Task AFailedFetchOrLaunchIsAToast()
    {
        await opener.OpenAsync(Attachment("9", "missing.pdf"), Summary("m1"), remote: false, null);
        Assert.Equal(["Opening the attachment failed"], toasts.Select(t => t.Text));

        toasts.Clear();
        Serve("2", "a.pdf", "application/pdf", [1]);
        launcher.Failure = new IOException("no application");
        await opener.OpenAsync(Attachment("2", "a.pdf"), Summary("m1"), remote: false, null);
        Assert.Equal(["The attachment could not be opened"], toasts.Select(t => t.Text));
    }

    [Fact]
    public async Task PreviewKeepsTheBytesInMemoryAndAProgramsMetadataOnly()
    {
        Serve("2", "photo.png", "image/png", [9, 9]);
        var shown = await opener.PreviewAsync(Attachment("2", "photo.png", "image/png"), Summary("m1"), remote: false, null);
        Assert.NotNull(shown);
        Assert.Equal("image/png", shown.ContentType);
        Assert.Equal([9, 9], shown.Data!.Value.ToArray());
        Assert.True(shown.CanOpen);
        Assert.Equal(2, shown.Size);
        Assert.False(Directory.Exists(openDir.Path));

        var exe = await opener.PreviewAsync(Attachment("3", "setup.exe", "application/x-msdownload", 76), Summary("m1"), remote: false, null);
        Assert.NotNull(exe);
        Assert.Null(exe.Data);
        Assert.Null(exe.Size);
        Assert.False(exe.CanOpen);
        Assert.Equal(["2"], cache.PartCalls);

        // Served as a program: its panel only.
        Serve("4", "run.bat", "application/x-bat", [1]);
        var served = await opener.PreviewAsync(Attachment("4", "notes.txt", "text/plain"), Summary("m1"), remote: false, null);
        Assert.Null(served!.Data);
        Assert.False(served.CanOpen);

        // A failed fetch: a toast, nothing to show.
        Assert.Null(await opener.PreviewAsync(Attachment("9", "gone.png"), Summary("m1"), remote: false, null));
        Assert.Equal(["Opening the attachment failed"], toasts.Select(t => t.Text));
    }

    [Fact]
    public async Task SaveAsWritesOverTheChosenFileAndMarksIt()
    {
        Serve("2", "report.pdf", "application/pdf", [4, 5]);
        var target = Path.Combine(temp.Path, "chosen.pdf");
        File.WriteAllBytes(target, [0, 0, 0, 0]);
        pickers.SaveAnswer = target;
        await opener.SaveAsAsync(Attachment("2", "report.pdf"), Summary("m1"), remote: false, "w");
        Assert.Equal([("w", "Save Attachment", "report.pdf")], pickers.SaveAsked);
        Assert.Equal([4, 5], File.ReadAllBytes(target));
        Assert.Equal([(target, AttachmentUse.Save)], mark.Marked);
        Assert.Empty(toasts);

        // A program may be saved: that is the way it goes.
        Serve("3", "setup.exe", "application/x-msdownload", [0x4D, 0x5A]);
        pickers.SaveAnswer = Path.Combine(temp.Path, "setup.exe");
        await opener.SaveAsAsync(Attachment("3", "setup.exe", "application/x-msdownload"), Summary("m1"), remote: false, "w");
        Assert.True(File.Exists(pickers.SaveAnswer));
    }

    [Fact]
    public async Task SaveAsDismissedDoesNothingAndAFailureIsAToast()
    {
        pickers.SaveAnswer = null;
        await opener.SaveAsAsync(Attachment("2", "a.pdf"), Summary("m1"), remote: false, null);
        Assert.Empty(cache.PartCalls);
        Assert.Empty(toasts);

        pickers.SaveAnswer = Path.Combine(temp.Path, "x.pdf");
        await opener.SaveAsAsync(Attachment("9", "x.pdf"), Summary("m1"), remote: false, null);
        Assert.Equal(["Saving the attachment failed"], toasts.Select(t => t.Text));

        // A file the check removed counts as not saved.
        toasts.Clear();
        Serve("2", "a.pdf", "application/pdf", [1]);
        mark.Outcome = ZoneMarkOutcome.Removed;
        await opener.SaveAsAsync(Attachment("2", "a.pdf"), Summary("m1"), remote: false, null);
        Assert.Equal(["Saving the attachment failed"], toasts.Select(t => t.Text));
    }

    [Fact]
    public async Task SaveAllNeverOverwritesAndSumsUp()
    {
        var folder = Path.Combine(temp.Path, "folder");
        Directory.CreateDirectory(folder);
        File.WriteAllBytes(Path.Combine(folder, "a.pdf"), [0]);
        Serve("2", "a.pdf", "application/pdf", [1]);
        Serve("3", "b.png", "image/png", [2]);
        pickers.FolderAnswer = folder;
        await opener.SaveAllAsync([Attachment("2", "a.pdf"), Attachment("3", "b.png", "image/png")], Summary("m1"), remote: false, "w");
        Assert.Equal([("w", "Save Attachments")], pickers.FolderAsked);
        Assert.Equal([0], File.ReadAllBytes(Path.Combine(folder, "a.pdf")));
        Assert.Equal([1], File.ReadAllBytes(Path.Combine(folder, "a (2).pdf")));
        Assert.Equal([2], File.ReadAllBytes(Path.Combine(folder, "b.png")));
        Assert.Equal(2, mark.Marked.Count);
        Assert.All(mark.Marked, m => Assert.Equal(AttachmentUse.Save, m.Use));
        Assert.Equal([("w", "Saved 2 attachments")], toasts);

        // A part that cannot be fetched is counted, the rest saved.
        toasts.Clear();
        await opener.SaveAllAsync([Attachment("2", "a.pdf"), Attachment("9", "gone.pdf")], Summary("m1"), remote: false, null);
        Assert.Equal(["1 of 2 attachments could not be saved"], toasts.Select(t => t.Text));
        Assert.True(File.Exists(Path.Combine(folder, "a (3).pdf")));
    }

    [Fact]
    public async Task SaveAllLeavesOutProgramsAndShortcutsAndSaysSo()
    {
        // Windows only: Explorer parses a shortcut or a library in the
        // folder it shows, whatever its mark; Save As saves one.
        var folder = Path.Combine(temp.Path, "folder");
        Directory.CreateDirectory(folder);
        Serve("2", "a.pdf", "application/pdf", [1]);
        Serve("3", "x.url", "application/octet-stream", [2]);
        Serve("4", "setup.exe", "application/x-msdownload", [3]);
        Serve("5", "run.bat", "application/x-bat", [4]);
        Serve("6", "b.png", "image/png", [5]);
        pickers.FolderAnswer = folder;
        await opener.SaveAllAsync(
            [
                Attachment("2", "a.pdf"),
                Attachment("3", "x.url", "application/octet-stream"),
                Attachment("4", "setup.exe", "application/x-msdownload"),
                Attachment("5", "notes.txt", "text/plain"), // served as a program
                Attachment("6", "b.png", "image/png"),
            ],
            Summary("m1"),
            remote: false,
            "w");
        Assert.Equal(["a.pdf", "b.png"], Names(folder));
        // What the message lists as a program is not even fetched.
        Assert.Equal(["2", "5", "6"], cache.PartCalls);
        Assert.Equal(2, mark.Marked.Count);
        Assert.Equal(
            [
                ("w", "Saved 2 attachments"),
                ("w", "3 attachments were not saved; save programs and scripts with Save As…"),
            ],
            toasts);
        Assert.False(opener.IsSavingAll("m1"));
    }

    [Fact]
    public async Task SaveAllOfProgramsAloneAsksForNoFolder()
    {
        pickers.FolderAnswer = Path.Combine(temp.Path, "unused");
        await opener.SaveAllAsync(
            [Attachment("2", "x.library-ms", "application/octet-stream"), Attachment("3", "y.searchConnector-ms", "")],
            Summary("m1"),
            remote: false,
            "w");
        Assert.Empty(pickers.FolderAsked);
        Assert.Empty(cache.PartCalls);
        Assert.Equal([("w", "2 attachments were not saved; save programs and scripts with Save As…")], toasts);
        Assert.False(opener.IsSavingAll("m1"));
    }

    [Fact]
    public async Task SaveAllOfPartsServedAsProgramsSaysOnlyThat()
    {
        var folder = Path.Combine(temp.Path, "folder");
        Directory.CreateDirectory(folder);
        Serve("2", "invoice.lnk", "application/x-ms-shortcut", [1]);
        pickers.FolderAnswer = folder;
        await opener.SaveAllAsync([Attachment("2", "invoice.pdf")], Summary("m1"), remote: false, null);
        Assert.Empty(Directory.GetFiles(folder));
        Assert.Empty(mark.Marked);
        Assert.Equal(["1 attachment was not saved; save programs and scripts with Save As…"], toasts.Select(t => t.Text));
    }

    [Fact]
    public async Task SaveAllJudgesTheNameTheFileWouldGet()
    {
        // A policy that names only the " (2)" a taken name gets: the third
        // look, on the free name, keeps it from being created.
        var folder = Path.Combine(temp.Path, "folder");
        Directory.CreateDirectory(folder);
        File.WriteAllBytes(Path.Combine(folder, "a.pdf"), [0]);
        Serve("2", "a.pdf", "application/pdf", [1]);
        pickers.FolderAnswer = folder;
        var strict = new AttachmentOpener(cache, openDir, mark, new NamedPolicy(" (2)"), launcher, pickers)
        {
            Toast = (w, text) => toasts.Add((w, text)),
        };
        await strict.SaveAllAsync([Attachment("2", "a.pdf")], Summary("m1"), remote: false, null);
        Assert.Equal(["a.pdf"], Names(folder));
        Assert.Equal([0], File.ReadAllBytes(Path.Combine(folder, "a.pdf")));
        Assert.Empty(mark.Marked);
        Assert.Equal(["1 attachment was not saved; save programs and scripts with Save As…"], toasts.Select(t => t.Text));
    }

    [Fact]
    public async Task SaveAllDismissedDoesNothing()
    {
        pickers.FolderAnswer = null;
        await opener.SaveAllAsync([Attachment("2", "a.pdf")], Summary("m1"), remote: false, null);
        Assert.Empty(cache.PartCalls);
        Assert.Empty(toasts);
        Assert.False(opener.IsSavingAll("m1"));
    }

    [Fact]
    public async Task SaveAllRunsOncePerMessageAtATime()
    {
        var folder = Path.Combine(temp.Path, "folder");
        Directory.CreateDirectory(folder);
        Serve("2", "a.pdf", "application/pdf", [1]);
        var changes = new List<(MessageId Id, bool Saving)>();
        opener.SavingAllChanged += (_, id) => changes.Add((id, opener.IsSavingAll(id)));
        var answer = new TaskCompletionSource<string?>();
        pickers.FolderGate = answer.Task;

        // The first run waits in its folder picker.
        var first = opener.SaveAllAsync([Attachment("2", "a.pdf")], Summary("m1"), remote: false, "w");
        Assert.False(first.IsCompleted);
        Assert.True(opener.IsSavingAll("m1"));
        Assert.Equal([((MessageId)"m1", true)], changes);

        // A rebuilt button's click, or another view of the same message: nothing.
        await opener.SaveAllAsync([Attachment("2", "a.pdf")], Summary("m1"), remote: false, "w2");
        Assert.Single(pickers.FolderAsked);

        // Another message is not held up.
        pickers.FolderGate = null;
        pickers.FolderAnswer = null;
        await opener.SaveAllAsync([Attachment("2", "a.pdf")], Summary("m2"), remote: false, "w");
        Assert.Equal(2, pickers.FolderAsked.Count);
        Assert.False(opener.IsSavingAll("m2"));

        answer.SetResult(folder);
        await first;
        Assert.False(opener.IsSavingAll("m1"));
        Assert.Equal(((MessageId)"m1", false), changes[^1]);
        Assert.Equal([("w", "Saved 1 attachment")], toasts);
        Assert.True(File.Exists(Path.Combine(folder, "a.pdf")));

        // And the next run of the first message goes ahead.
        pickers.FolderAnswer = folder;
        await opener.SaveAllAsync([Attachment("2", "a.pdf")], Summary("m1"), remote: false, "w");
        Assert.True(File.Exists(Path.Combine(folder, "a (2).pdf")));
    }

    /// <summary>
    /// download.go <c>partData</c> through the actions: a chip on the mail
    /// server downloads the message first, then fetches the part the
    /// downloaded message lists (renumbered on Microsoft 365); a failed
    /// download is the action's toast and fetches nothing; a part the chip
    /// showed stored that the daemon has moved to the server since gets one
    /// download and one retry.
    /// </summary>
    [Fact]
    public async Task APartOnTheServerIsDownloadedFirst()
    {
        var big = Attachment("2", "report.pdf") with { Remote = true };
        Serve("3", "report.pdf", "application/pdf", [1, 2, 3]);
        cache.Downloaded = Message(Summary("m1"), [Attachment("3", "report.pdf")]);
        await opener.OpenAsync(big, Summary("m1"), remote: true, "w");
        Assert.Equal(["m1"], cache.DownloadCalls.Select(id => id.Value));
        Assert.Equal(["3"], cache.PartCalls);
        Assert.Single(launcher.Files);
        Assert.Empty(toasts);

        // A failed download: the toast of the action, nothing fetched.
        cache.DownloadError = new RpcException(new RpcError { Code = ErrorCode.Offline, Message = "no network" });
        await opener.OpenAsync(big, Summary("m1"), remote: true, "w");
        Assert.Equal([("w", "Opening the attachment failed: no network connection")], toasts);
        Assert.Single(cache.PartCalls);
        Assert.Single(launcher.Files);

        // Save As the same way.
        toasts.Clear();
        cache.DownloadError = null;
        var target = Path.Combine(temp.Path, "saved.pdf");
        pickers.SaveAnswer = target;
        await opener.SaveAsAsync(big, Summary("m1"), remote: true, "w");
        Assert.Equal([1, 2, 3], File.ReadAllBytes(target));
        Assert.Equal(3, cache.DownloadCalls.Count);

        // The preview carries the part the daemon served, for its Open and
        // Save As….
        var shown = await opener.PreviewAsync(big, Summary("m1"), remote: true, "w");
        Assert.Equal("3", shown!.Attachment.PartId);
        Assert.Equal(4, cache.DownloadCalls.Count);

        // The chip said stored, the daemon disagrees: one download, one retry.
        Serve("4", "notes.txt", "text/plain", [9]);
        cache.OnServer.Add("4");
        cache.Downloaded = Message(Summary("m1"), [Attachment("4", "notes.txt", "text/plain")]);
        var preview = await opener.PreviewAsync(Attachment("4", "notes.txt", "text/plain"), Summary("m1"), remote: false, null);
        Assert.Equal([9], preview!.Data!.Value.ToArray());
        Assert.Equal(["3", "3", "3", "4", "4"], cache.PartCalls);
        Assert.Equal(5, cache.DownloadCalls.Count);
        Assert.Empty(toasts);
    }

    /// <summary>
    /// attachments.go <c>saveAllAttachments</c> with <c>remote</c>: the
    /// message is downloaded once after the folder is chosen, every part is
    /// fetched as the downloaded message lists it, and one it no longer lists
    /// counts as not saved; a failed download writes nothing and says why.
    /// </summary>
    [Fact]
    public async Task SaveAllDownloadsOnceWhenSomeAreOnTheServer()
    {
        var folder = Path.Combine(temp.Path, "folder");
        Directory.CreateDirectory(folder);
        pickers.FolderAnswer = folder;
        Serve("2", "a.pdf", "application/pdf", [1]);
        Serve("5", "b.pdf", "application/pdf", [2]);
        cache.Downloaded = Message(Summary("m1"), [Attachment("2", "a.pdf"), Attachment("5", "b.pdf")]);
        List<Attachment> atts =
        [
            Attachment("2", "a.pdf"),
            Attachment("3", "b.pdf") with { Remote = true },
            Attachment("4", "gone.pdf") with { Remote = true },
        ];
        await opener.SaveAllAsync(atts, Summary("m1"), remote: true, "w");
        Assert.Single(cache.DownloadCalls);
        Assert.Equal(["2", "5"], cache.PartCalls);
        Assert.Equal(["a.pdf", "b.pdf"], Names(folder));
        Assert.Equal([("w", "1 of 3 attachments could not be saved")], toasts);

        // A failed download writes nothing and says why.
        toasts.Clear();
        cache.DownloadError = new RpcException(new RpcError { Code = ErrorCode.Offline, Message = "no network" });
        await opener.SaveAllAsync(atts, Summary("m1"), remote: true, "w");
        Assert.Equal([("w", "Saving the attachments failed: no network connection")], toasts);
        Assert.Equal(2, cache.PartCalls.Count);
        Assert.Equal(2, pickers.FolderAsked.Count);
        Assert.False(opener.IsSavingAll("m1"));
    }

    // DangerousTypes, as FileTypePolicy starts from.
    private sealed class GtkPolicy : IFileTypePolicy
    {
        public bool IsDangerous(string? fileName, string? contentType) => DangerousTypes.IsDangerous(fileName, contentType);
    }

    // Names every file whose name holds a piece of text.
    private sealed class NamedPolicy(string piece) : IFileTypePolicy
    {
        public bool IsDangerous(string? fileName, string? contentType) =>
            (fileName ?? "").Contains(piece, StringComparison.Ordinal);
    }

    private sealed class FakeMark : IMarkOfTheWeb
    {
        public ZoneMarkOutcome Outcome { get; set; } = ZoneMarkOutcome.Marked;

        public List<(string Path, AttachmentUse Use)> Marked { get; } = [];

        public Task<ZoneMark> MarkAsync(string path, AttachmentUse use, CancellationToken cancellationToken = default)
        {
            Marked.Add((path, use));
            return Task.FromResult(new ZoneMark { Outcome = Outcome, ZoneId = 4 });
        }
    }

    private sealed class FakePickers : IAttachmentPickers
    {
        public string? SaveAnswer { get; set; }

        public string? FolderAnswer { get; set; }

        // When set, the folder picker answers when this does.
        public Task<string?>? FolderGate { get; set; }

        public List<(object? Window, string Title, string Name)> SaveAsked { get; } = [];

        public List<(object? Window, string Title)> FolderAsked { get; } = [];

        public Task<string?> PickSaveFileAsync(object? window, string title, string suggestedName)
        {
            SaveAsked.Add((window, title, suggestedName));
            return Task.FromResult(SaveAnswer);
        }

        public Task<string?> PickFolderAsync(object? window, string title)
        {
            FolderAsked.Add((window, title));
            return FolderGate ?? Task.FromResult(FolderAnswer);
        }
    }
}
