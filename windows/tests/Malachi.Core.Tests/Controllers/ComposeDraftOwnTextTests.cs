// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ComposeDraftOwnTextTests.swift (every
// test); GTK: ui/internal/compose/pane.go (loadEditor, editorLoaded) and
// comment_test.go's own-text cases of closesUnasked (those are in
// JiraComposeControllerTests.CloseAsksOnlyWithSomethingToLose).
// ComposeDraftController.EditorLoaded: a window that started with the user's
// own text (ComposeParams.OwnHtml, the chat's quick-reply bar) marks its
// draft dirty once the editor loaded it, behind one flush, so that the
// autosave and the question on close apply; once, not again after a crash's
// reload; the board's pane still learns how the editor writes its draft.
//
// One Windows difference: every Ready flushes once for the baseline of the
// flush echo (EditorReady, for every owner), so a window without own text
// flushes once where Swift's does not, and "once" is told by the status line
// (MarkDirty's RefreshStatus) rather than by the count of flushes. No RPC is
// made (the autosave's clock never moves), so the client never connects.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Html;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Model;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class ComposeDraftOwnTextTests
{
    /// <summary>The own text is the user's edit: dirty behind one flush, the autosave armed, and closing asks.</summary>
    [Fact]
    public async Task OwnTextMarksTheDraftDirty()
    {
        await using var h = await Harness.StartAsync();
        h.Form.Unreported = "Thanks";
        await h.Run(() =>
        {
            Assert.True(h.Draft.CanCloseWithoutAsking);
            h.Draft.EditorLoaded(ownText: true);
            Assert.Equal(1, h.Form.Flushes);
            Assert.True(h.Draft.Draft.Dirty);
            Assert.True(h.Draft.AutosaveArmed);
            Assert.False(h.Draft.CanCloseWithoutAsking);
            Assert.Equal(["Unsaved changes"], h.Form.Statuses);
        });
    }

    /// <summary>Once: a reload's Ready flushes for its baseline but marks nothing again.</summary>
    [Fact]
    public async Task OnceNotAgainAfterAReload()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() =>
        {
            h.Draft.EditorLoaded(ownText: true);
            h.Draft.EditorLoaded(ownText: true);
            Assert.Equal(2, h.Form.Flushes);
            Assert.Single(h.Form.Statuses);
        });
    }

    /// <summary>
    /// Without own text nothing is marked: the prefill is no edit and the
    /// window closes unasked (the one flush is the echo's baseline).
    /// </summary>
    [Fact]
    public async Task WithoutOwnTextNothingChanges()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() =>
        {
            h.Draft.EditorLoaded(ownText: false);
            Assert.Equal(1, h.Form.Flushes);
            Assert.False(h.Draft.Draft.Dirty);
            Assert.False(h.Draft.AutosaveArmed);
            Assert.True(h.Draft.CanCloseWithoutAsking);
            Assert.Empty(h.Form.Statuses);
        });
    }

    /// <summary>
    /// A comment whose editor never reported its text counted as empty and
    /// would close unasked; the flush makes the own text known, so it asks.
    /// </summary>
    [Fact]
    public async Task ACommentWithOwnTextAsks()
    {
        await using var h = await Harness.StartAsync();
        h.Form.IsComment = true;
        h.Form.Unreported = "Fixed in 1.2";
        await h.Run(() =>
        {
            Assert.True(h.Draft.CanCloseWithoutAsking);
            h.Draft.EditorLoaded(ownText: true);
            Assert.Equal("Fixed in 1.2", h.Form.EditorText());
            Assert.False(h.Draft.CanCloseWithoutAsking);
        });
    }

    /// <summary>
    /// The board's pane learns how the editor writes its draft (one flush,
    /// EditorReady), after every reload; it never has own text.
    /// </summary>
    [Fact]
    public async Task TheBoardStillLearnsTheEditor()
    {
        await using var h = await Harness.StartAsync(DraftOwner.Board);
        await h.Run(() =>
        {
            h.Draft.EditorLoaded(ownText: false);
            h.Draft.EditorLoaded(ownText: false);
            Assert.Equal(2, h.Form.Flushes);
            Assert.False(h.Draft.Draft.Dirty);
        });
    }

    /// <summary>
    /// Closing before the editor reported Ready asks: the form set
    /// OwnTextPending before it loaded the editor, and the draft is not dirty
    /// yet (a comment's editor has reported no text either). EditorLoaded
    /// clears it once the draft is marked dirty.
    /// </summary>
    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task ClosingBeforeReadyAsks(bool comment)
    {
        await using var h = await Harness.StartAsync();
        h.Form.IsComment = comment;
        h.Form.Unreported = "Thanks";
        await h.Run(() =>
        {
            h.Draft.OwnTextPending = true;
            Assert.True(h.Draft.Draft.OwnTextPending);
            Assert.False(h.Draft.Draft.Dirty);
            Assert.False(h.Draft.CanCloseWithoutAsking);
            h.Draft.EditorLoaded(ownText: true);
            Assert.False(h.Draft.OwnTextPending);
            Assert.True(h.Draft.Draft.Dirty);
            Assert.False(h.Draft.CanCloseWithoutAsking);
        });
    }

    /// <summary>A closed window marks nothing.</summary>
    [Fact]
    public async Task AClosedWindowMarksNothing()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() =>
        {
            h.Draft.Cleanup();
            h.Draft.EditorLoaded(ownText: true);
            Assert.Equal(0, h.Form.Flushes);
            Assert.False(h.Draft.Draft.Dirty);
        });
    }

    /// <summary>
    /// The flush's report is the echo: its Changed (which the window forwards
    /// after the flush's done) is no second edit.
    /// </summary>
    [Fact]
    public async Task TheFlushsChangedIsNoEdit()
    {
        await using var h = await Harness.StartAsync();
        h.Form.Unreported = "Thanks";
        await h.Run(() =>
        {
            h.Draft.EditorLoaded(ownText: true);
            h.Draft.EditorChanged();
            Assert.Single(h.Form.Statuses);
        });
    }

    // The controller over a client that never connects, a form that plays the
    // editor, and a clock that never moves; on a UI thread of its own.
    private sealed class Harness : IAsyncDisposable
    {
        private Harness()
        {
        }

        public SettingsStore Settings { get; } = CollapseStateTests.Scratch();

        public RpcClient Client { get; } = new("/nonexistent/malachi-test.sock", PortableKeyFilePolicy.Instance);

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Time { get; } = new();

        public OwnTextForm Form { get; } = new();

        public ComposeDraftController Draft { get; private set; } = null!;

        public static async Task<Harness> StartAsync(DraftOwner owner = DraftOwner.Window)
        {
            var h = new Harness();
            await h.Ui.RunAsync(() =>
            {
                h.Draft = new ComposeDraftController(h.Client, h.Settings, () => false, new CidRegistry(), h.Time, h.Pending, owner: owner)
                {
                    Form = h.Form,
                };
            });
            return h;
        }

        public Task Run(Action action) => Ui.RunAsync(action);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() => Draft.Cleanup());
            Client.Dispose();
            Settings.Dispose();
            Ui.Dispose();
        }
    }

    /// <summary>
    /// A form whose editor reports its text only with a flush
    /// (<see cref="Unreported"/>), as the bridge does before its first
    /// changed; a flush answers at once.
    /// </summary>
    private sealed class OwnTextForm : IComposeForm
    {
        private string html = "";
        private string text = "";

        public Account Account { get; } = MailModelTests.TestAccount("a", email: "me@example.invalid");

        public bool IsComment { get; set; }

        public string Subject => "Re: Lunch";

        public IReadOnlyList<DraftAttachment> Attachments { get; private set; } = [];

        /// <summary>Typed before the window opened, not reported yet: the next flush reports it.</summary>
        public string? Unreported { get; set; }

        public int Flushes { get; private set; }

        public List<string> Statuses { get; } = [];

        public (IReadOnlyList<Address> To, IReadOnlyList<Address> Cc, IReadOnlyList<Address> Bcc, bool Ok) Recipients() =>
            ([new Address { Name = "Jana", Email = "jana@example.org" }], [], [], true);

        public string EditorHtml() => html;

        public string EditorText() => text;

        public void FlushEditor(Action done)
        {
            Flushes++;
            if (Unreported is { } s)
            {
                Unreported = null;
                html = "<p>" + s + "</p>";
                text = s;
            }
            done();
        }

        public void SetAttachments(IReadOnlyList<DraftAttachment> attachments) => Attachments = attachments;

        public void SetStatus(string text)
        {
            // RefreshStatus says "" for a comment and before any edit; only
            // what it says of an edit counts here.
            if (text.Length > 0)
            {
                Statuses.Add(text);
            }
        }

        public void Toast(string text)
        {
        }

        public void SetSendEnabled(bool enabled)
        {
        }

        public void CloseWindow()
        {
        }
    }
}
