// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC storage, no Swift/Go counterpart. Credential Manager
// uses generation chunks and an atomic head item; metadata is separate.
// Directory policy follows Files/PrivateDirectory and Transport/WindowsKeyFilePolicy.

using System;
using System.ComponentModel;
using System.Globalization;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.ChatGPT;
using Malachi.Platform.Windows.Files;
using Malachi.Platform.Windows.Transport;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Storage.FileSystem;

namespace Malachi.Platform.Windows.ChatGPT;

public sealed class WindowsChatGptCredentialStore : IChatGptCredentialStore
{
    private const int ChunkSize = 2048;
    private const int MaxChunks = 128;
    private readonly string directory;
    private readonly string prefix;
    private readonly WindowsKeyFilePolicy policy = new();

    public WindowsChatGptCredentialStore(string directory)
    {
        ArgumentException.ThrowIfNullOrEmpty(directory);
        if (!Path.IsPathFullyQualified(directory) || directory.StartsWith(@"\\", StringComparison.Ordinal)
            || new DriveInfo(Path.GetPathRoot(directory)!).DriveType == DriveType.Network)
            throw new ArgumentException("ChatGPT storage requires a local absolute directory", nameof(directory));
        this.directory = Path.TrimEndingDirectorySeparator(Path.GetFullPath(directory));
        prefix = "io.github.schotek.Malachi/ChatGPT/" + Convert.ToHexString(SHA256.HashData(
            Encoding.UTF8.GetBytes(this.directory.ToUpperInvariant()))) + "/";
    }

    public async Task<IAsyncDisposable> AcquireAsync(CancellationToken cancellationToken)
    {
        PrepareDirectory();
        var path = Path.Combine(directory, "session.lock");
        while (true)
        {
            cancellationToken.ThrowIfCancellationRequested();
            var handle = PInvoke.CreateFile(path, 0x80000000 | 0x40000000, 0, null,
                FILE_CREATION_DISPOSITION.OPEN_ALWAYS, FILE_FLAGS_AND_ATTRIBUTES.FILE_FLAG_OPEN_REPARSE_POINT, null);
            if (!handle.IsInvalid)
            {
                try
                {
                    if ((File.GetAttributes(handle) & (FileAttributes.ReparsePoint | FileAttributes.Directory)) != 0
                        || policy.Check(handle, path) is not null)
                        throw new UnauthorizedAccessException("ChatGPT lock is not private");
                    return new FileStream(handle, FileAccess.ReadWrite);
                }
                catch { handle.Dispose(); throw; }
            }
            var error = (WIN32_ERROR)Marshal.GetLastPInvokeError();
            handle.Dispose();
            if (error != WIN32_ERROR.ERROR_SHARING_VIOLATION) throw new Win32Exception((int)error);
            await Task.Delay(75, cancellationToken).ConfigureAwait(false);
        }
    }

    public Task<ChatGptRegistration?> ReadRegistrationAsync(CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var path = Path.Combine(directory, "registration.json");
        if (!File.Exists(path)) return Task.FromResult<ChatGptRegistration?>(null);
        using var handle = policy.Open(path);
        if ((File.GetAttributes(handle) & (FileAttributes.ReparsePoint | FileAttributes.Directory)) != 0
            || policy.Check(handle, path) is not null) throw new UnauthorizedAccessException("ChatGPT metadata is not private");
        using var stream = new FileStream(handle, FileAccess.Read);
        if (stream.Length > 16 * 1024) throw new IOException("ChatGPT metadata is too large");
        var registration = JsonSerializer.Deserialize(stream, ChatGptStorageJson.Default.ChatGptRegistration)
            ?? throw new IOException("ChatGPT metadata is invalid");
        if (string.IsNullOrWhiteSpace(registration.HostId) || registration.ClientId == ChatGptOAuth.DynamicClient)
            throw new IOException("ChatGPT metadata is invalid");
        return Task.FromResult<ChatGptRegistration?>(registration);
    }

    public async Task WriteRegistrationAsync(ChatGptRegistration registration, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(registration);
        cancellationToken.ThrowIfCancellationRequested();
        PrepareDirectory();
        var temporary = Path.Combine(directory, Guid.NewGuid().ToString("N") + ".tmp");
        try
        {
            await using (var stream = new FileStream(temporary, FileMode.CreateNew, FileAccess.Write, FileShare.None))
            {
                await JsonSerializer.SerializeAsync(stream, registration, ChatGptStorageJson.Default.ChatGptRegistration, cancellationToken).ConfigureAwait(false);
                stream.Flush(true);
            }
            cancellationToken.ThrowIfCancellationRequested();
            File.Move(temporary, Path.Combine(directory, "registration.json"), true);
        }
        finally { if (File.Exists(temporary)) File.Delete(temporary); }
    }

