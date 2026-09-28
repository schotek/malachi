// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only, no Swift counterpart: the lock helper runs take turns with
// (CredentialLock). Every test uses a lock of its own name, never the
// session's.

using System;
using System.Threading;
using Xunit;

namespace Malachi.Credentials.Tests;

public sealed class CredentialLockTests
{
    [Fact]
    public void TheSessionLockIsTheUsersAndShorterThanTheDaemonsPatience()
    {
        Assert.Equal(@"Local\io.github.schotek.Malachi.credentials", CredentialLock.Session.Name);
        // backend/internal/auth/helper CallTimeout is 30 s.
        Assert.True(CredentialLock.Session.Timeout < TimeSpan.FromSeconds(30));
    }

    [Fact]
    public void OneRunHoldsItAtATime()
    {
        var turns = Unique(TimeSpan.FromMilliseconds(50));
        using (Holder.Hold(turns))
        {
            Assert.Null(turns.Take());
        }
        using var turn = turns.Take();
        Assert.NotNull(turn);
    }

    [Fact]
    public void ItIsHeldAgainByTheRunThatHoldsIt()
    {
        // A mutex counts its owner's waits, so a nested take cannot deadlock.
        var turns = Unique(TimeSpan.FromMilliseconds(50));
        using var outer = turns.Take();
        using var inner = turns.Take();
        Assert.NotNull(outer);
        Assert.NotNull(inner);
    }

    [Fact]
    public void ALockItsHolderDiedWithIsTaken()
    {
        var turns = Unique(TimeSpan.FromSeconds(10));
        CredentialLock.Turn? abandoned = null;
        var died = new Thread(() => abandoned = turns.Take());
        died.Start();
        died.Join();
        Assert.NotNull(abandoned);

        using var turn = turns.Take();
        Assert.NotNull(turn);
    }

    /// <summary>A lock no other test and no helper uses.</summary>
    internal static CredentialLock Unique(TimeSpan timeout) =>
        new($@"Local\io.github.schotek.Malachi.credentials.test-{Guid.NewGuid():N}", timeout);

    /// <summary>Holds a lock on a thread of its own until disposed.</summary>
    internal sealed class Holder : IDisposable
    {
        private readonly ManualResetEventSlim release = new();
        private readonly Thread thread;

        private Holder(CredentialLock turns)
        {
            var held = false;
            using var taken = new ManualResetEventSlim();
            thread = new Thread(() =>
            {
                using var turn = turns.Take();
                held = turn is not null;
                taken.Set();
                release.Wait();
            });
            thread.Start();
            taken.Wait();
            if (!held)
            {
                release.Set();
                thread.Join();
                Assert.Fail("the holder did not get the lock");
            }
        }

        public static Holder Hold(CredentialLock turns) => new(turns);

        public void Dispose()
        {
            release.Set();
            thread.Join();
            release.Dispose();
        }
    }
}
