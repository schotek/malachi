// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AssistantProfileTests.swift
// (ProfileStoreTests) and of ui/internal/assistantpanel/
// profile_store_test.go (TestProfileStore, TestProfileStoreOlderDaemon):
// the application's profile against a fake daemon: a read with the account
// names, a write, a resolve, a refused write, an older daemon and a
// transient failure.

using System;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class ProfileStoreTests
{
    [Fact]
    public async Task ReadsWritesAndResolves()
    {
        await using var h = await Harness.StartAsync();
        h.Get = new AssistantProfileGetResult
        {
            Profile = new AssistantProfile
            {
                Text = "Píšu stručně.",
                Gender = ProfileGender.Masculine,
                Accounts = [new ProfileAccountNote("acc_1", "Studio.")],
            },
            Proposals = [new ProfileProposal { Id = "p_1", Text = "Schůzky v pondělí.", Source = ProfileSource.Panel }],
            Limits = new ProfileLimits(),
        };
        var changes = 0;
        await h.Ui.RunAsync(() =>
        {
            h.S.Changed += (_, _) => changes++;
            Assert.False(h.S.Loaded);
            Assert.False(h.S.Supported);
            Assert.True(h.S.Profile.IsEmpty);
            h.S.Refresh();
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.S.Loaded);
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.S.Supported);
            Assert.Equal(1, changes);
            var p = h.S.Profile;
            Assert.Equal("Píšu stručně.", p.Text);
            var note = Assert.Single(p.Accounts);
            Assert.Equal(("Studio", "s@example.org"), (note.Name, note.Email));
            Assert.Equal("Schůzky v pondělí.", h.S.Proposal("p_1")?.Text);
            Assert.Null(h.S.Proposal("p_x"));
        });

        var error = await h.Ui.InvokeAsync(() => h.S.SetAsync(new AssistantProfileSetParams { Text = "Píšu velmi stručně." }));
        Assert.Null(error);
        Assert.Equal("Píšu velmi stručně.", await h.Ui.RunAsync(() => h.S.Stored.Text));
        Assert.Single(await h.Ui.RunAsync(() => h.S.Proposals));

        error = await h.Ui.InvokeAsync(() => h.S.ResolveAsync("p_1", accept: true));
        Assert.Null(error);
        await h.Ui.RunAsync(() =>
        {
            Assert.Empty(h.S.Proposals);
            Assert.Equal("Píšu velmi stručně.\nSchůzky v pondělí.", h.S.Stored.Text);
        });

        // A refused write keeps what the store has.
        h.SetFailure = BoardTriageDaemon.Error(ErrorCode.InvalidArgument, "too long");
        error = await h.Ui.InvokeAsync(() => h.S.SetAsync(new AssistantProfileSetParams { Text = "x" }));
        Assert.Equal(ErrorCode.InvalidArgument, error?.Code.Value);
        Assert.NotEqual("x", await h.Ui.RunAsync(() => h.S.Stored.Text));
    }

    [Fact]
    public async Task OlderDaemonAndTransientFailure()
    {
        await using var h = await Harness.StartAsync();
        h.GetFailure = BoardTriageDaemon.Error(ErrorCode.MethodNotFound, "no such method");
        await h.Ui.RunAsync(h.S.Refresh);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.S.Loaded);
        await h.Ui.RunAsync(() =>
        {
            Assert.False(h.S.Supported);
            Assert.True(h.S.Profile.IsEmpty);
            Assert.Equal(new ProfileLimits(), h.S.Limits);
            Assert.True(h.S.Stored.ShareWithMcp);
        });

        h.GetFailure = null;
        h.Get = new AssistantProfileGetResult { Profile = new AssistantProfile { Text = "T" } };
        await h.Ui.RunAsync(h.S.Refresh);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.S.Supported);

        // A transient failure keeps what was read before.
        h.GetFailure = BoardTriageDaemon.Error(ErrorCode.Unavailable, "busy");
        var changed = false;
        await h.Ui.RunAsync(() =>
        {
            h.S.Changed += (_, _) => changed = true;
            h.S.Refresh();
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => changed);
        await h.Ui.RunAsync(() =>
        {
            Assert.True(h.S.Supported);
            Assert.Equal("T", h.S.Stored.Text);
            Assert.Equal(new ProfileLimits(), h.S.Limits);
        });
    }

    private sealed class Harness : IAsyncDisposable
    {
        private readonly Lock gate = new();
        private AssistantProfileGetResult get = new();
        private RpcException? getFailure;
        private RpcException? setFailure;

        private Harness()
        {
            Fake.On(API.AssistantProfileGet.Name, _ =>
            {
                lock (gate)
                {
                    return getFailure is { } f ? throw f : JsonCoding.EncodeToString(get);
                }
            });
            Fake.On(API.AccountList.Name, _ => JsonCoding.EncodeToString(new AccountListResult
            {
                Accounts =
                [
                    new Account
                    {
                        Id = "acc_1",
                        Config = new AccountConfig { Name = "Studio", Email = "s@example.org" },
                        Enabled = true,
                        State = new SyncState { AccountId = "acc_1", Status = SyncStatus.Idle },
                    },
                ],
            }));
            Fake.On(API.AssistantProfileSet.Name, p =>
            {
                var q = JsonCoding.Decode<AssistantProfileSetParams>(p);
                lock (gate)
                {
                    if (setFailure is { } f)
                    {
                        throw f;
                    }
                    if (q.Text is { } text)
                    {
                        get = get with { Profile = get.Profile with { Text = text } };
                    }
                    return JsonCoding.EncodeToString(new AssistantProfileSetResult { Profile = get.Profile });
                }
            });
            Fake.On(API.AssistantProfileResolve.Name, p =>
            {
                var q = JsonCoding.Decode<AssistantProfileResolveParams>(p);
                lock (gate)
                {
                    var profile = q.Accept ? get.Profile with { Text = get.Profile.Text + "\nSchůzky v pondělí." } : get.Profile;
                    get = get with { Profile = profile, Proposals = [] };
                    return JsonCoding.EncodeToString(new AssistantProfileResolveResult { Profile = profile, Proposals = [] });
                }
            });
        }

        public FakeDaemon Fake { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public ProfileStore S { get; private set; } = null!;

        public AssistantProfileGetResult Get
        {
            set
            {
                lock (gate)
                {
                    get = value;
                }
            }
        }

        public RpcException? GetFailure
        {
            set
            {
                lock (gate)
                {
                    getFailure = value;
                }
            }
        }

        public RpcException? SetFailure
        {
            set
            {
                lock (gate)
                {
                    setFailure = value;
                }
            }
        }

        public static async Task<Harness> StartAsync()
        {
            var h = new Harness();
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            h.S = await h.Ui.RunAsync(() => new ProfileStore(h.Client, pending: h.Pending));
            return h;
        }

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(S.Dispose);
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
