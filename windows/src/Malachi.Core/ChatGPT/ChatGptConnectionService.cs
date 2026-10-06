// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC coordinator, no existing Swift/Go counterpart.
// docs/chatgpt-integration.md §4. One renewable grant, serialized across processes.

using System;
using System.Collections.Generic;
using System.IO;
using System.Net.Http;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.IdentityModel.Tokens;

namespace Malachi.Core.ChatGPT;

public sealed class ChatGptConnectionService : IChatGptAccessTokenSource, IChatGptInferenceObserver, IDisposable
{
    private readonly IChatGptCredentialStore store;
    private readonly IChatGptBrowser browser;
    private readonly ChatGptIdentityProtocol protocol;
    private readonly TimeProvider time;
    private readonly SemaphoreSlim gate = new(1, 1);
    private readonly CancellationTokenSource lifetime = new();
    private CancellationTokenSource session = new();
    private CancellationTokenSource? pending;
    private bool disposed;

    public ChatGptConnectionService(HttpClient http, IChatGptCredentialStore store, IChatGptBrowser browser, TimeProvider? time = null)
    {
        ArgumentNullException.ThrowIfNull(http);
        this.store = store ?? throw new ArgumentNullException(nameof(store));
        this.browser = browser ?? throw new ArgumentNullException(nameof(browser));
        this.time = time ?? TimeProvider.System;
        protocol = new ChatGptIdentityProtocol(http, this.time);
    }

    public ChatGptConnection Connection { get; private set; } = new(ChatGptConnectionStatus.Disconnected);
    public event Action? Changed;
    public CancellationToken SessionCancellation => session.Token;

