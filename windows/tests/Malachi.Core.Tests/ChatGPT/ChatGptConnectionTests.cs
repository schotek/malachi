// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC regression tests, no Swift/Go counterpart. All network
// traffic is injected; keys, users and tokens are generated only for tests.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.ChatGPT;
using Microsoft.Extensions.Time.Testing;
using Microsoft.IdentityModel.JsonWebTokens;
using Microsoft.IdentityModel.Tokens;
using Xunit;

namespace Malachi.Core.Tests.ChatGPT;

public sealed class ChatGptConnectionTests
{
    [Fact]
    public async Task RegistrationUsesIssuedClientAndValidatedIdentity()
    {
        using var h = new Harness();
        await h.Service.InitializeAsync(TestContext.Current.CancellationToken);
        var host = h.Store.Registration!.HostId;
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        Assert.Equal(ChatGptConnectionStatus.Connected, h.Service.Connection.Status);
        Assert.Equal("person@example.test", h.Service.Connection.Email);
        Assert.Equal("issued-client", h.Store.Registration.ClientId);
        Assert.Equal("subject-one", h.Store.Registration.Subject);
        Assert.Equal(host, h.Store.Registration.HostId);
        Assert.Equal("dynamic_agent_client", h.Authorization!["client_id"]);
        Assert.Equal("Malachi Mail", h.Authorization["agent_name_hint"]);
        Assert.Equal("issued-client", h.Exchange!["client_id"]);
        Assert.Equal(h.Authorization["redirect_uri"], h.Exchange["redirect_uri"]);
        Assert.Equal(h.Authorization["code_challenge"], ChatGptOAuth.Challenge(h.Exchange["code_verifier"]));
        Assert.Equal("access-first", await h.Service.GetAccessTokenAsync(TestContext.Current.CancellationToken));
        Assert.Equal("ChatGptTokens", h.Store.Tokens!.ToString());
    }

    [Theory]
    [InlineData("signature")]
    [InlineData("algorithm")]
    [InlineData("azp")]
    [InlineData("no-subject")]
    [InlineData("future-iat")]
    [InlineData("issuer")]
    [InlineData("audience")]
    [InlineData("expired")]
    [InlineData("nonce")]
    [InlineData("scope")]
    [InlineData("state")]
    [InlineData("duplicate-state")]
    [InlineData("no-client")]
    [InlineData("dynamic-client")]
    [InlineData("jwks-host")]
    [InlineData("discovery-issuer")]
    public async Task InvalidAuthorizationNeverStoresTokens(string fault)
    {
        using var h = new Harness { Fault = fault };
        await Assert.ThrowsAsync<ChatGptAuthException>(() => h.Service.SignInAsync(TestContext.Current.CancellationToken));
        Assert.Null(h.Store.Tokens);
        Assert.NotEqual(ChatGptConnectionStatus.Connected, h.Service.Connection.Status);
    }

