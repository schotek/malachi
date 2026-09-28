// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/MailPreferencesTests.swift: the Mail
// group of the preferences against a fake daemon
// (ui/internal/window/preferences.go bindMail, which has no Go test;
// preferences_test.go TestAttachmentDaysApply is AttachmentDaysApplyTest).
// Swift waits with waitUntil and sleeps for the negative cases; here the
// test waits until nothing is left to happen (Quiescence.IdleAsync), and a
// slow answer is held until the test releases it (HeldAnswer). A close
// comes once the call has reached the daemon: a Close in the same UI turn
// would keep it from being sent at all (docs/windows-port.md §7.2), where
// Swift's deferred Task still sends it.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Model;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class MailPreferencesTests
{
    private static readonly Preferences Stored = new() { SyncIntervalSeconds = 300, RemoteContent = RemoteContentPolicy.Block, OfflineDays = 30 };

    // What a daemon that knows the storage preferences reports.
    private static readonly Preferences StoredFull = Stored with { CompressStore = true, AttachmentOfflineDays = 30 };

    // What a daemon that also knows neverStoreAttachments reports.
    private static readonly Preferences StoredAll = StoredFull with { NeverStoreAttachments = false };

    [Fact]
    public void SelectionMapsValuesOntoPopupPositions()
    {
        Assert.Equal(new MailPreferencesController.MailSelection(1, 0, 1), new MailPreferencesController.MailSelection(Stored));
        var odd = new Preferences { SyncIntervalSeconds = 1000, RemoteContent = RemoteContentPolicy.Allow, OfflineDays = 400 };
        Assert.Equal(new MailPreferencesController.MailSelection(2, 2, 3), new MailPreferencesController.MailSelection(odd));
        var manual = new Preferences { SyncIntervalSeconds = 0, RemoteContent = RemoteContentPolicy.KnownSenders, OfflineDays = 0 };
        Assert.Equal(new MailPreferencesController.MailSelection(0, 1, 4), new MailPreferencesController.MailSelection(manual));
    }

    [Fact]
    public async Task DisabledUntilLoaded()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.MakeControllerAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.IsEnabled);
            Assert.Null(c.Preferences);
            Assert.Null(c.Selection);
            // A change before the load has nothing to base itself on.
            c.SetCheckInterval(900);
            c.SelectRetention(0);
        });
        await h.IdleAsync();
        Assert.Empty(h.Sent);
        Assert.Empty(rec.Enabled);

        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.Equal(Stored, c.Preferences);
        Assert.Equal(new MailPreferencesController.MailSelection(1, 0, 1), c.Selection);
        Assert.Equal([Stored], rec.Preferences);
        Assert.Equal([true], rec.Enabled);
        Assert.Empty(rec.Descriptions);
        Assert.Empty(rec.Toasts);
        Assert.Empty(h.Sent);
    }

    [Fact]
    public async Task LoadFailureGoesIntoTheDescriptionAndKeepsTheGroupInsensitive()
    {
        await using var h = await Harness.StartAsync();
        h.Fake.On(API.ConfigGet.Name, Fails(ErrorCode.InternalError, "no store"));
        var (c, rec) = await h.MakeControllerAsync();
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Equal(["Loading mail settings failed"], rec.Descriptions);
        Assert.False(c.IsEnabled);
        Assert.Null(c.Preferences);
        Assert.Empty(rec.Preferences);
        Assert.Empty(rec.Toasts);
    }

    [Fact]
    public async Task LoadWithoutADaemonReportsTheBackend()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.MakeControllerAsync();
        h.Client.Close();
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Equal(["Loading mail settings needs a running mail backend"], rec.Descriptions);
        Assert.False(c.IsEnabled);
        Assert.Null(c.Preferences);
    }

    [Fact]
    public async Task SetSendsTheWholeSetWithOneFieldChanged()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.LoadedControllerAsync();

        await h.Ui.RunAsync(() =>
        {
            c.SetCheckInterval(900);
            Assert.False(c.IsEnabled, "insensitive while the save is in flight");
        });
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        var want1 = new Preferences { SyncIntervalSeconds = 900, RemoteContent = RemoteContentPolicy.Block, OfflineDays = 30 };
        Assert.Equal([want1], h.Sent);
        Assert.Equal(want1, c.Preferences);

        await h.Ui.RunAsync(() => c.SetRemoteContent(RemoteContentPolicy.Allow));
        await h.IdleAsync();
        var want2 = new Preferences { SyncIntervalSeconds = 900, RemoteContent = RemoteContentPolicy.Allow, OfflineDays = 30 };
        Assert.Equal(want2, h.Sent[^1]);

        await h.Ui.RunAsync(() => c.SetOfflineDays(0));
        await h.IdleAsync();
        var want3 = new Preferences { SyncIntervalSeconds = 900, RemoteContent = RemoteContentPolicy.Allow, OfflineDays = 0 };
        Assert.Equal(want3, h.Sent[^1]);
        Assert.Equal(want3, c.Preferences);

        Assert.Equal([Stored, want1, want2, want3], rec.Preferences);
        Assert.Equal([true, false, true, false, true, false, true], rec.Enabled);
        Assert.Empty(rec.Toasts);
        Assert.Empty(rec.Descriptions);
    }

    [Fact]
    public async Task PopupPositionsMapThroughTheChoiceTables()
    {
        await using var h = await Harness.StartAsync();
        var (c, _) = await h.LoadedControllerAsync();

        await h.Ui.RunAsync(() => c.SelectInterval(3));
        await h.IdleAsync();
        Assert.Equal(1800, h.Sent[^1].SyncIntervalSeconds);
        await h.Ui.RunAsync(() => c.SelectRemoteContent(1));
        await h.IdleAsync();
        Assert.Equal(RemoteContentPolicy.KnownSenders, h.Sent[^1].RemoteContent);
        await h.Ui.RunAsync(() => c.SelectRetention(4));
        await h.IdleAsync();
        Assert.Equal(0, h.Sent[^1].OfflineDays);
        await h.Ui.RunAsync(() => c.SelectRetention(0));
        await h.IdleAsync();
        Assert.Equal(7, h.Sent[^1].OfflineDays);

        // Positions outside the tables are ignored.
        await h.Ui.RunAsync(() =>
        {
            c.SelectInterval(4);
            c.SelectRemoteContent(-1);
            c.SelectRetention(5);
            Assert.True(c.IsEnabled);
        });
        await h.IdleAsync();
        Assert.Equal(4, h.Sent.Count);
    }

    [Fact]
    public async Task TheDaemonsEchoIsWhatIsShown()
    {
        // The daemon clamps the interval to its minimum.
        await using var h = await Harness.StartAsync(normalise: p => p with { SyncIntervalSeconds = Math.Max(p.SyncIntervalSeconds, 600) });
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetCheckInterval(300));
        await h.IdleAsync();
        var echoed = new Preferences { SyncIntervalSeconds = 600, RemoteContent = RemoteContentPolicy.Block, OfflineDays = 30 };
        Assert.Equal(300, h.Sent[^1].SyncIntervalSeconds);
        Assert.Equal(echoed, c.Preferences);
        Assert.Equal(echoed, rec.Preferences[^1]);
    }

    [Fact]
    public async Task FailedSaveToastsAndReverts()
    {
        await using var h = await Harness.StartAsync();
        h.Fake.On(API.ConfigSet.Name, Fails(ErrorCode.InvalidArgument, "offlineDays out of range"));
        var (c, rec) = await h.LoadedControllerAsync();

        await h.Ui.RunAsync(() =>
        {
            c.SetOfflineDays(-3);
            Assert.False(c.IsEnabled);
        });
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.Equal(["Saving mail settings was rejected: offlineDays out of range"], rec.Toasts);
        Assert.Equal(Stored, c.Preferences); // the last confirmed values stay
        Assert.Equal([Stored, Stored], rec.Preferences); // rendered again so the pop-ups revert
        Assert.Equal([true, false, true], rec.Enabled);
        Assert.Empty(rec.Descriptions);

        // The next change bases itself on the confirmed values, not the
        // rejected ones.
        h.ServeConfigSet();
        await h.Ui.RunAsync(() => c.SetRemoteContent(RemoteContentPolicy.Allow));
        await h.IdleAsync();
        Assert.Equal(new Preferences { SyncIntervalSeconds = 300, RemoteContent = RemoteContentPolicy.Allow, OfflineDays = 30 }, h.Sent[^1]);
    }

    [Fact]
    public async Task AStaleReplyDoesNotOverwriteANewerOne()
    {
        var slow = new HeldAnswer();
        await using var h = await Harness.StartAsync(hold: p => p.SyncIntervalSeconds == 900 ? slow : null);
        var (c, rec) = await h.LoadedControllerAsync();

        await h.Ui.RunAsync(() =>
        {
            c.SetCheckInterval(900); // slow
            c.SetCheckInterval(1800); // fast, answers first
        });
        await rec.Conditions.WhenAsync(h.Ui, () => c.IsEnabled);
        var newer = new Preferences { SyncIntervalSeconds = 1800, RemoteContent = RemoteContentPolicy.Block, OfflineDays = 30 };
        Assert.Equal(newer, c.Preferences);
        Assert.Equal(newer, rec.Preferences[^1]);
        var renders = rec.Preferences.Count;
        var toggles = rec.Enabled.Count;

        // The slow reply arrives and is dropped.
        slow.Release();
        await h.IdleAsync();
        Assert.Equal(1, slow.Arrivals);
        Assert.Equal(newer, c.Preferences);
        Assert.Equal(renders, rec.Preferences.Count);
        Assert.Equal(toggles, rec.Enabled.Count);
        Assert.True(c.IsEnabled);
    }

    [Fact]
    public async Task CloseDropsLateReplies()
    {
        var held = new HeldAnswer();
        await using var h = await Harness.StartAsync(hold: _ => held);
        var (c, rec) = await h.LoadedControllerAsync();

        await h.Ui.RunAsync(() =>
        {
            c.SetCheckInterval(0);
            Assert.False(c.IsEnabled);
        });
        await held.ArrivedAsync();
        await h.Ui.RunAsync(() =>
        {
            c.Close();
            Assert.True(c.IsClosed);
        });
        var renders = rec.Preferences.Count;
        var toggles = rec.Enabled.Count;
        held.Release();
        await h.IdleAsync();
        Assert.Single(h.Sent); // the request itself went out
        Assert.Equal(renders, rec.Preferences.Count);
        Assert.Equal(toggles, rec.Enabled.Count);
        Assert.Empty(rec.Toasts);
        // Nothing starts after close either.
        await h.Ui.RunAsync(() =>
        {
            c.Load();
            c.SetOfflineDays(7);
        });
        await h.IdleAsync();
        Assert.Single(h.Sent);
        Assert.Equal(renders, rec.Preferences.Count);
    }

    // Storage preferences (compressStore, attachmentOfflineDays)

    [Fact]
    public void SelectionMapsTheStoragePreferences()
    {
        Assert.Equal(new MailPreferencesController.MailSelection(1, 0, 1, Attachments: 2, Compress: true), new MailPreferencesController.MailSelection(StoredFull));
        var small = Stored with { CompressStore = false, AttachmentOfflineDays = -1 };
        Assert.Equal(0, new MailPreferencesController.MailSelection(small).Attachments);
        Assert.False(new MailPreferencesController.MailSelection(small).Compress);
        var everything = Stored with { AttachmentOfflineDays = 0 };
        Assert.Equal(4, new MailPreferencesController.MailSelection(everything).Attachments);
        // An older daemon: the rows are hidden.
        var old = new MailPreferencesController.MailSelection(Stored);
        Assert.True(old.Attachments is null && old.Compress is null);
    }

    [Fact]
    public async Task StoragePreferencesAreSentWithTheWholeSet()
    {
        await using var h = await Harness.StartAsync(initial: StoredFull);
        var (c, rec) = await h.LoadedControllerAsync();
        Assert.Equal(StoredFull, c.Preferences);

        await h.Ui.RunAsync(() =>
        {
            c.SetCompressStore(false);
            Assert.False(c.IsEnabled);
        });
        await h.IdleAsync();
        var want = StoredFull with { CompressStore = false };
        Assert.Equal([want], h.Sent);
        Assert.Equal(1, rec.Saved);

        await h.Ui.RunAsync(() => c.SelectAttachmentDays(0));
        await h.IdleAsync();
        want = want with { AttachmentOfflineDays = -1 };
        Assert.Equal(want, h.Sent[^1]);
        await h.Ui.RunAsync(() => c.SelectAttachmentDays(4));
        await h.IdleAsync();
        want = want with { AttachmentOfflineDays = 0 };
        Assert.Equal(want, h.Sent[^1]);
        // False and 0 go over the wire.
        Assert.Equal(["attachmentOfflineDays", "compressStore", "offlineDays", "remoteContent", "syncIntervalSeconds"], h.Keys[^1]);
        // A position outside the table is ignored.
        await h.Ui.RunAsync(() =>
        {
            c.SelectAttachmentDays(5);
            c.SelectAttachmentDays(-1);
            Assert.True(c.IsEnabled);
        });
        await h.IdleAsync();
        Assert.Equal(3, h.Sent.Count);
        Assert.Equal(3, rec.Saved);
        Assert.Equal(want, c.Preferences);

        // Another row keeps the storage values as confirmed.
        await h.Ui.RunAsync(() => c.SetCheckInterval(900));
        await h.IdleAsync();
        Assert.True(h.Sent[^1].CompressStore == false && h.Sent[^1].AttachmentOfflineDays == 0);
        Assert.Empty(rec.Toasts);
    }

    /// <summary>
    /// A value between the pop-up's positions (14 days from config.toml) is
    /// shown at its nearest one and goes back unchanged when another row is
    /// saved; only a choice in its own pop-up replaces it (preferences.go
    /// <c>attachmentDaysToSave</c>).
    /// </summary>
    [Fact]
    public async Task AnUntouchedAttachmentValueIsKept()
    {
        var initial = StoredFull with { AttachmentOfflineDays = 14 };
        await using var h = await Harness.StartAsync(initial: initial);
        var (c, _) = await h.LoadedControllerAsync();
        Assert.Equal(1, new MailPreferencesController.MailSelection(initial).Attachments); // shown as 1 week

        await h.Ui.RunAsync(() => c.SetCheckInterval(900));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => c.SetCompressStore(false));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => c.SelectRetention(2));
        await h.IdleAsync();
        Assert.Equal(new int?[] { 14, 14, 14 }, h.Sent.Select(p => p.AttachmentOfflineDays));

        await h.Ui.RunAsync(() => c.SelectAttachmentDays(2));
        await h.IdleAsync();
        Assert.Equal(30, h.Sent[^1].AttachmentOfflineDays);
    }

    /// <summary>
    /// An older daemon does not report them: their setters do nothing and the
    /// fields never go back (absent is "unchanged" to a newer one).
    /// </summary>
    [Fact]
    public async Task AnOlderDaemonsSetIsSentWithoutThem()
    {
        await using var h = await Harness.StartAsync();
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() =>
        {
            c.SetCompressStore(true);
            c.SetAttachmentOfflineDays(7);
            c.SelectAttachmentDays(1);
            Assert.True(c.IsEnabled);
        });
        await h.IdleAsync();
        Assert.Empty(h.Sent);

        await h.Ui.RunAsync(() => c.SetOfflineDays(90));
        await h.IdleAsync();
        Assert.Single(h.Keys);
        Assert.Equal(["offlineDays", "remoteContent", "syncIntervalSeconds"], h.Keys[0]);
        Assert.Equal(1, rec.Saved);
    }

    // Never Store Attachments (neverStoreAttachments)

    /// <summary>
    /// preferences_test.go TestAttachmentDaysApply: Keep Attachments Offline
    /// For is insensitive only while the daemon confirms that attachments are
    /// never stored.
    /// </summary>
    [Fact]
    public void AttachmentDaysApplyTest()
    {
        (string Name, Preferences P, bool Want)[] cases =
        [
            ("older daemon, field absent", Stored with { AttachmentOfflineDays = 30 }, true),
            ("never store off", Stored with { AttachmentOfflineDays = 30, NeverStoreAttachments = false }, true),
            ("never store on", Stored with { AttachmentOfflineDays = 30, NeverStoreAttachments = true }, false),
            ("on, days unknown", Stored with { NeverStoreAttachments = true }, false),
        ];
        foreach (var (name, p, want) in cases)
        {
            Assert.True(PreferenceChoices.AttachmentDaysApply(p) == want, name);
        }
    }

    [Fact]
    public void SelectionMapsNeverStore()
    {
        Assert.Equal(
            new MailPreferencesController.MailSelection(1, 0, 1, Attachments: 2, NeverStore: false, Compress: true),
            new MailPreferencesController.MailSelection(StoredAll));
        var on = StoredAll with { NeverStoreAttachments = true };
        Assert.True(new MailPreferencesController.MailSelection(on).NeverStore);
        Assert.Equal(2, new MailPreferencesController.MailSelection(on).Attachments); // the pop-up keeps its value while insensitive
        // The daemons before it: the row is hidden.
        Assert.Null(new MailPreferencesController.MailSelection(StoredFull).NeverStore);
        Assert.Null(new MailPreferencesController.MailSelection(Stored).NeverStore);
    }

    [Fact]
    public async Task NeverStoreIsSentWithTheWholeSet()
    {
        await using var h = await Harness.StartAsync(initial: StoredAll);
        var (c, rec) = await h.LoadedControllerAsync();
        Assert.Equal(StoredAll, c.Preferences);

        await h.Ui.RunAsync(() =>
        {
            c.SetNeverStoreAttachments(true);
            Assert.False(c.IsEnabled, "insensitive while the save is in flight");
        });
        await h.IdleAsync();
        var want = StoredAll with { NeverStoreAttachments = true };
        Assert.Equal([want], h.Sent); // the attachment days go back as confirmed
        Assert.Equal(want, c.Preferences);
        Assert.Equal(want, rec.Preferences[^1]);
        Assert.False(PreferenceChoices.AttachmentDaysApply(rec.Preferences[^1]!));
        Assert.Equal(1, rec.Saved);

        // Another row keeps it as confirmed.
        await h.Ui.RunAsync(() => c.SetCheckInterval(900));
        await h.IdleAsync();
        Assert.True(h.Sent[^1].NeverStoreAttachments);
        Assert.Equal(30, h.Sent[^1].AttachmentOfflineDays);

        await h.Ui.RunAsync(() => c.SetNeverStoreAttachments(false));
        await h.IdleAsync();
        Assert.False(h.Sent[^1].NeverStoreAttachments);
        // False goes over the wire.
        Assert.Equal(
            ["attachmentOfflineDays", "compressStore", "neverStoreAttachments", "offlineDays", "remoteContent", "syncIntervalSeconds"],
            h.Keys[^1]);
        Assert.Equal(3, rec.Saved);
        Assert.Empty(rec.Toasts);
        Assert.True(PreferenceChoices.AttachmentDaysApply(c.Preferences!));
    }

    /// <summary>
    /// The daemon's echo decides, not what was sent: a daemon that keeps it
    /// off shows it off, with the attachment days applying.
    /// </summary>
    [Fact]
    public async Task NeverStoreShowsTheDaemonsEcho()
    {
        await using var h = await Harness.StartAsync(initial: StoredAll, normalise: p => p with { NeverStoreAttachments = false });
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetNeverStoreAttachments(true));
        await h.IdleAsync();
        Assert.True(h.Sent[^1].NeverStoreAttachments);
        Assert.False(c.Preferences!.NeverStoreAttachments);
        Assert.False(rec.Preferences[^1]!.NeverStoreAttachments);
    }

    /// <summary>
    /// A failed save reverts the switch, and with it the insensitive
    /// attachment days, to what the daemon confirmed last.
    /// </summary>
    [Fact]
    public async Task AFailedNeverStoreSaveReverts()
    {
        var initial = StoredAll with { NeverStoreAttachments = true };
        await using var h = await Harness.StartAsync(initial: initial);
        h.Fake.On(API.ConfigSet.Name, Fails(ErrorCode.InternalError, "store busy"));
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() =>
        {
            c.SetNeverStoreAttachments(false);
            Assert.False(c.IsEnabled);
        });
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.Equal(["Saving mail settings failed"], rec.Toasts);
        Assert.Equal(0, rec.Saved);
        Assert.Equal(initial, c.Preferences);
        Assert.Equal([initial, initial], rec.Preferences); // rendered again: the switch on, the attachment days insensitive
        Assert.False(PreferenceChoices.AttachmentDaysApply(initial));
    }

    /// <summary>
    /// The daemons before it (with or without the other storage preferences)
    /// do not report it: its setter does nothing and the field never goes
    /// back, which a newer daemon reads as unchanged.
    /// </summary>
    [Fact]
    public async Task ADaemonWithoutNeverStoreIsSentWithoutIt()
    {
        foreach (var initial in new[] { StoredFull, Stored })
        {
            await using var h = await Harness.StartAsync(initial: initial);
            var (c, rec) = await h.LoadedControllerAsync();
            await h.Ui.RunAsync(() =>
            {
                c.SetNeverStoreAttachments(true);
                c.SetNeverStoreAttachments(false);
                Assert.True(c.IsEnabled);
            });
            await h.IdleAsync();
            Assert.Empty(h.Sent);

            await h.Ui.RunAsync(() => c.SetOfflineDays(90));
            await h.IdleAsync();
            Assert.Single(h.Keys);
            Assert.DoesNotContain("neverStoreAttachments", h.Keys[^1]);
            Assert.Null(c.Preferences!.NeverStoreAttachments);
            Assert.Equal(1, rec.Saved);
            await h.Ui.RunAsync(c.Close);
        }
    }

    [Fact]
    public async Task AFailedSaveIsNotReportedAsSaved()
    {
        await using var h = await Harness.StartAsync(initial: StoredFull);
        h.Fake.On(API.ConfigSet.Name, Fails(ErrorCode.InvalidArgument, "attachmentOfflineDays out of range"));
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetAttachmentOfflineDays(99999));
        await h.IdleAsync();
        Assert.Equal(0, rec.Saved);
        Assert.Equal(["Saving mail settings was rejected: attachmentOfflineDays out of range"], rec.Toasts);
        Assert.Equal(StoredFull, c.Preferences);
    }

    /// <summary>A handler that fails as the daemon does (Swift's <c>throw RPCError(...)</c>).</summary>
    private static Func<string, string> Fails(int code, string message) =>
        _ => throw new RpcException(new RpcError { Code = code, Message = message });

    /// <summary>Collects what the controller emits (the Swift suite's Recorder).</summary>
    private sealed class Recorder
    {
        public List<Preferences?> Preferences { get; } = [];

        public List<bool> Enabled { get; } = [];

        public List<string> Descriptions { get; } = [];

        public List<string> Toasts { get; } = [];

        public int Saved { get; private set; }

        public UiConditions Conditions { get; } = new();

        public void Attach(MailPreferencesController c)
        {
            c.PreferencesChanged += (_, p) => Note(() => Preferences.Add(p));
            c.EnabledChanged += (_, on) => Note(() => Enabled.Add(on));
            c.DescriptionChanged += (_, text) => Note(() => Descriptions.Add(text));
            c.ToastRequested += (_, text) => Note(() => Toasts.Add(text));
            c.Saved += (_, _) => Note(() => Saved++);
            c.PropertyChanged += (_, _) => Conditions.Changed();
        }

        private void Note(Action record)
        {
            record();
            Conditions.Changed();
        }
    }

    /// <summary>
    /// A daemon that serves <see cref="Stored"/> (or another initial set)
    /// from config.get and echoes config.set (after the daemon's validation)
    /// into <see cref="Sent"/>, with the keys of each preference object as
    /// it went over the wire, the UI thread and a connected client.
    /// </summary>
    private sealed class Harness : IAsyncDisposable
    {
        private readonly object gate = new();
        private readonly List<Preferences> sent = [];
        private readonly List<string[]> keys = [];
        private readonly Func<Preferences, HeldAnswer?>? hold;
        private readonly Func<Preferences, Preferences> normalise;

        private Harness(Func<Preferences, HeldAnswer?>? hold, Func<Preferences, Preferences>? normalise)
        {
            this.hold = hold;
            this.normalise = normalise ?? (p => p);
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeDaemon Fake { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        /// <summary>What the daemon was asked to store, in order.</summary>
        public IReadOnlyList<Preferences> Sent
        {
            get
            {
                lock (gate)
                {
                    return [.. sent];
                }
            }
        }

        /// <summary>The sorted keys of each preference object config.set received, in order.</summary>
        public IReadOnlyList<string[]> Keys
        {
            get
            {
                lock (gate)
                {
                    return [.. keys];
                }
            }
        }

        public static async Task<Harness> StartAsync(
            Func<Preferences, HeldAnswer?>? hold = null, Func<Preferences, Preferences>? normalise = null, Preferences? initial = null)
        {
            var h = new Harness(hold, normalise);
            h.Fake.On(API.ConfigGet.Name, _ => JsonCoding.EncodeToString(new ConfigGetResult { Preferences = initial ?? Stored }));
            h.ServeConfigSet();
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            return h;
        }

        /// <summary>config.set records, waits for its hold if any, and echoes.</summary>
        public void ServeConfigSet() => Fake.On(API.ConfigSet.Name, async json =>
        {
            var p = JsonCoding.Decode<ConfigSetParams>(json).Preferences;
            var names = ApiJson.Keys(ApiJson.Parse(json).GetProperty("preferences"));
            lock (gate)
            {
                sent.Add(p);
                keys.Add(names);
            }
            if (hold?.Invoke(p) is { } held)
            {
                await held.WaitAsync();
            }
            return JsonCoding.EncodeToString(new ConfigSetResult { Preferences = normalise(p) });
        });

        public Task<(MailPreferencesController, Recorder)> MakeControllerAsync() => Ui.RunAsync(() =>
        {
            var c = new MailPreferencesController(Client, pending: Pending);
            var rec = new Recorder();
            rec.Attach(c);
            return (c, rec);
        });

        /// <summary>A controller whose load has answered.</summary>
        public async Task<(MailPreferencesController, Recorder)> LoadedControllerAsync()
        {
            var (c, rec) = await MakeControllerAsync();
            await Ui.RunAsync(c.Load);
            await IdleAsync();
            Assert.True(c.IsEnabled);
            return (c, rec);
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        public async ValueTask DisposeAsync()
        {
            Client?.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