    public async Task InitializeAsync(CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(disposed, this);
        await gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await using var lease = await store.AcquireAsync(cancellationToken).ConfigureAwait(false);
            var registration = await RegistrationAsync(cancellationToken).ConfigureAwait(false);
            var tokens = await store.ReadTokensAsync(cancellationToken).ConfigureAwait(false);
            Set(registration, tokens is not null && tokens.ConnectionId == registration.ConnectionId && ChatGptOAuth.HasPlanScope(tokens.Scope)
                ? ChatGptConnectionStatus.Connected : ChatGptConnectionStatus.Disconnected);
        }
        catch (Exception e) { throw Sanitize(e); }
        finally { gate.Release(); }
    }

    public async Task SignInAsync(CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(disposed, this);
        await gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        var before = Connection;
        using var attempt = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken, lifetime.Token);
        attempt.CancelAfter(TimeSpan.FromMinutes(5));
        pending = attempt;
        var ct = attempt.Token;
        session.Cancel();
        try
        {
            await using var lease = await store.AcquireAsync(ct).ConfigureAwait(false);
            var registration = await RegistrationAsync(ct).ConfigureAwait(false);
            Set(registration, ChatGptConnectionStatus.SigningIn);
            var state = ChatGptOAuth.RandomValue();
            var nonce = ChatGptOAuth.RandomValue();
            var verifier = ChatGptOAuth.RandomValue();
            Uri? redirect = null;
            var callback = await browser.AuthorizeAsync(uri =>
            {
                redirect = uri;
                return ChatGptOAuth.AuthorizationUri(registration, uri, state, nonce, verifier);
            }, state, ct).ConfigureAwait(false);
            var fields = ChatGptOAuth.ParseCallback(callback,
                redirect ?? throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse), state);
            if (fields.ContainsKey("error")) throw new ChatGptAuthException(ChatGptAuthError.PermissionDenied);
            fields.TryGetValue("client_id", out var suppliedClient);
            var clientId = registration.ClientId ?? suppliedClient;
            if (string.IsNullOrWhiteSpace(clientId) || clientId == ChatGptOAuth.DynamicClient || clientId.Length > 512
                || (registration.ClientId is not null && suppliedClient is not null && suppliedClient != registration.ClientId)
                || !fields.TryGetValue("code", out var code) || string.IsNullOrWhiteSpace(code))
                throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
            // Retain the issued registration even when the single-use code expires.
            // Identity remains unset until a signed ID token has been validated.
            if (registration.ClientId is null)
            {
                registration = registration with { ClientId = clientId };
                await store.WriteRegistrationAsync(registration, ct).ConfigureAwait(false);
            }
            var result = await protocol.ExchangeAsync(new Dictionary<string, string>
            {
                ["grant_type"] = "authorization_code",
                ["client_id"] = clientId,
                ["code"] = code,
                ["code_verifier"] = verifier,
                ["redirect_uri"] = redirect.AbsoluteUri,
                ["resource"] = ChatGptOAuth.Resource,
            }, ct).ConfigureAwait(false);
            if (string.IsNullOrEmpty(result.IdToken) || string.IsNullOrEmpty(result.RefreshToken))
                throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
            var identity = await protocol.ValidateAsync(result.IdToken, clientId, nonce, registration.Subject, ct).ConfigureAwait(false);
            if (result.Scope is null || !ChatGptOAuth.HasPlanScope(result.Scope))
                throw new ChatGptAuthException(ChatGptAuthError.PermissionDenied);
            var connectionId = registration.ConnectionId ?? Guid.NewGuid().ToString("N");
            registration = registration with { ConnectionId = connectionId, ClientId = clientId, Subject = identity.Subject, Email = identity.Email };
            ct.ThrowIfCancellationRequested();
            await store.WriteRegistrationAsync(registration, ct).ConfigureAwait(false);
            await store.WriteTokensAsync(new ChatGptTokens(connectionId, result.AccessToken, result.RefreshToken,
                result.IdToken, result.Scope, time.GetUtcNow().AddSeconds(result.ExpiresIn)), ct).ConfigureAwait(false);
            session = new CancellationTokenSource();
            Set(registration, ChatGptConnectionStatus.Connected);
        }
        catch (Exception e)
        {
            Connection = before;
            if (!disposed && before.Status == ChatGptConnectionStatus.Connected) session = new CancellationTokenSource();
            Changed?.Invoke();
            if (e is OperationCanceledException && !ct.IsCancellationRequested)
                throw new ChatGptAuthException(ChatGptAuthError.Network);
            throw Sanitize(e);
        }
        finally { pending = null; gate.Release(); }
    }

    public async Task<string> GetAccessTokenAsync(CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(disposed, this);
        using var linked = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken, session.Token);
        var ct = linked.Token;
        await gate.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            await using var lease = await store.AcquireAsync(ct).ConfigureAwait(false);
            var registration = await RegistrationAsync(ct).ConfigureAwait(false);
            var tokens = await store.ReadTokensAsync(ct).ConfigureAwait(false);
            if (tokens is null || registration.ClientId is null || registration.Subject is null
                || tokens.ConnectionId != registration.ConnectionId || !ChatGptOAuth.HasPlanScope(tokens.Scope))
            {
                session.Cancel();
                Set(registration, ChatGptConnectionStatus.Disconnected);
                throw new ChatGptAuthException(ChatGptAuthError.NotConnected);
            }
            if (tokens.ExpiresAt > time.GetUtcNow().AddMinutes(2))
            {
                ct.ThrowIfCancellationRequested();
                return tokens.AccessToken;
            }
            try
            {
                var result = await protocol.ExchangeAsync(new Dictionary<string, string>
                {
                    ["grant_type"] = "refresh_token",
                    ["client_id"] = registration.ClientId,
                    ["refresh_token"] = tokens.RefreshToken,
                    ["resource"] = ChatGptOAuth.Resource,
                }, ct).ConfigureAwait(false);
                var scope = result.Scope ?? tokens.Scope;
                if (!ChatGptOAuth.HasPlanScope(scope)) throw new ChatGptAuthException(ChatGptAuthError.PermissionDenied);
                if (result.IdToken is not null)
                    await protocol.ValidateAsync(result.IdToken, registration.ClientId, null, registration.Subject, ct).ConfigureAwait(false);
                var refreshed = tokens with
                {
                    AccessToken = result.AccessToken,
                    RefreshToken = result.RefreshToken ?? tokens.RefreshToken,
                    IdToken = result.IdToken ?? tokens.IdToken,
                    Scope = scope,
                    ExpiresAt = time.GetUtcNow().AddSeconds(result.ExpiresIn),
                };
                ct.ThrowIfCancellationRequested();
                await store.WriteTokensAsync(refreshed, ct).ConfigureAwait(false);
                ct.ThrowIfCancellationRequested();
                return refreshed.AccessToken;
            }
            catch (ChatGptAuthException e) when (e.Error is ChatGptAuthError.ReconnectRequired or ChatGptAuthError.PermissionDenied
                or ChatGptAuthError.IdentityMismatch or ChatGptAuthError.InvalidResponse)
            {
                await store.DeleteTokensAsync(CancellationToken.None).ConfigureAwait(false);
                session.Cancel();
                Set(registration, ChatGptConnectionStatus.ReconnectRequired);
                throw;
            }
        }
        catch (Exception e)
        {
            if (e is OperationCanceledException && !ct.IsCancellationRequested)
                throw new ChatGptAuthException(ChatGptAuthError.Network);
            throw Sanitize(e);
        }
        finally { gate.Release(); }
    }

    /// <summary>
    /// A 401 ends only the credential actually rejected. A delayed response
    /// must not erase a newer rotated token. Model/policy denials (403) and
    /// plan limits (429) leave the renewable grant intact. Never retries inference.
    /// </summary>
    public async Task InferenceRejectedAsync(string rejectedAccessToken, int httpStatusCode, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(rejectedAccessToken);
        ObjectDisposedException.ThrowIf(disposed, this);
        if (httpStatusCode != 401) return;
        await gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await using var lease = await store.AcquireAsync(cancellationToken).ConfigureAwait(false);
            var registration = await store.ReadRegistrationAsync(cancellationToken).ConfigureAwait(false);
            var tokens = await store.ReadTokensAsync(cancellationToken).ConfigureAwait(false);
            if (registration is null || tokens is null || tokens.ConnectionId != registration.ConnectionId
                || !string.Equals(tokens.AccessToken, rejectedAccessToken, StringComparison.Ordinal)) return;
            await store.DeleteTokensAsync(cancellationToken).ConfigureAwait(false);
            session.Cancel();
            Set(registration, ChatGptConnectionStatus.ReconnectRequired);
        }
        catch (Exception e) { throw Sanitize(e); }
        finally { gate.Release(); }
    }

    /// <summary>Cancels requests immediately, clears local tokens even if revocation fails.</summary>
    public async Task<bool> DisconnectAsync(CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(disposed, this);
        session.Cancel();
        CancelPending();
        // Cancellation may shorten remote work, never skip local deletion.
        await gate.WaitAsync(CancellationToken.None).ConfigureAwait(false);
        try
        {
            await using var lease = await store.AcquireAsync(CancellationToken.None).ConfigureAwait(false);
            session.Cancel();
            var registration = await RegistrationAsync(CancellationToken.None).ConfigureAwait(false);
            var confirmed = false;
            try
            {
                var tokens = await store.ReadTokensAsync(CancellationToken.None).ConfigureAwait(false);
                confirmed = tokens is null;
                if (tokens is not null && registration.ClientId is not null && tokens.ConnectionId == registration.ConnectionId)
                {
                    using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
                    timeout.CancelAfter(TimeSpan.FromSeconds(15));
                    for (var attempt = 0; attempt < 3 && !confirmed; attempt++)
                    {
                        try { confirmed = await protocol.RevokeAsync(registration.ClientId, tokens.RefreshToken, timeout.Token).ConfigureAwait(false); }
                        catch (HttpRequestException) when (attempt < 2) { }
                        if (!confirmed && attempt < 2)
                            await Task.Delay(TimeSpan.FromMilliseconds(250 * (attempt + 1)), timeout.Token).ConfigureAwait(false);
                    }
                }
            }
            catch (Exception e) when (e is HttpRequestException or OperationCanceledException or ChatGptAuthException or JsonException or IOException)
            { confirmed = false; }
            finally
            {
                await store.DeleteTokensAsync(CancellationToken.None).ConfigureAwait(false);
                Set(registration, ChatGptConnectionStatus.Disconnected);
            }
            return confirmed;
        }
        catch (Exception e) { throw Sanitize(e); }
        finally { gate.Release(); }
    }

    private async Task<ChatGptRegistration> RegistrationAsync(CancellationToken ct)
    {
        var registration = await store.ReadRegistrationAsync(ct).ConfigureAwait(false);
        if (registration is not null) return registration;
        registration = new ChatGptRegistration("urn:uuid:" + Guid.NewGuid());
        await store.WriteRegistrationAsync(registration, ct).ConfigureAwait(false);
        return registration;
    }

    private void Set(ChatGptRegistration registration, ChatGptConnectionStatus status)
    {
        Connection = new ChatGptConnection(status, registration.Email, registration.ConnectionId, registration.ClientId, registration.Subject);
        Changed?.Invoke();
    }

    private void CancelPending()
    {
        try { pending?.Cancel(); }
        catch (ObjectDisposedException) { /* A completed attempt disposed its local cancellation source. */ }
    }

    private static Exception Sanitize(Exception e) => e switch
    {
        ChatGptAuthException or OperationCanceledException => e,
        HttpRequestException => new ChatGptAuthException(ChatGptAuthError.Network),
        JsonException or SecurityTokenException or ArgumentException => new ChatGptAuthException(ChatGptAuthError.InvalidResponse),
        _ => new ChatGptAuthException(ChatGptAuthError.Storage),
    };

    public void Dispose()
    {
        if (disposed) return;
        disposed = true;
        lifetime.Cancel();
        session.Cancel();
        CancelPending();
        // Do not dispose synchronization primitives while a canceled network
        // operation is unwinding. They own no native wait handles here.
    }
}
