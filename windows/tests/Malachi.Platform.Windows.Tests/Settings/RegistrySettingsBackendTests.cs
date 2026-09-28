// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of RegistrySettingsBackend (docs/windows-port.md §8, §12): the
// GSettings types in the registry, hand-edited values, and the watcher that
// makes a change from outside (another thread through the registry API, a
// second instance, reg.exe, a deleted key) reach the running app once, on
// its UI context, as gsettings set and defaults write do on GNOME and macOS.
// Also ui/internal/settings/store_test.go TestOpenFallsBack for the
// registry. Every test works under a key of its own,
// HKCU\Software\io.github.schotek.Malachi.Tests.<guid> (TestRegistryRoot),
// and deletes it.

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Settings;
using Malachi.Platform.Windows.Settings;
using Malachi.Platform.Windows.Tests.Startup;
using Microsoft.Win32;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Settings;

public sealed class RegistrySettingsBackendTests : IDisposable
{
    private static readonly TimeSpan Timeout = TimeSpan.FromSeconds(10);

    private readonly string path = TestRegistryRoot.NewPath();
    private readonly Stack<IDisposable> owned = new();
    private int sentinel;

    // The key goes whatever happens to the rest.
    public void Dispose()
    {
        try
        {
            while (owned.Count > 0)
            {
                owned.Pop().Dispose();
            }
        }
        finally
        {
            Registry.CurrentUser.DeleteSubKeyTree(path, throwOnMissingSubKey: false);
        }
    }

    [Fact]
    public void DefaultsWhenTheKeyIsEmpty()
    {
        var s = new SettingsStore(Backend(), null);
        Assert.True(s.Persistent);
        Assert.Equal(100, s.TextZoom);
        Assert.Equal(ColorScheme.System, s.ColorScheme);
        Assert.True(s.ShowAvatars);
        Assert.Empty(s.FavouriteFolders);
        Assert.Equal(1200, s.WindowWidth);
        // Defaults are never written.
        using var key = Registry.CurrentUser.OpenSubKey(path)!;
        Assert.Equal(0, key.ValueCount);
    }

    [Fact]
    public void WritesUseTheGSettingsTypes()
    {
        var s = new SettingsStore(Backend(), null);
        s.ShowAvatars = false;
        s.TextZoom = 150;
        s.MarkReadDelay = -3;
        s.ColorScheme = ColorScheme.Dark;
        s.CtrlR = CtrlR.Refresh;
        s.CollapsedFolders = ["acc_1/f_1", "acc_1/f_2"];
        s.FavouriteFolders = [];
        s.WindowWidth = 1400;

        using var key = Registry.CurrentUser.OpenSubKey(path)!;
        Assert.Equal(RegistryValueKind.DWord, key.GetValueKind("show-avatars"));
        Assert.Equal(0, key.GetValue("show-avatars"));
        Assert.Equal(RegistryValueKind.DWord, key.GetValueKind("text-zoom"));
        Assert.Equal(150, key.GetValue("text-zoom"));
        Assert.Equal(0, key.GetValue("mark-read-delay"));
        Assert.Equal(RegistryValueKind.String, key.GetValueKind("color-scheme"));
        Assert.Equal("dark", key.GetValue("color-scheme"));
        Assert.Equal("refresh", key.GetValue("ctrl-r"));
        Assert.Equal(RegistryValueKind.MultiString, key.GetValueKind("collapsed-folders"));
        Assert.Equal(["acc_1/f_1", "acc_1/f_2"], (string[])key.GetValue("collapsed-folders")!);
        Assert.Equal(1400, key.GetValue("window-width"));
        // An empty list equal to the default is not written.
        Assert.Null(key.GetValue("favourite-folders"));
        Assert.Equal(7, key.ValueCount);
    }

    // What a user or an administrator writes with regedit or reg add: out of
    // range, of another type, an unknown nick.
    [Fact]
    public void ReadsHandEditedValues()
    {
        using (var key = Registry.CurrentUser.CreateSubKey(path))
        {
            key.SetValue("text-zoom", 999, RegistryValueKind.DWord);
            key.SetValue("mark-read-delay", unchecked((int)0xFFFFFFFB), RegistryValueKind.DWord);
            key.SetValue("color-scheme", "neon", RegistryValueKind.String);
            key.SetValue("message-list-density", "compact", RegistryValueKind.String);
            key.SetValue("show-avatars", 0, RegistryValueKind.DWord);
            key.SetValue("confirm-delete", 2, RegistryValueKind.DWord);
            key.SetValue("notification-sound", "true", RegistryValueKind.String);
            key.SetValue("window-width", 1500L, RegistryValueKind.QWord);
            string[] folders = ["x", "", "y"];
            key.SetValue("collapsed-folders", folders, RegistryValueKind.MultiString);
            key.SetValue("favourite-folders", "a/b", RegistryValueKind.String);
            key.SetValue("ctrl-r", "refresh", RegistryValueKind.String);
            key.SetValue("Search-Scope", "all", RegistryValueKind.String);
        }
        var s = new SettingsStore(Backend(), null);
        Assert.Equal(SettingsStore.TextZoomMax, s.TextZoom);
        Assert.Equal(0, s.MarkReadDelay);
        Assert.Equal(ColorScheme.System, s.ColorScheme);
        Assert.Equal(Density.Compact, s.Density);
        Assert.False(s.ShowAvatars);
        Assert.True(s.ConfirmDelete);
        Assert.False(s.NotificationSound);
        Assert.Equal(1200, s.WindowWidth);
        Assert.Equal(["x", "", "y"], s.CollapsedFolders);
        Assert.Empty(s.FavouriteFolders);
        Assert.Equal(CtrlR.Refresh, s.CtrlR);
        Assert.Equal(SearchScope.All, s.SearchScope);
    }

