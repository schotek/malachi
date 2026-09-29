// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/SettingsTests.swift, the counterpart
// of ui/internal/settings/store_test.go (TestMemoryDefaults,
// TestMemorySetAndNotify, TestMemoryValidation, TestHandlerMayRemoveItself,
// TestFavouriteFoldersRoundTrip, TestStringListRoundTrip, and TestCoerce,
// which the Swift suite did not port; TestOpenFallsBack is in
// Malachi.Platform.Windows.Tests, RegistrySettingsBackendTests). The Swift
// scratch UserDefaults suite is an InMemorySettingsBackend here; the
// registry backend has its own tests.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Fixtures;
using Xunit;

namespace Malachi.Core.Tests.Settings;

public sealed class SettingsTests
{
    private static readonly TimeSpan Timeout = TimeSpan.FromSeconds(10);

    [Fact]
    public void Defaults()
    {
        var s = Scratch();
        Assert.Equal(ColorScheme.System, s.ColorScheme);
        Assert.Equal(Density.Comfortable, s.Density);
        Assert.True(s.ShowPreviewLine);
        Assert.True(s.ShowAvatars);
        Assert.False(s.MonochromeAvatars);
        Assert.False(s.MonospacePlainText);
        Assert.False(s.GroupByConversation);
        Assert.Equal(100, s.TextZoom);
        Assert.False(s.LaunchAtLogin);
        Assert.False(s.RunInBackground);
        Assert.True(s.ConfirmDelete);
        Assert.True(s.DesktopNotifications);
        Assert.False(s.NotificationSound);
        Assert.Equal(2, s.MarkReadDelay);
        Assert.Equal(CtrlR.Reply, s.CtrlR);
        Assert.Empty(s.CollapsedFolders);
        Assert.Empty(s.CollapsedAccounts);
        Assert.Empty(s.FavouriteFolders);
        s.MarkReadDelay = 999;
        Assert.Equal(SettingsStore.MarkReadDelayMax, s.MarkReadDelay);
        Assert.Equal(SearchScope.Folder, s.SearchScope);
        Assert.True(s.AssistantMenu);
        Assert.Equal(AssistantTarget.Desktop, s.AssistantTarget);
        // The gschema's window geometry, which Windows uses (GTK does not).
        Assert.Equal(1200, s.WindowWidth);
        Assert.Equal(760, s.WindowHeight);
        Assert.False(s.WindowMaximized);
        Assert.Equal(240, s.FolderPaneWidth);
        Assert.Equal(380, s.MessageListWidth);
        // The 25 gschema keys and ctrl-r.
        Assert.Equal(26, SettingsStore.Schema.Count);
        Assert.Equal(Enum.GetValues<SettingsKey>().Length, SettingsStore.Schema.Count);
        Assert.Equal(
            SettingsStore.Schema.Select(k => k.Name).Order(StringComparer.Ordinal),
            SettingsStore.RegistrationDefaults().Keys.Order(StringComparer.Ordinal));
        Assert.False(s.Persistent);
    }

    [Fact]
    public void SetAndNotify()
    {
        var backend = new InMemorySettingsBackend();
        var s = new SettingsStore(backend, null);
        var calls = 0;
        var token = s.OnChange(SettingsKey.TextZoom, () => calls++);

        s.TextZoom = 120;
        Assert.Equal(120, s.TextZoom);
        Assert.Equal(1, calls);
        Assert.True(backend.TryGetInt32("text-zoom", out var stored));
        Assert.Equal(120, stored);
        s.TextZoom = 120; // unchanged: no notification
        Assert.Equal(1, calls);
        token.Cancel();
        s.TextZoom = 130;
        Assert.Equal(1, calls);
    }