    public Task<ChatGptTokens?> ReadTokensAsync(CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var head = ReadHead();
        if (head is null) return Task.FromResult<ChatGptTokens?>(null);
        using var buffer = new MemoryStream();
        try
        {
            for (var index = 0; index < head.Count; index++)
            {
                var chunk = ChatGptNativeCredentials.Read(ChunkName(head.Id, index)) ?? throw new IOException("ChatGPT credentials are incomplete");
                try { buffer.Write(chunk); }
                finally { CryptographicOperations.ZeroMemory(chunk); }
            }
            var bytes = buffer.GetBuffer().AsSpan(0, checked((int)buffer.Length));
            if (!CryptographicOperations.FixedTimeEquals(SHA256.HashData(bytes), Convert.FromHexString(head.Hash)))
                throw new IOException("ChatGPT credential integrity check failed");
            return Task.FromResult(JsonSerializer.Deserialize(bytes, ChatGptStorageJson.Default.ChatGptTokens));
        }
        finally { CryptographicOperations.ZeroMemory(buffer.GetBuffer()); }
    }

    public Task WriteTokensAsync(ChatGptTokens tokens, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(tokens);
        cancellationToken.ThrowIfCancellationRequested();
        var bytes = JsonSerializer.SerializeToUtf8Bytes(tokens, ChatGptStorageJson.Default.ChatGptTokens);
        var generation = Guid.NewGuid().ToString("N");
        var committed = false;
        try
        {
            if (bytes.Length > ChunkSize * MaxChunks) throw new IOException("ChatGPT credentials are too large");
            var count = (bytes.Length + ChunkSize - 1) / ChunkSize;
            for (var index = 0; index < count; index++)
            {
                cancellationToken.ThrowIfCancellationRequested();
                ChatGptNativeCredentials.Write(ChunkName(generation, index), bytes.AsSpan(index * ChunkSize, Math.Min(ChunkSize, bytes.Length - index * ChunkSize)));
            }
            var head = new CredentialGeneration(generation, count, Convert.ToHexString(SHA256.HashData(bytes)));
            cancellationToken.ThrowIfCancellationRequested();
            ChatGptNativeCredentials.Write(prefix + "head", JsonSerializer.SerializeToUtf8Bytes(head, ChatGptStorageJson.Default.CredentialGeneration));
            committed = true;
            // Any interrupted earlier generation is collected too. Failure to
            // collect never rolls back the new rotating refresh token.
            CleanupExcept(generation);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(bytes);
            if (!committed) CleanupGeneration(generation);
        }
        return Task.CompletedTask;
    }

    public Task DeleteTokensAsync(CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        // Remove the head first, so a partial cleanup cannot leave a usable
        // local session. Include abandoned generations from interrupted writes.
        ChatGptNativeCredentials.Delete(prefix + "head");
        foreach (var name in ChatGptNativeCredentials.List(prefix)) ChatGptNativeCredentials.Delete(name);
        return Task.CompletedTask;
    }

    private CredentialGeneration? ReadHead()
    {
        var bytes = ChatGptNativeCredentials.Read(prefix + "head");
        if (bytes is null) return null;
        var head = JsonSerializer.Deserialize(bytes, ChatGptStorageJson.Default.CredentialGeneration)
            ?? throw new IOException("ChatGPT credential pointer is invalid");
        if (!Guid.TryParseExact(head.Id, "N", out _) || head.Count is < 1 or > MaxChunks || head.Hash.Length != 64)
            throw new IOException("ChatGPT credential pointer is invalid");
        return head;
    }

    private string ChunkName(string generation, int index) => prefix + generation + "/" + index.ToString(CultureInfo.InvariantCulture);

    private void CleanupExcept(string generation)
    {
        try
        {
            foreach (var name in ChatGptNativeCredentials.List(prefix))
                if (name != prefix + "head" && !name.StartsWith(prefix + generation + "/", StringComparison.Ordinal))
                    ChatGptNativeCredentials.Delete(name);
        }
        catch (Win32Exception) { /* The committed head is authoritative; next write/sign-out retries cleanup. */ }
    }

    private void CleanupGeneration(string generation)
    {
        try { foreach (var name in ChatGptNativeCredentials.List(prefix + generation + "/")) ChatGptNativeCredentials.Delete(name); }
        catch (Win32Exception) { /* No head references this generation; sign-out retries cleanup. */ }
    }

    private void PrepareDirectory()
    {
        for (var parent = directory; parent is not null; parent = Path.GetDirectoryName(parent))
            if (Directory.Exists(parent) && (File.GetAttributes(parent) & FileAttributes.ReparsePoint) != 0)
                throw new UnauthorizedAccessException("ChatGPT metadata path contains a link");
        var directories = new PrivateDirectory();
        if (Directory.Exists(directory))
        {
            if (!directories.IsPrivate(directory)) throw new UnauthorizedAccessException("ChatGPT metadata directory is not private");
        }
        else directories.Ensure(directory);
    }
}
