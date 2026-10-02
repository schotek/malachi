// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC, no existing Swift/Go counterpart. Cryptographic JWT
// validation belongs to Microsoft's IdentityModel library, never a decoder.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Net.Http;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.IdentityModel.JsonWebTokens;
using Microsoft.IdentityModel.Tokens;

namespace Malachi.Core.ChatGPT;

internal sealed class ChatGptIdentityProtocol(HttpClient http, TimeProvider time)
{
    private const int MaxResponse = 256 * 1024;
    private static readonly Uri Discovery = new(ChatGptOAuth.Issuer + "/.well-known/openid-configuration");
    private static readonly Uri TokenEndpoint = new(ChatGptOAuth.Issuer + "/api/accounts/oauth/token");

    internal sealed record TokenResponse(string AccessToken, string? RefreshToken, string? IdToken, string? Scope, long ExpiresIn);
    internal sealed record Identity(string Subject, string? Email);

    internal async Task<TokenResponse> ExchangeAsync(Dictionary<string, string> fields, CancellationToken ct)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, TokenEndpoint) { Content = new FormUrlEncodedContent(fields) };
        using var response = await http.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, ct).ConfigureAwait(false);
        using var body = await ReadJsonAsync(response, ct).ConfigureAwait(false);
        var json = body.RootElement;
        if (!response.IsSuccessStatusCode)
        {
            var error = String(json, "error");
            throw new ChatGptAuthException(error == "invalid_grant" ? ChatGptAuthError.ReconnectRequired
                : (int)response.StatusCode >= 500 ? ChatGptAuthError.Network : ChatGptAuthError.PermissionDenied);
        }
        var access = String(json, "access_token");
        if (string.IsNullOrWhiteSpace(access) || !string.Equals(String(json, "token_type"), "Bearer", StringComparison.OrdinalIgnoreCase)
            || !json.TryGetProperty("expires_in", out var expiry) || !expiry.TryGetInt64(out var seconds)
            || seconds <= 0 || seconds > 7 * 24 * 60 * 60)
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        var refresh = String(json, "refresh_token");
        var idToken = String(json, "id_token");
        if (refresh is not null && string.IsNullOrWhiteSpace(refresh) || idToken is not null && string.IsNullOrWhiteSpace(idToken))
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        return new TokenResponse(access, refresh, idToken, String(json, "scope"), seconds);
    }

    internal async Task<Identity> ValidateAsync(string idToken, string clientId, string? nonce, string? expectedSubject, CancellationToken ct)
    {
        using var discovery = await GetAsync(Discovery, ct).ConfigureAwait(false);
        if (String(discovery.RootElement, "issuer") != ChatGptOAuth.Issuer)
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        var jwksUri = TrustedEndpoint(String(discovery.RootElement, "jwks_uri"));
        using var jwks = await GetAsync(jwksUri, ct).ConfigureAwait(false);
        var keys = new JsonWebKeySet(jwks.RootElement.GetRawText()).GetSigningKeys();
        var now = time.GetUtcNow().UtcDateTime;
        var validation = new TokenValidationParameters
        {
            ValidIssuer = ChatGptOAuth.Issuer,
            ValidAudience = clientId,
            ValidateIssuer = true, ValidateAudience = true, ValidateIssuerSigningKey = true,
            RequireSignedTokens = true, RequireExpirationTime = true, ValidateLifetime = true,
            IssuerSigningKeys = keys,
            ValidAlgorithms = [SecurityAlgorithms.RsaSha256],
            ClockSkew = TimeSpan.FromSeconds(60),
            LifetimeValidator = (before, expires, _, _) => expires is not null && expires > now.AddSeconds(-60)
                && (before is null || (before <= now.AddSeconds(60) && before < expires)),
        };
        var result = await new JsonWebTokenHandler { MaximumTokenSizeInBytes = 64 * 1024, MapInboundClaims = false }
            .ValidateTokenAsync(idToken, validation).ConfigureAwait(false);
        if (!result.IsValid || result.SecurityToken is not JsonWebToken jwt)
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        // All claims below are read only after signature and claim validation.
        if (!jwt.TryGetPayloadValue<long>("iat", out var issuedAt) || !jwt.TryGetPayloadValue<long>("exp", out var expiresAt)
            || issuedAt > time.GetUtcNow().AddSeconds(60).ToUnixTimeSeconds() || issuedAt >= expiresAt)
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        if (!jwt.TryGetPayloadValue<string>("sub", out var subject) || string.IsNullOrWhiteSpace(subject)
            || (nonce is not null && (!jwt.TryGetPayloadValue<string>("nonce", out var actualNonce) || actualNonce != nonce)))
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        if (expectedSubject is not null && subject != expectedSubject)
            throw new ChatGptAuthException(ChatGptAuthError.IdentityMismatch);
        var audiences = jwt.Audiences.ToArray();
        if ((audiences.Length > 1 || jwt.TryGetPayloadValue<string>("azp", out _))
            && (!jwt.TryGetPayloadValue<string>("azp", out var azp) || azp != clientId))
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        jwt.TryGetPayloadValue<string>("email", out var email);
        return new Identity(subject, email);
    }

    internal async Task<bool> RevokeAsync(string clientId, string refreshToken, CancellationToken ct)
    {
        using var discovery = await GetAsync(Discovery, ct).ConfigureAwait(false);
        if (String(discovery.RootElement, "issuer") != ChatGptOAuth.Issuer)
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        var endpoint = TrustedEndpoint(String(discovery.RootElement, "revocation_endpoint"));
        using var request = new HttpRequestMessage(HttpMethod.Post, endpoint)
        {
            Content = new FormUrlEncodedContent(new Dictionary<string, string>
            { ["token"] = refreshToken, ["token_type_hint"] = "refresh_token", ["client_id"] = clientId }),
        };
        using var response = await http.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, ct).ConfigureAwait(false);
        return response.StatusCode == System.Net.HttpStatusCode.OK;
    }

    private async Task<JsonDocument> GetAsync(Uri uri, CancellationToken ct)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, uri);
        using var response = await http.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, ct).ConfigureAwait(false);
        if (!response.IsSuccessStatusCode) throw new ChatGptAuthException(ChatGptAuthError.Network);
        return await ReadJsonAsync(response, ct).ConfigureAwait(false);
    }

    private static Uri TrustedEndpoint(string? value)
    {
        if (!Uri.TryCreate(value, UriKind.Absolute, out var uri) || uri.Scheme != "https" || uri.Host != "auth.openai.com"
            || !uri.IsDefaultPort || uri.UserInfo.Length != 0 || uri.Fragment.Length != 0)
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        return uri;
    }

    private static async Task<JsonDocument> ReadJsonAsync(HttpResponseMessage response, CancellationToken ct)
    {
        if (response.Content.Headers.ContentLength > MaxResponse) throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        await using var stream = await response.Content.ReadAsStreamAsync(ct).ConfigureAwait(false);
        using var buffer = new MemoryStream();
        var block = new byte[8192];
        while (true)
        {
            var count = await stream.ReadAsync(block, ct).ConfigureAwait(false);
            if (count == 0) break;
            if (buffer.Length + count > MaxResponse) throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
            buffer.Write(block, 0, count);
        }
        return JsonDocument.Parse(buffer.GetBuffer().AsMemory(0, (int)buffer.Length), new JsonDocumentOptions { MaxDepth = 16 });
    }

    internal static string? String(JsonElement value, string name) => value.TryGetProperty(name, out var property)
        && property.ValueKind == JsonValueKind.String ? property.GetString() : null;
}