    [Theory]
    [InlineData("changed-client")]
    [InlineData("changed-subject")]
    [InlineData("denied")]
    public async Task ReauthorizationCannotReplaceIdentityAndRestoresPreviousSession(string fault)
    {
        using var h = new Harness();
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        var oldTokens = h.Store.Tokens;
        var session = h.Service.SessionCancellation;
        h.Fault = fault;
        await Assert.ThrowsAsync<ChatGptAuthException>(() => h.Service.SignInAsync(TestContext.Current.CancellationToken));
        Assert.True(session.IsCancellationRequested);
        Assert.False(h.Service.SessionCancellation.IsCancellationRequested);
        Assert.Same(oldTokens, h.Store.Tokens);
        Assert.Equal("access-first", await h.Service.GetAccessTokenAsync(TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task ExpiredAuthorizationCodeRetainsIssuedRegistrationForRetry()
    {
        using var h = new Harness { Fault = "invalid-grant" };
        await Assert.ThrowsAsync<ChatGptAuthException>(() => h.Service.SignInAsync(TestContext.Current.CancellationToken));
        Assert.Equal("issued-client", h.Store.Registration!.ClientId);
        Assert.Null(h.Store.Tokens);
        h.Fault = null;
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        Assert.Equal("issued-client", h.Authorization!["client_id"]);
        Assert.False(h.Authorization.ContainsKey("agent_name_hint"));
    }

    [Fact]
    public async Task DisposingCancelsPendingSignInWithoutSavingTokens()
    {
        using var h = new Harness { Fault = "wait-browser" };
        var attempt = h.Service.SignInAsync(TestContext.Current.CancellationToken);
        await h.BrowserStarted.Task;
        h.Service.Dispose();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => attempt);
        Assert.Null(h.Store.Tokens);
        Assert.True(h.Service.SessionCancellation.IsCancellationRequested);
    }

    [Fact]
    public async Task ConcurrentCoordinatorsRefreshRotationOnlyOnce()
    {
        using var h = new Harness();
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        h.Time.Advance(TimeSpan.FromHours(1));
        using var other = new ChatGptConnectionService(h.Http, h.Store, h, h.Time);
        var values = await Task.WhenAll(h.Service.GetAccessTokenAsync(TestContext.Current.CancellationToken), other.GetAccessTokenAsync(TestContext.Current.CancellationToken), h.Service.GetAccessTokenAsync(TestContext.Current.CancellationToken));
        Assert.All(values, value => Assert.Equal("access-refreshed", value));
        Assert.Equal(1, h.Refreshes);
        Assert.Equal("refresh-rotated", h.Store.Tokens!.RefreshToken);
        Assert.Equal("issued-client", h.Exchange!["client_id"]);
        Assert.False(h.Exchange.ContainsKey("scope"));
    }

    [Fact]
    public async Task UnauthorizedInferenceEndsRejectedSessionWithoutResubmitting()
    {
        using var h = new Harness();
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        await h.Service.InferenceRejectedAsync("access-first", 401, TestContext.Current.CancellationToken);
        Assert.Null(h.Store.Tokens);
        Assert.True(h.Service.SessionCancellation.IsCancellationRequested);
        Assert.Equal(ChatGptConnectionStatus.ReconnectRequired, h.Service.Connection.Status);
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => h.Service.GetAccessTokenAsync(TestContext.Current.CancellationToken));
        Assert.Equal(1, h.BrowserCalls);
        Assert.Equal(0, h.Refreshes);
    }

    [Theory]
    [InlineData(403)]
    [InlineData(429)]
    public async Task InferencePolicyOrUsageDenialDoesNotEraseGrant(int status)
    {
        using var h = new Harness();
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        await h.Service.InferenceRejectedAsync("access-first", status, TestContext.Current.CancellationToken);
        Assert.NotNull(h.Store.Tokens);
        Assert.False(h.Service.SessionCancellation.IsCancellationRequested);
        Assert.Equal(ChatGptConnectionStatus.Connected, h.Service.Connection.Status);
        Assert.Equal("access-first", await h.Service.GetAccessTokenAsync(TestContext.Current.CancellationToken));
        Assert.Equal(1, h.BrowserCalls);
    }

    [Fact]
    public async Task LateUnauthorizedResponseCannotEraseRotatedCredential()
    {
        using var h = new Harness();
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        h.Time.Advance(TimeSpan.FromHours(1));
        Assert.Equal("access-refreshed", await h.Service.GetAccessTokenAsync(TestContext.Current.CancellationToken));
        await h.Service.InferenceRejectedAsync("access-first", 401, TestContext.Current.CancellationToken);
        Assert.Equal("access-refreshed", await h.Service.GetAccessTokenAsync(TestContext.Current.CancellationToken));
        Assert.False(h.Service.SessionCancellation.IsCancellationRequested);
        Assert.Equal(ChatGptConnectionStatus.Connected, h.Service.Connection.Status);
    }

    [Fact]
    public async Task InvalidGrantDeletesSessionAndRequiresReconnect()
    {
        using var h = new Harness();
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        h.Time.Advance(TimeSpan.FromHours(1));
        h.Fault = "invalid-grant";
        var error = await Assert.ThrowsAsync<ChatGptAuthException>(() => h.Service.GetAccessTokenAsync(TestContext.Current.CancellationToken));
        Assert.Equal(ChatGptAuthError.ReconnectRequired, error.Error);
        Assert.Null(h.Store.Tokens);
        Assert.True(h.Service.SessionCancellation.IsCancellationRequested);
        Assert.Equal(ChatGptConnectionStatus.ReconnectRequired, h.Service.Connection.Status);
    }

    [Theory]
    [InlineData("network")]
    [InlineData("http-timeout")]
    public async Task TemporaryRefreshFailureKeepsGrantAndNeverStartsBrowser(string fault)
    {
        using var h = new Harness();
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        var saved = h.Store.Tokens;
        h.Time.Advance(TimeSpan.FromHours(1));
        h.Fault = fault;
        var error = await Assert.ThrowsAsync<ChatGptAuthException>(() => h.Service.GetAccessTokenAsync(TestContext.Current.CancellationToken));
        Assert.Equal(ChatGptAuthError.Network, error.Error);
        Assert.Same(saved, h.Store.Tokens);
        Assert.Equal(1, h.BrowserCalls);
        Assert.False(h.Service.SessionCancellation.IsCancellationRequested);
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task DisconnectClearsAllTokensRetainsRegistrationAndReportsRevocation(bool failure)
    {
        using var h = new Harness();
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        var registration = h.Store.Registration;
        h.Fault = failure ? "revocation" : null;
        Assert.Equal(!failure, await h.Service.DisconnectAsync(TestContext.Current.CancellationToken));
        Assert.Null(h.Store.Tokens);
        Assert.Equal(registration, h.Store.Registration);
        Assert.Equal(ChatGptConnectionStatus.Disconnected, h.Service.Connection.Status);
        Assert.True(h.Service.SessionCancellation.IsCancellationRequested);
        Assert.Equal("refresh-first", h.Revocation!["token"]);
        Assert.Equal("issued-client", h.Revocation["client_id"]);
        h.Fault = null;
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        Assert.Equal("issued-client", h.Authorization!["client_id"]);
        Assert.False(h.Authorization.ContainsKey("agent_name_hint"));
        Assert.False(h.Authorization.ContainsKey("id_token_hint"));
        Assert.Equal(registration!.HostId, h.Authorization["ext_agent_host_id"]);
    }

    [Fact]
    public async Task DisconnectCancelsPendingBrowserAndPreventsLateCommit()
    {
        using var h = new Harness { Fault = "wait-browser" };
        var signIn = h.Service.SignInAsync(TestContext.Current.CancellationToken);
        await h.BrowserStarted.Task;
        Assert.True(await h.Service.DisconnectAsync(TestContext.Current.CancellationToken));
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => signIn);
        Assert.Null(h.Store.Tokens);
        Assert.True(h.Service.SessionCancellation.IsCancellationRequested);
        Assert.Equal(ChatGptConnectionStatus.Disconnected, h.Service.Connection.Status);
    }

    [Fact]
    public async Task CanceledDisconnectStillDeletesLocalSession()
    {
        using var h = new Harness();
        await h.Service.SignInAsync(TestContext.Current.CancellationToken);
        using var canceled = new CancellationTokenSource();
        canceled.Cancel();
        Assert.False(await h.Service.DisconnectAsync(canceled.Token));
        Assert.Null(h.Store.Tokens);
    }

    [Fact]
    public void CallbackRejectsWrongHostPathPortAndDuplicateParameters()
    {
        var redirect = new Uri("http://127.0.0.1:1455/auth/callback");
        foreach (var callback in new[] {
            "http://localhost:1455/auth/callback?state=s", "http://127.0.0.1:1456/auth/callback?state=s",
            "http://127.0.0.1:1455/callback?state=s", "http://127.0.0.1:1455/auth/callback?state=s&state=s" })
            Assert.Throws<ChatGptAuthException>(() => ChatGptOAuth.ParseCallback(new Uri(callback), redirect, "s"));
    }

    private sealed class Harness : HttpMessageHandler, IChatGptBrowser
    {
        private readonly RSA rsa = RSA.Create(2048);
        public FakeTimeProvider Time { get; } = new(new DateTimeOffset(2026, 10, 2, 10, 0, 0, TimeSpan.Zero));
        public MemoryStore Store { get; } = new();
        public HttpClient Http { get; }
        public ChatGptConnectionService Service { get; }
        public string? Fault { get; set; }
        public Dictionary<string, string>? Authorization { get; private set; }
        public Dictionary<string, string>? Exchange { get; private set; }
        public Dictionary<string, string>? Revocation { get; private set; }
        public int Refreshes { get; private set; }
        public int BrowserCalls { get; private set; }
        public TaskCompletionSource BrowserStarted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        public Harness()
        {
            Http = new HttpClient(this, disposeHandler: false);
            Service = new ChatGptConnectionService(Http, Store, this, Time);
        }

        public async Task<Uri> AuthorizeAsync(Func<Uri, Uri> authorizationUrl, string state, CancellationToken cancellationToken)
        {
            BrowserCalls++;
            var redirect = new Uri("http://127.0.0.1:1455/auth/callback");
            Authorization = Form(authorizationUrl(redirect).Query.TrimStart('?'));
            BrowserStarted.TrySetResult();
            if (Fault == "wait-browser") await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
            var client = Fault == "no-client" ? "" : "&client_id=" + (Fault == "dynamic-client" ? ChatGptOAuth.DynamicClient : Fault == "changed-client" ? "other-client" : "issued-client");
            return new Uri(redirect + "?code=test-code&state=" + (Fault == "state" ? "wrong" : state)
                + (Fault == "duplicate-state" ? "&state=" + state : "") + client + (Fault == "denied" ? "&error=access_denied" : ""));
        }

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            var path = request.RequestUri!.AbsolutePath;
            if (path == "/.well-known/openid-configuration") return Json(new
            {
                issuer = Fault == "discovery-issuer" ? "https://wrong.test" : ChatGptOAuth.Issuer,
                jwks_uri = Fault == "jwks-host" ? "https://wrong.test/jwks" : ChatGptOAuth.Issuer + "/jwks",
                revocation_endpoint = ChatGptOAuth.Issuer + "/revoke",
            });
            if (path == "/jwks")
            {
                var key = rsa.ExportParameters(false);
                return Json(new { keys = new[] { new { kty = "RSA", kid = "test", use = "sig", alg = "RS256", n = Base64UrlEncoder.Encode(key.Modulus!), e = Base64UrlEncoder.Encode(key.Exponent!) } } });
            }
            var form = Form(await request.Content!.ReadAsStringAsync(cancellationToken));
            if (path == "/revoke")
            {
                Revocation = form;
                return new HttpResponseMessage(Fault == "revocation" ? HttpStatusCode.ServiceUnavailable : HttpStatusCode.OK);
            }
            Assert.Equal("/api/accounts/oauth/token", path);
            Exchange = form;
            var refresh = form["grant_type"] == "refresh_token";
            if (refresh) Refreshes++;
            if (Fault == "invalid-grant") return Json(new { error = "invalid_grant" }, HttpStatusCode.BadRequest);
            if (Fault == "http-timeout") throw new TaskCanceledException("http deadline");
            if (Fault == "network") throw new HttpRequestException("secret-bearing diagnostic must not escape");
            return Json(new
            {
                access_token = refresh ? "access-refreshed" : "access-first",
                refresh_token = refresh ? "refresh-rotated" : "refresh-first",
                id_token = refresh ? null : IdToken(), token_type = "Bearer", expires_in = 3600,
                scope = Fault == "scope" ? "openid profile email" : ChatGptOAuth.Scope,
            });
        }

        private string IdToken()
        {
            var descriptor = new SecurityTokenDescriptor
            {
                Issuer = Fault == "issuer" ? "https://wrong.test" : ChatGptOAuth.Issuer,
                Audience = Fault == "audience" ? "wrong-client" : "issued-client",
                IssuedAt = Time.GetUtcNow().UtcDateTime.AddMinutes(Fault == "future-iat" ? 3 : -10),
                NotBefore = Time.GetUtcNow().UtcDateTime.AddMinutes(-10),
                Expires = Time.GetUtcNow().UtcDateTime.AddMinutes(Fault == "expired" ? -5 : 5),
                SigningCredentials = new SigningCredentials(new RsaSecurityKey(rsa) { KeyId = "test" }, Fault == "algorithm" ? SecurityAlgorithms.RsaSha512 : SecurityAlgorithms.RsaSha256),
                Claims = new Dictionary<string, object>
                {
                    ["sub"] = Fault == "no-subject" ? "" : Fault == "changed-subject" ? "subject-two" : "subject-one",
                    ["email"] = "person@example.test", ["nonce"] = Fault == "nonce" ? "wrong" : Authorization!["nonce"],
                },
            };
            if (Fault == "azp") descriptor.Claims["azp"] = "different-client";
            var token = new JsonWebTokenHandler().CreateToken(descriptor);
            if (Fault == "signature")
            {
                var parts = token.Split('.');
                var signature = Base64UrlEncoder.DecodeBytes(parts[2]);
                signature[0] ^= 1;
                token = parts[0] + "." + parts[1] + "." + Base64UrlEncoder.Encode(signature);
            }
            return token;
        }

        private static HttpResponseMessage Json<T>(T body, HttpStatusCode status = HttpStatusCode.OK) => new(status)
        { Content = new StringContent(JsonSerializer.Serialize(body), Encoding.UTF8, "application/json") };
        private static Dictionary<string, string> Form(string value) => value.Split('&').Select(part => part.Split('=', 2))
            .ToDictionary(pair => Uri.UnescapeDataString(pair[0]), pair => Uri.UnescapeDataString(pair[1].Replace('+', ' ')), StringComparer.Ordinal);

        protected override void Dispose(bool disposing)
        {
            if (disposing) { Service.Dispose(); Http.Dispose(); rsa.Dispose(); Store.Dispose(); }
            base.Dispose(disposing);
        }
    }

