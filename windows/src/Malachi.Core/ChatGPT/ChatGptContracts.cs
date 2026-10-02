// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC client contracts; no existing Swift/Go counterpart.
// docs/chatgpt-integration.md §4. These credentials never reach the daemon.

using System;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.ChatGPT;

public enum ChatGptConnectionStatus { Disconnected, SigningIn, Connected, ReconnectRequired }
public enum ChatGptAuthError { NotConnected, InvalidResponse, IdentityMismatch, PermissionDenied, ReconnectRequired, Network, Storage, Browser }

public sealed class ChatGptAuthException(ChatGptAuthError error) : Exception("ChatGPT authentication failed: " + error)
{
    public ChatGptAuthError Error { get; } = error;
}

/// <summary>Safe presentation data; contains no credential or authorization URL.</summary>
public sealed record ChatGptConnection(ChatGptConnectionStatus Status, string? Email = null, string? ConnectionId = null,
    string? ClientId = null, string? Subject = null);

public interface IChatGptAccessTokenSource
{
    CancellationToken SessionCancellation { get; }
    Task<string> GetAccessTokenAsync(CancellationToken cancellationToken = default);
}

/// <summary>Only inference adapters report credential rejection; views never receive the token.</summary>
public interface IChatGptInferenceObserver
{
    Task InferenceRejectedAsync(string rejectedAccessToken, int httpStatusCode, CancellationToken cancellationToken = default);
}

public sealed record ChatGptRegistration(string HostId, string? ConnectionId = null, string? ClientId = null,
    string? Subject = null, string? Email = null);

/// <summary>Only the connection coordinator and OS credential store consume this type.</summary>
public sealed record ChatGptTokens(string ConnectionId, string AccessToken, string RefreshToken, string IdToken,
    string Scope, DateTimeOffset ExpiresAt)
{
    public override string ToString() => nameof(ChatGptTokens);
}

/// <summary>All operations occur under a lease shared by every app instance using this store.</summary>
public interface IChatGptCredentialStore
{
    Task<IAsyncDisposable> AcquireAsync(CancellationToken cancellationToken);
    Task<ChatGptRegistration?> ReadRegistrationAsync(CancellationToken cancellationToken);
    Task WriteRegistrationAsync(ChatGptRegistration registration, CancellationToken cancellationToken);
    Task<ChatGptTokens?> ReadTokensAsync(CancellationToken cancellationToken);
    Task WriteTokensAsync(ChatGptTokens tokens, CancellationToken cancellationToken);
    Task DeleteTokensAsync(CancellationToken cancellationToken);
}

/// <summary>The implementation binds a loopback listener before opening the system browser.</summary>
public interface IChatGptBrowser
{
    Task<Uri> AuthorizeAsync(Func<Uri, Uri> authorizationUrl, string state, CancellationToken cancellationToken);
}