    [Fact]
    public void Validation()
    {
        var backend = new InMemorySettingsBackend();
        var s = new SettingsStore(backend, null);

        s.TextZoom = 10;
        Assert.Equal(SettingsStore.TextZoomMin, s.TextZoom);
        s.TextZoom = 1000;
        Assert.Equal(SettingsStore.TextZoomMax, s.TextZoom);
        // An out-of-range value written from outside reads clamped too.
        backend.SetInt32("text-zoom", 999);
        Assert.Equal(SettingsStore.TextZoomMax, s.TextZoom);
        backend.SetInt32("mark-read-delay", -5);
        Assert.Equal(0, s.MarkReadDelay);

        backend.SetString("color-scheme", "neon");
        Assert.Equal(ColorScheme.System, s.ColorScheme);
        s.ColorScheme = ColorScheme.Dark;
        Assert.Equal(ColorScheme.Dark, s.ColorScheme);

        backend.SetString("message-list-density", "sardine");
        Assert.Equal(Density.Comfortable, s.Density);
        s.Density = Density.Compact;
        Assert.Equal(Density.Compact, s.Density);
        backend.SetString("search-scope", "everywhere");
        Assert.Equal(SearchScope.Folder, s.SearchScope);
        s.SearchScope = SearchScope.All;
        Assert.Equal(SearchScope.All, s.SearchScope);
        backend.SetString("assistant-target", "browser");
        Assert.Equal(AssistantTarget.Desktop, s.AssistantTarget);
        s.AssistantTarget = AssistantTarget.Code;
        Assert.Equal(AssistantTarget.Code, s.AssistantTarget);

        backend.SetString("ctrl-r", "dance");
        Assert.Equal(CtrlR.Reply, s.CtrlR);
        s.CtrlR = CtrlR.Refresh;
        Assert.Equal(CtrlR.Refresh, s.CtrlR);

        // GTK's SetColorScheme("neon"): a value outside the enum is ignored.
        var calls = 0;
        s.OnChange(SettingsKey.ColorScheme, () => calls++);
        s.ColorScheme = (ColorScheme)42;
        Assert.Equal(ColorScheme.Dark, s.ColorScheme);
        Assert.Equal(0, calls);
        // Nicks are case-sensitive, as GSettings' are; the wrong type is unset.
        backend.SetString("color-scheme", "Light");
        Assert.Equal(ColorScheme.System, s.ColorScheme);
        backend.SetString("text-zoom", "150");
        Assert.Equal(100, s.TextZoom);
    }

    [Fact]
    public void HandlerMayRemoveItself()
    {
        var s = Scratch();
        var calls = 0;
        SettingsChangeToken? token = null;
        token = s.OnChange(SettingsKey.ShowAvatars, () =>
        {
            calls++;
            token?.Cancel();
        });
        s.ShowAvatars = false;
        s.ShowAvatars = true;
        Assert.Equal(1, calls);
    }

    [Fact]
    public void HandlersFireInRegistrationOrderAndPerKey()
    {
        var s = Scratch();
        var order = new List<string>();
        s.OnChange(SettingsKey.ConfirmDelete, () => order.Add("a"));
        s.OnChange(SettingsKey.ConfirmDelete, () => order.Add("b"));
        s.OnChange(SettingsKey.NotificationSound, () => order.Add("other"));
        s.ConfirmDelete = false;
        Assert.Equal(["a", "b"], order);
    }

    [Fact]
    public void FavouriteFoldersRoundTrip()
    {
        var s = Scratch();
        Assert.Empty(s.FavouriteFolders);
        var changed = 0;
        s.OnChange(SettingsKey.FavouriteFolders, () => changed++);
        s.FavouriteFolders = ["acc_1/f_1"];
        Assert.Equal(["acc_1/f_1"], s.FavouriteFolders);
        Assert.Equal(1, changed);
    }

    [Fact]
    public void StringListRoundTrip()
    {
        var s = Scratch();
        Assert.Empty(s.CollapsedFolders);
        var changed = 0;
        s.OnChange(SettingsKey.CollapsedFolders, () => changed++);

        s.CollapsedFolders = ["acc_1/f_1", "acc_1/f_2"];
        var got = s.CollapsedFolders;
        Assert.Equal(["acc_1/f_1", "acc_1/f_2"], got);
        Assert.Equal(1, changed);

        // Writing the same contents is not a change.
        s.CollapsedFolders = ["acc_1/f_1", "acc_1/f_2"];
        Assert.Equal(1, changed);

        // The store hands out copies.
        ((string[])got)[0] = "tampered";
        Assert.Equal("acc_1/f_1", s.CollapsedFolders[0]);

        s.CollapsedFolders = [];
        Assert.Empty(s.CollapsedFolders);
        Assert.Equal(2, changed);

        s.CollapsedAccounts = ["acc_2"];
        Assert.Equal(["acc_2"], s.CollapsedAccounts);
        Assert.Equal(2, changed);

        // A list the caller changes afterwards is not the stored one.
        var mine = new List<string> { "a" };
        s.CollapsedAccounts = mine;
        mine.Add("b");
        Assert.Equal(["a"], s.CollapsedAccounts);
        s.CollapsedAccounts = null!;
        Assert.Empty(s.CollapsedAccounts);
    }

