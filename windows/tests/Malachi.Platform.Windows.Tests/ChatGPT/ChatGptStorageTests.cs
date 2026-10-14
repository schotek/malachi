// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC storage regression tests; no Swift/Go counterpart.
// Uses only uniquely named test credentials, deleted in finally.

using System;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.ChatGPT;
using Malachi.Platform.Windows.ChatGPT;
using Xunit;

namespace Malachi.Platform.Windows.Tests.ChatGPT;

public sealed class ChatGptStorageTests
{
    [Fact]
    public async Task LargeTokensRotateAtomicallyAndNeverEnterMetadataFiles()
    {
        Assert.SkipUnless(OperatingSystem.IsWindows(), "requires Windows Credential Manager");
        var directory = Path.Combine(Path.GetTempPath(), "malachi-chatgpt-test-" + Guid.NewGuid().ToString("N"));
        var store = new WindowsChatGptCredentialStore(directory);
        try
        {
            await using var lease = await store.AcquireAsync(CancellationToken.None);
            var registration = new ChatGptRegistration("urn:uuid:" + Guid.NewGuid(), "connection", "client", "subject", "test@example.test");
            await store.WriteRegistrationAsync(registration, CancellationToken.None);
            var tokens = new ChatGptTokens("connection", "secret-access-" + new string('a', 9000),
                "secret-refresh-" + new string('r', 9000), "secret-id-" + new string('i', 9000), ChatGptOAuth.Scope, DateTimeOffset.UtcNow.AddHours(1));
            await store.WriteTokensAsync(tokens, CancellationToken.None);
            Assert.Equal(tokens, await store.ReadTokensAsync(CancellationToken.None));
            var rotated = tokens with { AccessToken = "rotated-access", RefreshToken = "rotated-refresh" };
            await store.WriteTokensAsync(rotated, CancellationToken.None);
            Assert.Equal(rotated, await store.ReadTokensAsync(CancellationToken.None));
            var metadata = await File.ReadAllTextAsync(Path.Combine(directory, "registration.json"), TestContext.Current.CancellationToken);
            Assert.DoesNotContain("secret-access", metadata, StringComparison.Ordinal);
            Assert.DoesNotContain("secret-refresh", metadata, StringComparison.Ordinal);
            Assert.DoesNotContain("rotated", metadata, StringComparison.Ordinal);
            await store.DeleteTokensAsync(CancellationToken.None);
            Assert.Null(await store.ReadTokensAsync(CancellationToken.None));
            Assert.Equal(registration, await store.ReadRegistrationAsync(CancellationToken.None));
        }
        finally
        {
            await store.DeleteTokensAsync(CancellationToken.None);
            if (Directory.Exists(directory)) Directory.Delete(directory, true);
        }
    }

    [Fact]
    public async Task StoreLeaseSerializesDifferentInstancesAndCanBeCanceled()
    {
        Assert.SkipUnless(OperatingSystem.IsWindows(), "requires Windows file DACLs");
        var directory = Path.Combine(Path.GetTempPath(), "malachi-chatgpt-lock-" + Guid.NewGuid().ToString("N"));
        try
        {
            var first = new WindowsChatGptCredentialStore(directory);
            var second = new WindowsChatGptCredentialStore(directory);
            await using (var lease = await first.AcquireAsync(CancellationToken.None))
            {
                using var cancel = new CancellationTokenSource();
                var waiting = second.AcquireAsync(cancel.Token);
                Assert.False(waiting.IsCompleted);
                cancel.Cancel();
                await Assert.ThrowsAnyAsync<OperationCanceledException>(() => waiting);
            }
            await using var next = await second.AcquireAsync(CancellationToken.None);
        }
        finally { if (Directory.Exists(directory)) Directory.Delete(directory, true); }
    }

    [Fact]
    public async Task ExistingPublicDirectoryIsRefused()
    {
        Assert.SkipUnless(OperatingSystem.IsWindows(), "requires Windows file DACLs");
        var directory = Path.Combine(Path.GetTempPath(), "malachi-chatgpt-public-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(directory);
        try
        {
            var store = new WindowsChatGptCredentialStore(directory);
            await Assert.ThrowsAsync<UnauthorizedAccessException>(() => store.AcquireAsync(CancellationToken.None));
        }
        finally { Directory.Delete(directory, true); }
    }
}
