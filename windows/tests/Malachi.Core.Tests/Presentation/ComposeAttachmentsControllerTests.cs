// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of Malachi.Core.Presentation.ComposeAttachmentsController, the
// attachments of a compose window (ui/internal/compose/compose.go
// attachGioFiles, insertImage, importFile, addChip, removeAttachment,
// setAttachments, registerInline; macos ComposeAttachments.swift and
// AttachmentChipsView.swift), which have no test in Go or Swift: what counts
// as a local path, the chip's name and icon, the import with its status and
// failure, the inline picture registered and inserted, the removal, the
// list the backend kept with its cid: registrations, and a closed window.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Html;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Controllers;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class ComposeAttachmentsControllerTests
{
    [Theory]
    [InlineData(@"C:\Users\me\report.pdf", true)]
    [InlineData("c:/Users/me/report.pdf", true)]
    [InlineData(@"\\server\share\report.pdf", true)]
    [InlineData(@"D:\a", true)]
    [InlineData(@"\\?\C:\Users\me\report.pdf", false)]
    [InlineData(@"\\.\pipe\x", false)]
    [InlineData(@"C:report.pdf", false)]
    [InlineData("C:", false)]
    [InlineData(@"report.pdf", false)]
    [InlineData(@"\report.pdf", false)]
    [InlineData("/home/me/report.pdf", false)]
    [InlineData("", false)]
    [InlineData(null, false)]
    [InlineData("C:\\a\0b", false)]
    public void OnlyFileSystemPathsAreLocal(string? path, bool local) =>
        Assert.Equal(local, ComposeAttachmentsController.IsLocalPath(path));

    [Theory]
    [InlineData("a.pdf", "a.pdf")]
    [InlineData("exactly-twenty-four.pdf!", "exactly-twenty-four.pdf!")]
    [InlineData("twenty-five-characters.pdf", "twenty-five-characters.…")]
    public void ChipNamesAreCutAtTheEnd(string name, string chip) =>
        Assert.Equal(chip, ComposeAttachmentsController.ChipName(name));

    [Fact]
    public void AChipNameNeverSplitsACharacter()
    {
        // 30 text elements, each an emoji of two UTF-16 units or a letter
        // with a combining accent.
        var name = string.Concat(Enumerable.Repeat("\U0001F600e\u0301", 15));
        var chip = ComposeAttachmentsController.ChipName(name);
        Assert.EndsWith("\u2026", chip, StringComparison.Ordinal);
        Assert.Equal(24, new System.Globalization.StringInfo(chip).LengthInTextElements);
        Assert.StartsWith(chip[..^1], name, StringComparison.Ordinal);
    }

    [Fact]
    public void AChipShowsTheIconTheNameAndTheSize()
    {
        var file = AttachmentChip.For(Att("a1", "quarterly-report-final-version.pdf", 2048, inline: false));
        Assert.Equal("a1", file.Id);
        Assert.Equal("quarterly-report-final-…", file.Name);
        Assert.Equal("quarterly-report-final-version.pdf", file.FullName);
        Assert.Equal(Malachi.Core.Text.Format.FormatSize(2048), file.Size);
        Assert.Equal("mail-attachment-symbolic", file.Icon);
        Assert.Equal("image-x-generic-symbolic", AttachmentChip.For(Att("a2", "p.png", 1, inline: true, cid: "c2")).Icon);
    }

    [Fact]
    public async Task AttachingImportsEachLocalFileAndRefusesTheRest()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Attachments.AttachFiles([@"C:\docs\a.pdf", null, @"\\?\C:\docs\b.pdf"]));
        Assert.Equal(["Attaching a.pdf…"], h.Statuses);
        Assert.Equal(["Only local files can be attached", "Only local files can be attached"], h.Toasts);
        await h.IdleAsync();
        var import = Assert.Single(h.Script.Imports);
        Assert.Equal(new AccountId("acc1"), import.AccountId);
        Assert.Equal(@"C:\docs\a.pdf", import.Path);
        Assert.Equal("a.pdf", import.Filename);
        Assert.False(import.Inline);
        Assert.Equal(["a.pdf"], h.Attachments.Attachments.Select(a => a.Filename));
        Assert.Equal(["a.pdf"], h.Attachments.Chips.Select(c => c.Name));
        Assert.Equal(1, h.Changes);
        Assert.Equal(1, h.Edits);
        Assert.Equal(0, h.Failures);
    }

    [Fact]
    public async Task AFailedImportIsAToastAndPutsTheStatusBack()
    {
        await using var h = await Harness.StartAsync();
        h.Script.ImportError = new RpcError { Code = ErrorCode.InvalidParams, Message = "too big" };
        await h.Run(() => h.Attachments.Import(@"C:\docs\big.iso", "big.iso", inline: false, then: _ => Assert.Fail("not imported")));
        await h.IdleAsync();
        var toast = Assert.Single(h.Toasts);
        Assert.StartsWith("Attaching big.iso", toast, StringComparison.Ordinal);
        Assert.Equal(1, h.Failures);
        Assert.Empty(h.Attachments.Attachments);
        Assert.Equal(0, h.Edits);
    }

    [Fact]
    public async Task AnInsertedImageIsImportedInlineRegisteredAndInserted()
    {
        await using var h = await Harness.StartAsync();
        var inserted = new List<string>();
        await h.Run(() => h.Attachments.InsertImage(@"C:\pics\cat.png", inserted.Add));
        await h.IdleAsync();
        var import = Assert.Single(h.Script.Imports);
        Assert.True(import.Inline);
        Assert.Equal(["cid:cid-1"], inserted);
        Assert.Equal(new CidEntry.File(@"C:\pics\cat.png", "image/png"), h.Registry.Lookup("cid-1"));

        await h.Run(() => h.Attachments.InsertImage("", inserted.Add));
        Assert.Equal(["Only local images can be inserted"], h.Toasts);
        Assert.Single(inserted);
    }

    [Fact]
    public async Task RemovingForgetsTheFileAndTellsTheBackend()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Attachments.InsertImage(@"C:\pics\cat.png", _ => { }));
        await h.Run(() => h.Attachments.AttachFiles([@"C:\docs\a.pdf"]));
        await h.IdleAsync();
        Assert.Equal(2, h.Attachments.Attachments.Count);
        var picture = h.Attachments.Attachments[0];

        await h.Run(() => h.Attachments.Remove(picture.Id));
        Assert.Equal(["a.pdf"], h.Attachments.Attachments.Select(a => a.Filename));
        Assert.Null(h.Registry.Lookup("cid-1"));
        Assert.Equal(3, h.Edits);
        await h.IdleAsync();
        var removed = Assert.Single(h.Script.Removes);
        Assert.Equal(picture.Id, removed.AttachmentId);
        Assert.Equal(new AccountId("acc1"), removed.AccountId);

        await h.Run(() => h.Attachments.Remove("nope"));
        await h.IdleAsync();
        Assert.Single(h.Script.Removes);
        Assert.Equal(3, h.Edits);
    }

    [Fact]
    public async Task TheListTheBackendKeptServesItsPictures()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Attachments.InsertImage(@"C:\pics\cat.png", _ => { }));
        await h.IdleAsync();
        var mine = h.Attachments.Attachments[0];
        h.Registry.Register("gone", @"C:\pics\old.png", "image/png");
        var old = Att("old", "old.png", 3, inline: true, cid: "gone");

        // A save that kept my picture and the template's copy, dropped another.
        var copy = Att("t1", "quoted.png", 9, inline: true, cid: "quoted");
        await h.Run(() => h.Attachments.Set([mine, old]));
        await h.Run(() => h.Attachments.Set([mine, copy, Att("f1", "file.txt", 4, inline: false)]));
        Assert.Equal(["cat.png", "quoted.png", "file.txt"], h.Attachments.Attachments.Select(a => a.Filename));
        Assert.Null(h.Registry.Lookup("gone"));
        Assert.IsType<CidEntry.File>(h.Registry.Lookup("cid-1"));
        var fetcher = Assert.IsType<CidEntry.Fetcher>(h.Registry.Lookup("quoted"));
        Assert.Equal(1, h.Edits); // the insertion; the backend's lists are no edit

        var picture = await fetcher.Fetch(TestContext.Current.CancellationToken);
        Assert.Equal("image/png", picture.ContentType);
        Assert.Equal([1, 2, 3], picture.Data.ToArray());
        var get = Assert.Single(h.Script.Gets);
        Assert.Equal("t1", get.AttachmentId);
        Assert.Equal(new AccountId("acc1"), get.AccountId);
    }

    /// <summary>
    /// A picture of the template is fetched from the account the window
    /// has when the editor asks for it: the first window of a run is made
    /// while the account list is on its way (the placeholder identity).
    /// </summary>
    [Fact]
    public async Task APictureIsFetchedFromTheAccountOfTheMoment()
    {
        await using var h = await Harness.StartAsync();
        h.Account = "acc_dummy";
        await h.Run(() => h.Attachments.Set([Att("t1", "quoted.png", 9, inline: true, cid: "quoted")]));
        h.Account = "acc2";
        var fetcher = Assert.IsType<CidEntry.Fetcher>(h.Registry.Lookup("quoted"));
        await fetcher.Fetch(TestContext.Current.CancellationToken);
        Assert.Equal(new AccountId("acc2"), Assert.Single(h.Script.Gets).AccountId);
    }

    [Fact]
    public async Task AClosedWindowTakesNothingLate()
    {
        await using var h = await Harness.StartAsync();
        var held = h.Script.HoldImports();
        await h.Run(() => h.Attachments.AttachFiles([@"C:\docs\a.pdf"]));
        await held.ArrivedAsync();
        await h.Run(() => h.Attachments.Dispose());
        held.Release();
        await h.IdleAsync();
        Assert.Empty(h.Attachments.Attachments);
        Assert.Equal(0, h.Changes);

        await h.Run(() => h.Attachments.AttachFiles([@"C:\docs\b.pdf"]));
        await h.IdleAsync();
        Assert.Single(h.Script.Imports);
    }

    private static DraftAttachment Att(string id, string name, long size, bool inline, string? cid = null) =>
        new() { Id = id, Filename = name, ContentType = inline ? "image/png" : "application/octet-stream", Size = size, Inline = inline, ContentId = cid };

    /// <summary>The attachment calls of a fake daemon.</summary>
    private sealed class Script
    {
        private readonly Lock gate = new();
        private readonly List<AttachmentImportParams> imports = [];
        private readonly List<AttachmentRemoveParams> removes = [];
        private readonly List<AttachmentGetParams> gets = [];
        private HeldAnswer? held;

        public RpcError? ImportError { get; set; }

        public IReadOnlyList<AttachmentImportParams> Imports => Locked(imports);

        public IReadOnlyList<AttachmentRemoveParams> Removes => Locked(removes);

        public IReadOnlyList<AttachmentGetParams> Gets => Locked(gets);

        public HeldAnswer HoldImports()
        {
            var h = new HeldAnswer();
            lock (gate)
            {
                held = h;
            }
            return h;
        }

        public async Task<string> Import(string p)
        {
            AttachmentImportParams request;
            int n;
            Task wait;
            lock (gate)
            {
                request = JsonCoding.Decode<AttachmentImportParams>(p);
                imports.Add(request);
                n = imports.Count;
                wait = held?.WaitAsync() ?? Task.CompletedTask;
            }
            await wait;
            if (ImportError is { } e)
            {
                throw new RpcException(e);
            }
            var inline = request.Inline == true;
            return JsonCoding.EncodeToString(new AttachmentImportResult
            {
                Attachment = new DraftAttachment
                {
                    Id = "att-" + n,
                    Filename = request.Filename ?? "",
                    ContentType = inline ? "image/png" : "application/pdf",
                    Size = 100,
                    Inline = inline,
                    ContentId = inline ? "cid-" + n : null,
                },
            });
        }

        public string Remove(string p)
        {
            lock (gate)
            {
                removes.Add(JsonCoding.Decode<AttachmentRemoveParams>(p));
            }
            return "{}";
        }

        public string Get(string p)
        {
            AttachmentGetParams request;
            lock (gate)
            {
                request = JsonCoding.Decode<AttachmentGetParams>(p);
                gets.Add(request);
            }
            return JsonCoding.EncodeToString(new AttachmentGetResult
            {
                AttachmentId = request.AttachmentId,
                Filename = "quoted.png",
                ContentType = "image/png",
                Size = 3,
                Data = [1, 2, 3],
            });
        }

        private IReadOnlyList<T> Locked<T>(List<T> list)
        {
            lock (gate)
            {
                return [.. list];
            }
        }
    }

    private sealed class Harness : IAsyncDisposable
    {
        private Harness()
        {
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeDaemon Fake { get; } = new();

        public Script Script { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public CidRegistry Registry { get; } = new();

        /// <summary>The window's identity (the From row).</summary>
        public AccountId Account { get; set; } = "acc1";

        public ComposeAttachmentsController Attachments { get; private set; } = null!;

        public List<string> Statuses { get; } = [];

        public List<string> Toasts { get; } = [];

        public int Changes { get; private set; }

        public int Edits { get; private set; }

        public int Failures { get; private set; }

        public static async Task<Harness> StartAsync()
        {
            var h = new Harness();
            h.Fake.On(API.AttachmentImport.Name, h.Script.Import);
            h.Fake.On(API.AttachmentRemove.Name, h.Script.Remove);
            h.Fake.On(API.AttachmentGet.Name, h.Script.Get);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            h.Attachments = await h.Ui.RunAsync(() =>
            {
                var a = new ComposeAttachmentsController(h.Client, () => h.Account, h.Registry, h.Pending);
                a.Changed += (_, _) => h.Changes++;
                a.Edited += (_, _) => h.Edits++;
                a.ImportFailed += (_, _) => h.Failures++;
                a.StatusChanged += (_, s) => h.Statuses.Add(s);
                a.ToastRequested += (_, t) => h.Toasts.Add(t);
                return a;
            });
            return h;
        }

        public Task Run(Action action) => Ui.RunAsync(action);

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() => Attachments.Dispose());
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