    // Swift: another UserDefaults object on the same domain, as another
    // window or process would use. Here a change from outside the store,
    // made on another thread, reaches the handler once, on the UI context
    // captured when the store was made.
    [Fact]
    public async Task ExternalWriteThroughAnotherInstanceNotifies()
    {
        using var ui = new TestUIContext();
        var backend = new InMemorySettingsBackend();
        var s = await ui.RunAsync(() => new SettingsStore(backend));
        var seen = 0;
        var thread = new TaskCompletionSource<int>(TaskCreationOptions.RunContinuationsAsynchronously);
        s.OnChange(SettingsKey.MarkReadDelay, () =>
        {
            Interlocked.Increment(ref seen);
            thread.TrySetResult(Environment.CurrentManagedThreadId);
        });
        await Task.Run(() => backend.ChangeExternally("mark-read-delay", 7), TestContext.Current.CancellationToken);
        Assert.Equal(ui.ThreadId, await thread.Task.WaitAsync(Timeout, TestContext.Current.CancellationToken));
        await ui.DrainAsync();
        Assert.Equal(1, Volatile.Read(ref seen));
        Assert.Equal(7, s.MarkReadDelay);
        Assert.Empty(ui.Failures);
    }

    [Fact]
    public void ValuesPersistAcrossInstances()
    {
        var backend = new InMemorySettingsBackend();
        var first = new SettingsStore(backend, null);
        first.TextZoom = 140;
        first.FavouriteFolders = ["a/b"];
        var again = new SettingsStore(backend, null);
        Assert.Equal(140, again.TextZoom);
        Assert.Equal(["a/b"], again.FavouriteFolders);
    }

    // ui/internal/settings TestCoerce: what a two-way binding of an integer
    // key to a numeric control converts.
    [Fact]
    public void Coerce()
    {
        Assert.Equal(120.0, SettingsStore.Coerce(120, 0.0));
        Assert.Equal(120, SettingsStore.Coerce(120.4, 0));
        Assert.Equal(120, SettingsStore.Coerce(119.6, 0));
        Assert.Equal(true, SettingsStore.Coerce(true, false));
        Assert.Equal("x", SettingsStore.Coerce("x", ""));
        Assert.Equal(7u, SettingsStore.Coerce(7, 0u));
        // Go's math.Round: halves away from zero.
        Assert.Equal(121, SettingsStore.Coerce(120.5, 0));
        Assert.Equal(-121, SettingsStore.Coerce(-120.5, 0));
        Assert.Equal(7.0, SettingsStore.Coerce(7u, 0.0));
    }

    // A change made through the store reaches its handlers before the
    // setter returns, on the thread that made it; one from outside without
    // a context reaches them on the thread that reported it.
    [Fact]
    public async Task HandlersRunOnTheThreadOfTheChange()
    {
        var backend = new InMemorySettingsBackend();
        var s = new SettingsStore(backend, null);
        var threads = new List<int>();
        s.OnChange(SettingsKey.ShowAvatars, () =>
        {
            lock (threads)
            {
                threads.Add(Environment.CurrentManagedThreadId);
            }
        });
        var writer = Environment.CurrentManagedThreadId;
        s.ShowAvatars = false;
        Assert.Equal([writer], threads);
        var worker = await Task.Run(() =>
        {
            backend.ChangeExternally("show-avatars", true);
            return Environment.CurrentManagedThreadId;
        }, TestContext.Current.CancellationToken);
        Assert.Equal([writer, worker], threads);
        Assert.True(s.ShowAvatars);
    }

    // A change reported on the UI thread itself runs at once, as Swift's
    // observer does on the main thread; names are matched as the registry
    // matches them; unknown names are ignored.
    [Fact]
    public async Task ExternalChangesOnTheUIThreadRunAtOnce()
    {
        using var ui = new TestUIContext();
        var backend = new InMemorySettingsBackend();
        var result = await ui.RunAsync(() =>
        {
            var s = new SettingsStore(backend);
            var calls = 0;
            s.OnChange(SettingsKey.TextZoom, () => calls++);
            backend.ChangeExternally("Text-Zoom", 150);
            var first = calls;
            backend.ChangeExternally("no-such-key", 1);
            return (first, calls);
        });
        Assert.Equal((1, 1), result);
    }

