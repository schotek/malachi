// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first tests of Controllers/UpdateStatusController (the flow of
// ui/internal/updates/updates.go's package comment) against a fake daemon:
// the status with the client's locale, Check Now, the preference, the
// banner's dismissals, the security question asked once, an older daemon
// and a failure that keeps the status.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class UpdateStatusControllerTests
{
    private const string Dl = "https://github.com/schotek/malachi/releases/download/v1.0.0/Malachi-Mail-1.0.0-windows-x64-setup.exe";

    private static UpdateStatus Offer(bool security) => new()
    {
        Enabled = true,
        Channel = "windows-setup-x64",
        Current = "0.9.0",
        State = UpdateState.Available,
        Available = new UpdateInfo
        {
            Version = "1.0.0",
            PublishedAt = new DateTimeOffset(2026, 11, 1, 19, 0, 0, TimeSpan.Zero),
            Security = security,
            Notes = "Things.",
            InfoUrl = "https://malachimail.app/download.html",
            Url = Dl,
            Size = 1,
            Prompt = security,
        },
    };

    [Fact]
    public async Task RefreshReadsTheStatusInTheLocale()
    {
        await using var h = await Harness.StartAsync("cs");
        h.Status = Offer(security: false);
        var changes = 0;
        await h.Ui.RunAsync(() =>
        {
            h.C.Changed += (_, _) => changes++;
            Assert.Null(h.C.Status);
            h.C.Refresh();
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.C.Status is not null);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(1, changes);
            Assert.Equal("1.0.0", h.C.Status!.Available!.Version);
            Assert.Null(h.C.PendingPrompt); // not a security release
        });
        Assert.Equal("cs", JsonCoding.Decode<UpdateStatusParams>(h.Params(API.UpdateStatus.Name)).Locale);
    }

    [Fact]
    public async Task CheckNowSetEnabledAndDismiss()
    {
        await using var h = await Harness.StartAsync("en");
        h.Status = new UpdateStatus { Enabled = true, Current = "1.0.0", State = UpdateState.UpToDate };
        await h.Ui.RunAsync(h.C.Refresh);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.C.Status is not null);

        // Check Now: the button waits for the answer, which is the new status.
        h.Status = Offer(security: false);
        await h.Ui.RunAsync(() =>
        {
            h.C.CheckNow();
            Assert.True(h.C.Checking);
            h.C.CheckNow(); // one at a time
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => !h.C.Checking);
        Assert.Equal(1, h.Count(API.UpdateCheck.Name));
        Assert.Equal("1.0.0", await h.Ui.RunAsync(() => h.C.Status!.Available!.Version));

        // Later and Skip go to the daemon with the version.
        await h.Ui.RunAsync(() => h.C.Dismiss("1.0.0", UpdateDismissAction.Later));
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.Count(API.UpdateDismiss.Name) == 1);
        var dismiss = JsonCoding.Decode<UpdateDismissParams>(h.Params(API.UpdateDismiss.Name));
        Assert.Equal(("1.0.0", UpdateDismissAction.Later), (dismiss.Version, dismiss.Action.Value));

        // A notice's Close.
        await h.Ui.RunAsync(() => h.C.DismissNotice("n-1"));
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.Count(API.UpdateDismissNotice.Name) == 1);
        Assert.Equal("n-1", JsonCoding.Decode<UpdateDismissNoticeParams>(h.Params(API.UpdateDismissNotice.Name)).Id);

        // The switch: off is written, the same value again is not.
        h.Status = new UpdateStatus { Enabled = false, Current = "1.0.0", State = UpdateState.Disabled };
        await h.Ui.RunAsync(() => h.C.SetEnabled(false));
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.C.Status is { Enabled: false });
        Assert.False(JsonCoding.Decode<UpdateSetPrefsParams>(h.Params(API.UpdateSetPrefs.Name)).Enabled);
        await h.Ui.RunAsync(() =>
        {
            h.C.SetEnabled(false);
            // Disabled: Check Now does nothing.
            h.C.CheckNow();
            Assert.False(h.C.Checking);
        });
        await h.Ui.RunAsync(h.C.Refresh);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.Count(API.UpdateStatus.Name) == 2);
        Assert.Equal(1, h.Count(API.UpdateSetPrefs.Name));
        Assert.Equal(1, h.Count(API.UpdateCheck.Name));
    }

    [Fact]
    public async Task TheSecurityQuestionIsAskedOnce()
    {
        await using var h = await Harness.StartAsync("en");
        h.Status = Offer(security: true);
        await h.Ui.RunAsync(h.C.Refresh);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.C.Status is not null);
        await h.Ui.RunAsync(() =>
        {
            var p = h.C.PendingPrompt;
            Assert.NotNull(p);
            Assert.Equal(Dl, p.Url);
            Assert.True(h.C.BeginPrompt(p.Version));
            Assert.False(h.C.BeginPrompt(p.Version));
            // The daemon still says prompt: true, this process does not ask again.
            Assert.Null(h.C.PendingPrompt);
            h.C.PromptAnswered(p.Version);
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.Count(API.UpdateDismiss.Name) == 1);
        var dismiss = JsonCoding.Decode<UpdateDismissParams>(h.Params(API.UpdateDismiss.Name));
        Assert.Equal(("1.0.0", UpdateDismissAction.Prompted), (dismiss.Version, dismiss.Action.Value));
    }

    [Fact]
    public async Task OlderDaemonAndFailureKeepTheStatus()
    {
        await using var h = await Harness.StartAsync("en");
        h.Failure = BoardTriageDaemon.Error(ErrorCode.MethodNotFound, "no such method");
        var changed = false;
        await h.Ui.RunAsync(() =>
        {
            h.C.Changed += (_, _) => changed = true;
            h.C.Refresh();
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => changed);
        Assert.Null(await h.Ui.RunAsync(() => h.C.Status));

        h.Failure = null;
        h.Status = Offer(security: false);
        await h.Ui.RunAsync(h.C.Refresh);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.C.Status is not null);

        h.Failure = BoardTriageDaemon.Error(ErrorCode.Unavailable, "busy");
        changed = false;
        await h.Ui.RunAsync(h.C.Refresh);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => changed);
        Assert.Equal("1.0.0", await h.Ui.RunAsync(() => h.C.Status!.Available!.Version));
    }

    private sealed class Harness : IAsyncDisposable
    {
        private readonly Lock gate = new();
        private readonly Dictionary<string, (int Count, string Params)> seen = new(StringComparer.Ordinal);
        private UpdateStatus status = new();
        private RpcException? failure;

        private Harness()
        {
            foreach (var method in new[]
            {
                API.UpdateStatus.Name, API.UpdateCheck.Name, API.UpdateSetPrefs.Name, API.UpdateDismiss.Name, API.UpdateDismissNotice.Name,
            })
            {
                Fake.On(method, p =>
                {
                    lock (gate)
                    {
                        seen[method] = (seen.GetValueOrDefault(method).Count + 1, p);
                        return failure is { } f ? throw f : JsonCoding.EncodeToString(status);
                    }
                });
            }
        }

        public FakeDaemon Fake { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public UpdateStatusController C { get; private set; } = null!;

        public UpdateStatus Status
        {
            set
            {
                lock (gate)
                {
                    status = value;
                }
            }
        }

        public RpcException? Failure
        {
            set
            {
                lock (gate)
                {
                    failure = value;
                }
            }
        }

        public static async Task<Harness> StartAsync(string locale)
        {
            var h = new Harness();
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            h.C = await h.Ui.RunAsync(() => new UpdateStatusController(h.Client, () => locale, pending: h.Pending));
            return h;
        }

        public int Count(string method)
        {
            lock (gate)
            {
                return seen.GetValueOrDefault(method).Count;
            }
        }

        public string Params(string method)
        {
            lock (gate)
            {
                return seen[method].Params;
            }
        }

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(C.Dispose);
            try
            {
                await Quiescence.IdleAsync(Ui, Pending, Fake);
            }
            catch (Exception e) when (e is TimeoutException or AggregateException)
            {
                // A test's own failure, reported there.
            }
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