    // A change written through the registry API on another thread (as
    // another process would) reaches the handler once, on the UI context.
    [Fact]
    public async Task ExternalChangeFromAnotherThreadReachesTheUIContext()
    {
        var ui = Owned(new TestUIContext());
        var backend = Backend();
        var s = await ui.RunAsync(() => new SettingsStore(backend));
        var calls = 0;
        var thread = new TaskCompletionSource<int>(TaskCreationOptions.RunContinuationsAsynchronously);
        s.OnChange(SettingsKey.TextZoom, () =>
        {
            Interlocked.Increment(ref calls);
            thread.TrySetResult(Environment.CurrentManagedThreadId);
        });

        await Task.Run(() =>
        {
            using var key = Registry.CurrentUser.OpenSubKey(path, writable: true)!;
            key.SetValue("text-zoom", 150, RegistryValueKind.DWord);
        }, TestContext.Current.CancellationToken);

        Assert.Equal(ui.ThreadId, await thread.Task.WaitAsync(Timeout, TestContext.Current.CancellationToken));
        Assert.Equal(150, s.TextZoom);
        await SettleAsync(ui, s);
        Assert.Equal(1, Volatile.Read(ref calls));
        Assert.Empty(ui.Failures);
    }

    // The watcher never reports what the backend wrote itself: the handler
    // fires once, synchronously, from the setter.
    [Fact]
    public async Task OwnWritesAreNotReportedBack()
    {
        var ui = Owned(new TestUIContext());
        var backend = Backend();
        var s = await ui.RunAsync(() => new SettingsStore(backend));
        var calls = 0;
        s.OnChange(SettingsKey.ColorScheme, () => Interlocked.Increment(ref calls));
        s.ColorScheme = ColorScheme.Light;
        s.ColorScheme = ColorScheme.Dark;
        Assert.Equal(2, Volatile.Read(ref calls));
        await SettleAsync(ui, s);
        Assert.Equal(2, Volatile.Read(ref calls));
        Assert.Equal(ColorScheme.Dark, s.ColorScheme);
    }