    private sealed class MemoryStore : IChatGptCredentialStore, IDisposable
    {
        private readonly SemaphoreSlim gate = new(1);
        public ChatGptRegistration? Registration { get; private set; }
        public ChatGptTokens? Tokens { get; private set; }
        public async Task<IAsyncDisposable> AcquireAsync(CancellationToken cancellationToken)
        {
            await gate.WaitAsync(cancellationToken);
            return new Lease(gate);
        }
        public Task<ChatGptRegistration?> ReadRegistrationAsync(CancellationToken cancellationToken) => Task.FromResult(Registration);
        public Task WriteRegistrationAsync(ChatGptRegistration registration, CancellationToken cancellationToken) { Registration = registration; return Task.CompletedTask; }
        public Task<ChatGptTokens?> ReadTokensAsync(CancellationToken cancellationToken) => Task.FromResult(Tokens);
        public Task WriteTokensAsync(ChatGptTokens tokens, CancellationToken cancellationToken) { Tokens = tokens; return Task.CompletedTask; }
        public Task DeleteTokensAsync(CancellationToken cancellationToken) { Tokens = null; return Task.CompletedTask; }
        public void Dispose() => gate.Dispose();
        private sealed class Lease(SemaphoreSlim gate) : IAsyncDisposable
        { public ValueTask DisposeAsync() { gate.Release(); return ValueTask.CompletedTask; } }
    }
}
