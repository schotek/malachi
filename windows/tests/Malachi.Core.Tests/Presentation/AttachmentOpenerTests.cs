// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of AttachmentOpener: ui/internal/window/attachments.go
// (openAttachment, previewAttachment, writeAttachment, saveAttachment,
// saveAllAttachments, saveInto) and macos AttachmentActions.swift (open with
// its second and third look, writeForViewing's quarantine, saveAs, saveAll,
// writeUnique), with the Windows mark in place of the quarantine: files in
// a temporary directory, the mark, the launcher and the pickers faked.

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
        await opener.OpenAsync(Attachment("2", "report.pdf"), Summary("m1"), "w");
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
        await opener.OpenAsync(Attachment("2", "setup.exe", "application/x-msdownload"), Summary("m1"), "w");
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
        await opener.OpenAsync(Attachment("2", "invoice.txt", "text/plain"), Summary("m1"), null);
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
        await opener.OpenAsync(Attachment("2", "a.pdf"), Summary("m1"), null);
        Assert.Empty(launcher.Files);
        Assert.Equal(["The attachment could not be opened"], toasts.Select(t => t.Text));
    }

    [Fact]
    public async Task AFailedFetchOrLaunchIsAToast()
    {
        await opener.OpenAsync(Attachment("9", "missing.pdf"), Summary("m1"), null);
        Assert.Equal(["Opening the attachment failed"], toasts.Select(t => t.Text));

        toasts.Clear();
        Serve("2", "a.pdf", "application/pdf", [1]);
        launcher.Failure = new IOException("no application");
        await opener.OpenAsync(Attachment("2", "a.pdf"), Summary("m1"), null);
        Assert.Equal(["The attachment could not be opened"], toasts.Select(t => t.Text));
    }

    [Fact]
    public async Task PreviewKeepsTheBytesInMemoryAndAProgramsMetadataOnly()
    {
        Serve("2", "photo.png", "image/png", [9, 9]);
        var shown = await opener.PreviewAsync(Attachment("2", "photo.png", "image/png"), Summary("m1"), null);
        Assert.NotNull(shown);
        Assert.Equal("image/png", shown.ContentType);
        Assert.Equal([9, 9], shown.Data!.Value.ToArray());
        Assert.True(shown.CanOpen);
        Assert.Equal(2, shown.Size);
        Assert.False(Directory.Exists(openDir.Path));

        var exe = await opener.PreviewAsync(Attachment("3", "setup.exe", "application/x-msdownload", 76), Summary("m1"), null);
        Assert.NotNull(exe);
        Assert.Null(exe.Data);
        Assert.Null(exe.Size);
        Assert.False(exe.CanOpen);
        Assert.Equal(["2"], cache.PartCalls);

        // Served as a program: its panel only.
        Serve("4", "run.bat", "application/x-bat", [1]);
        var served = await opener.PreviewAsync(Attachment("4", "notes.txt", "text/plain"), Summary("m1"), null);
        Assert.Null(served!.Data);
        Assert.False(served.CanOpen);

        // A failed fetch: a toast, nothing to show.
        Assert.Null(await opener.PreviewAsync(Attachment("9", "gone.png"), Summary("m1"), null));
        Assert.Equal(["Opening the attachment failed"], toasts.Select(t => t.Text));
    }

    [Fact]
    public async Task SaveAsWritesOverTheChosenFileAndMarksIt()
    {
        Serve("2", "report.pdf", "application/pdf", [4, 5]);
        var target = Path.Combine(temp.Path, "chosen.pdf");
        File.WriteAllBytes(target, [0, 0, 0, 0]);
        pickers.SaveAnswer = target;
        await opener.SaveAsAsync(Attachment("2", "report.pdf"), Summary("m1"), "w");
        Assert.Equal([("w", "Save Attachment", "report.pdf")], pickers.SaveAsked);
        Assert.Equal([4, 5], File.ReadAllBytes(target));
        Assert.Equal([(target, AttachmentUse.Save)], mark.Marked);
        Assert.Empty(toasts);

        // A program may be saved: that is the way it goes.
        Serve("3", "setup.exe", "application/x-msdownload", [0x4D, 0x5A]);
        pickers.SaveAnswer = Path.Combine(temp.Path, "setup.exe");
        await opener.SaveAsAsync(Attachment("3", "setup.exe", "application/x-msdownload"), Summary("m1"), "w");
        Assert.True(File.Exists(pickers.SaveAnswer));
    }

    [Fact]
    public async Task SaveAsDismissedDoesNothingAndAFailureIsAToast()
    {
        pickers.SaveAnswer = null;
        await opener.SaveAsAsync(Attachment("2", "a.pdf"), Summary("m1"), null);
        Assert.Empty(cache.PartCalls);
        Assert.Empty(toasts);

        pickers.SaveAnswer = Path.Combine(temp.Path, "x.pdf");
        await opener.SaveAsAsync(Attachment("9", "x.pdf"), Summary("m1"), null);
        Assert.Equal(["Saving the attachment failed"], toasts.Select(t => t.Text));

        // A file the check removed counts as not saved.
        toasts.Clear();
        Serve("2", "a.pdf", "application/pdf", [1]);
        mark.Outcome = ZoneMarkOutcome.Removed;
        await opener.SaveAsAsync(Attachment("2", "a.pdf"), Summary("m1"), null);
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
        await opener.SaveAllAsync([Attachment("2", "a.pdf"), Attachment("3", "b.png", "image/png")], Summary("m1"), "w");
        Assert.Equal([("w", "Save Attachments")], pickers.FolderAsked);
        Assert.Equal([0], File.ReadAllBytes(Path.Combine(folder, "a.pdf")));
        Assert.Equal([1], File.ReadAllBytes(Path.Combine(folder, "a (2).pdf")));
        Assert.Equal([2], File.ReadAllBytes(Path.Combine(folder, "b.png")));
        Assert.Equal(2, mark.Marked.Count);
        Assert.All(mark.Marked, m => Assert.Equal(AttachmentUse.Save, m.Use));
        Assert.Equal([("w", "Saved 2 attachments")], toasts);

        // A part that cannot be fetched is counted, the rest saved.
        toasts.Clear();
        await opener.SaveAllAsync([Attachment("2", "a.pdf"), Attachment("9", "gone.pdf")], Summary("m1"), null);
        Assert.Equal(["1 of 2 attachments could not be saved"], toasts.Select(t => t.Text));
        Assert.True(File.Exists(Path.Combine(folder, "a (3).pdf")));
    }

    [Fact]
    public async Task SaveAllDismissedDoesNothing()
    {
        pickers.FolderAnswer = null;
        await opener.SaveAllAsync([Attachment("2", "a.pdf")], Summary("m1"), null);
        Assert.Empty(cache.PartCalls);
        Assert.Empty(toasts);
    }

    // DangerousTypes, as FileTypePolicy starts from.
    private sealed class GtkPolicy : IFileTypePolicy
    {
        public bool IsDangerous(string? fileName, string? contentType) => DangerousTypes.IsDangerous(fileName, contentType);
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
            return Task.FromResult(FolderAnswer);
        }
    }
}