    [Fact]
    public async Task DisposedStoreHearsNothingFromOutside()
    {
        using var ui = new TestUIContext();
        var backend = new InMemorySettingsBackend();
        var s = await ui.RunAsync(() => new SettingsStore(backend));
        var calls = 0;
        s.OnChange(SettingsKey.RunInBackground, () => Interlocked.Increment(ref calls));
        s.Dispose();
        s.Dispose();
        await Task.Run(() => backend.ChangeExternally("run-in-background", true), TestContext.Current.CancellationToken);
        await ui.DrainAsync();
        Assert.Equal(0, Volatile.Read(ref calls));
        Assert.True(s.RunInBackground);
        // Its own writes still work and still notify.
        s.RunInBackground = false;
        Assert.Equal(1, calls);
    }

    // The schema rows are in key order and name the keys of the gschema;
    // the enum nicks are the members' names in lower case.
    [Fact]
    public void SchemaDescribesEveryKey()
    {
        foreach (var key in Enum.GetValues<SettingsKey>())
        {
            var info = SettingsStore.Info(key);
            Assert.Equal(key, info.Key);
            Assert.True(SettingsStore.TryParseKey(info.Name, out var parsed));
            Assert.Equal(key, parsed);
            Assert.True(SettingsStore.TryParseKey(info.Name.ToUpperInvariant(), out parsed));
            Assert.Equal(key, parsed);
        }
        Assert.False(SettingsStore.TryParseKey("command-r", out _));
        Assert.Equal(["system", "light", "dark"], SettingsStore.Info(SettingsKey.ColorScheme).Choices);
        Assert.Equal(["comfortable", "compact"], SettingsStore.Info(SettingsKey.Density).Choices);
        Assert.Equal(["folder", "account", "all"], SettingsStore.Info(SettingsKey.SearchScope).Choices);
        Assert.Equal(["desktop", "code"], SettingsStore.Info(SettingsKey.AssistantTarget).Choices);
        Assert.Equal(["reply", "refresh"], SettingsStore.Info(SettingsKey.CtrlR).Choices);
        Assert.Equal([SettingsKey.CtrlR], SettingsStore.Schema.Where(k => k.WindowsOnly).Select(k => k.Key));
        var backend = new InMemorySettingsBackend();
        var s = new SettingsStore(backend, null);
        s.ColorScheme = ColorScheme.Light;
        s.Density = Density.Compact;
        s.SearchScope = SearchScope.Account;
        s.AssistantTarget = AssistantTarget.Code;
        s.AssistantMenu = false;
        s.CtrlR = CtrlR.Refresh;
        s.WindowMaximized = true;
        Assert.True(backend.TryGetString("color-scheme", out var scheme) && scheme == "light");
        Assert.True(backend.TryGetString("message-list-density", out var density) && density == "compact");
        Assert.True(backend.TryGetString("search-scope", out var scope) && scope == "account");
        Assert.True(backend.TryGetString("assistant-target", out var target) && target == "code");
        Assert.True(backend.TryGetBoolean("assistant-menu", out var menu) && !menu);
        Assert.True(backend.TryGetString("ctrl-r", out var ctrlR) && ctrlR == "refresh");
        Assert.True(backend.TryGetBoolean("window-maximized", out var maximized) && maximized);
    }

    // The geometry keys have no range in the gschema: the window clamps.
    [Fact]
    public void GeometryIsStoredAsGiven()
    {
        var s = Scratch();
        var calls = 0;
        s.OnChange(SettingsKey.WindowWidth, () => calls++);
        s.WindowWidth = 1600;
        s.WindowHeight = 900;
        s.FolderPaneWidth = 300;
        s.MessageListWidth = 420;
        s.WindowWidth = 1600;
        Assert.Equal((1600, 900, 300, 420), (s.WindowWidth, s.WindowHeight, s.FolderPaneWidth, s.MessageListWidth));
        Assert.Equal(1, calls);
        s.WindowWidth = -5;
        Assert.Equal(-5, s.WindowWidth);
    }

    private static SettingsStore Scratch() => new(new InMemorySettingsBackend(), null);
}