    // The scenario of docs/windows-port.md §8: reg add reaches the running app.
    [Fact]
    public async Task RegAddReachesTheRunningApp()
    {
        var ui = Owned(new TestUIContext());
        var backend = Backend();
        var s = await ui.RunAsync(() => new SettingsStore(backend));
        var changed = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        s.OnChange(SettingsKey.ColorScheme, () => changed.TrySetResult());

        var start = new ProcessStartInfo("reg.exe")
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        string[] args = ["add", @"HKCU\" + path, "/v", "color-scheme", "/t", "REG_SZ", "/d", "dark", "/f"];
        foreach (var arg in args)
        {
            start.ArgumentList.Add(arg);
        }
        using (var reg = Process.Start(start)!)
        {
            await reg.WaitForExitAsync(TestContext.Current.CancellationToken);
            Assert.Equal(0, reg.ExitCode);
        }

        await changed.Task.WaitAsync(Timeout, TestContext.Current.CancellationToken);
        Assert.Equal(ColorScheme.Dark, s.ColorScheme);
    }

    // reg delete of the whole key: it is created again and every value it
    // held reads as changed, back to its default.
    [Fact]
    public async Task DeletingTheKeyResetsEveryValue()
    {
        var ui = Owned(new TestUIContext());
        var backend = Backend();
        var s = await ui.RunAsync(() => new SettingsStore(backend));
        s.TextZoom = 150;
        s.CollapsedFolders = ["a"];
        var zoom = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var folders = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        s.OnChange(SettingsKey.TextZoom, () => zoom.TrySetResult());
        s.OnChange(SettingsKey.CollapsedFolders, () => folders.TrySetResult());

        await Task.Run(() => Registry.CurrentUser.DeleteSubKeyTree(path), TestContext.Current.CancellationToken);

        await Task.WhenAll(zoom.Task, folders.Task).WaitAsync(Timeout, TestContext.Current.CancellationToken);
        Assert.Equal(100, s.TextZoom);
        Assert.Empty(s.CollapsedFolders);
        // The app keeps working on the key it created again.
        s.TextZoom = 120;
        using var key = Registry.CurrentUser.OpenSubKey(path)!;
        Assert.Equal(120, key.GetValue("text-zoom"));
    }

    // Two instances over one key see each other's writes, as two GSettings
    // objects of one schema do.
    [Fact]
    public async Task ASecondInstanceSeesTheChange()
    {
        var ui = Owned(new TestUIContext());
        var first = Backend();
        var second = Backend();
        var watching = await ui.RunAsync(() => new SettingsStore(first));
        var writing = new SettingsStore(second, null);
        var changed = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        watching.OnChange(SettingsKey.FavouriteFolders, () => changed.TrySetResult());

        writing.FavouriteFolders = ["acc_1/f_9"];

        await changed.Task.WaitAsync(Timeout, TestContext.Current.CancellationToken);
        Assert.Equal(["acc_1/f_9"], watching.FavouriteFolders);
    }

    [Fact]
    public async Task ValueNamesIgnoreCase()
    {
        var ui = Owned(new TestUIContext());
        var backend = Backend();
        var s = await ui.RunAsync(() => new SettingsStore(backend));
        var changed = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        s.OnChange(SettingsKey.MarkReadDelay, () => changed.TrySetResult());
        await Task.Run(() =>
        {
            using var key = Registry.CurrentUser.OpenSubKey(path, writable: true)!;
            key.SetValue("Mark-Read-Delay", 9, RegistryValueKind.DWord);
        }, TestContext.Current.CancellationToken);
        await changed.Task.WaitAsync(Timeout, TestContext.Current.CancellationToken);
        Assert.Equal(9, s.MarkReadDelay);
        // A write through the store updates that value instead of adding one.
        s.MarkReadDelay = 4;
        using var check = Registry.CurrentUser.OpenSubKey(path)!;
        Assert.Equal(4, check.GetValue("mark-read-delay"));
        Assert.Equal(1, check.ValueCount);
    }

    [Fact]
    public void ValuesPersistAcrossInstances()
    {
        using (var backend = new RegistrySettingsBackend(path))
        {
            var s = new SettingsStore(backend, null);
            s.TextZoom = 140;
            s.FavouriteFolders = ["a/b"];
        }
        var again = new SettingsStore(Backend(), null);
        Assert.Equal(140, again.TextZoom);
        Assert.Equal(["a/b"], again.FavouriteFolders);
    }

    // Dispose stops the watcher and closes the key; the backend then reads
    // as empty and writes nothing, and a second Dispose does nothing.
    [Fact]
    public void DisposeStopsTheWatcher()
    {
        var backend = new RegistrySettingsBackend(path);
        var s = new SettingsStore(backend, null);
        s.TextZoom = 130;
        var watch = Stopwatch.StartNew();
        backend.Dispose();
        Assert.True(watch.Elapsed < Timeout);
        backend.Dispose();
        Assert.Equal(100, s.TextZoom);
        s.ShowAvatars = false;
        using var key = Registry.CurrentUser.OpenSubKey(path)!;
        Assert.Equal(130, key.GetValue("text-zoom"));
        Assert.Null(key.GetValue("show-avatars"));
    }

    // ui/internal/settings TestOpenFallsBack: a key that cannot be opened
    // gives an in-memory store instead of an exception.
    [Fact]
    public void OpenFallsBack()
    {
        using var fallback = RegistrySettingsBackend.Open(path + @"\" + new string('x', 300));
        Assert.IsType<InMemorySettingsBackend>(fallback);
        var s = new SettingsStore(fallback, null);
        Assert.False(s.Persistent);
        Assert.Equal(100, s.TextZoom);

        using var real = RegistrySettingsBackend.Open(path);
        Assert.IsType<RegistrySettingsBackend>(real);
        Assert.True(real.IsPersistent);
    }

    [Fact]
    public void DefaultPathIsTheAppId()
    {
        Assert.Equal(@"Software\io.github.schotek.Malachi", RegistrySettingsBackend.DefaultPath);
    }

    private RegistrySettingsBackend Backend() => Owned(new RegistrySettingsBackend(path));

    private T Owned<T>(T disposable)
        where T : IDisposable
    {
        owned.Push(disposable);
        return disposable;
    }

    // Waits until the watcher has processed everything written so far: an
    // outside change of another key is reported after anything before it,
    // and its handler runs on the UI context after anything posted before.
    private async Task SettleAsync(TestUIContext ui, SettingsStore store)
    {
        var seen = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var token = store.OnChange(SettingsKey.WindowHeight, () => seen.TrySetResult());
        var height = 500 + Interlocked.Increment(ref sentinel);
        await Task.Run(() =>
        {
            using var key = Registry.CurrentUser.OpenSubKey(path, writable: true)!;
            key.SetValue("window-height", height, RegistryValueKind.DWord);
        }, TestContext.Current.CancellationToken);
        await seen.Task.WaitAsync(Timeout, TestContext.Current.CancellationToken);
        token.Cancel();
        await ui.DrainAsync();
    }
}
