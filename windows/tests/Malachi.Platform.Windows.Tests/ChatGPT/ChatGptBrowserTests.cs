// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC loopback tests, no Swift/Go counterpart. The browser
// launcher is injected; no real browser or OpenAI endpoint is contacted.

using System;
using System.Net;
using System.Net.Http;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.ChatGPT;
using Malachi.Platform.Windows.ChatGPT;
using Xunit;

namespace Malachi.Platform.Windows.Tests.ChatGPT;

public sealed class ChatGptBrowserTests
{
    [Fact]
    public async Task ListenerRejectsMethodPathAndStateThenClosesAfterMatchingCallback()
    {
        using var handler = new SocketsHttpHandler { UseProxy = false, AllowAutoRedirect = false };
        using var http = new HttpClient(handler);
        var redirectReady = new TaskCompletionSource<Uri>(TaskCreationOptions.RunContinuationsAsynchronously);
        var browser = new WindowsChatGptBrowser((uri, _) => { redirectReady.TrySetResult(uri); return Task.CompletedTask; });
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(TestContext.Current.CancellationToken);
        timeout.CancelAfter(TimeSpan.FromSeconds(10));
        // Returning the redirect itself is deliberate: the injected launch
        // function receives it, never the production shell launcher.
        var authorization = browser.AuthorizeAsync(uri => uri, "state-test", timeout.Token);
        var redirect = await redirectReady.Task.WaitAsync(timeout.Token);
        Assert.Equal("127.0.0.1", redirect.Host);
        Assert.Equal(ChatGptOAuth.CallbackPath, redirect.AbsolutePath);
        using (var post = await http.PostAsync(new Uri(redirect + "?state=state-test&code=code-test"), null, timeout.Token))
            Assert.Equal(HttpStatusCode.BadRequest, post.StatusCode);
        using (var wrongPath = await http.GetAsync(new Uri(redirect, "/callback?state=state-test&code=code-test"), timeout.Token))
            Assert.Equal(HttpStatusCode.BadRequest, wrongPath.StatusCode);
        using (var wrongState = await http.GetAsync(new Uri(redirect + "?state=stale&code=code-test"), timeout.Token))
            Assert.Equal(HttpStatusCode.BadRequest, wrongState.StatusCode);
        using (var success = await http.GetAsync(new Uri(redirect + "?state=state-test&code=code-test"), timeout.Token))
        {
            Assert.Equal(HttpStatusCode.OK, success.StatusCode);
            var text = await success.Content.ReadAsStringAsync(timeout.Token);
            Assert.DoesNotContain("state-test", text, StringComparison.Ordinal);
            Assert.DoesNotContain("code-test", text, StringComparison.Ordinal);
        }
        Assert.Equal("?state=state-test&code=code-test", (await authorization).Query);
        await Assert.ThrowsAsync<HttpRequestException>(() => http.GetAsync(redirect, timeout.Token));
    }

    [Fact]
    public async Task CancelingAttemptClosesItsReservedPort()
    {
        var ready = new TaskCompletionSource<Uri>(TaskCreationOptions.RunContinuationsAsynchronously);
        var browser = new WindowsChatGptBrowser((uri, _) => { ready.TrySetResult(uri); return Task.CompletedTask; });
        using var cancellation = CancellationTokenSource.CreateLinkedTokenSource(TestContext.Current.CancellationToken);
        var attempt = browser.AuthorizeAsync(uri => uri, "state-test", cancellation.Token);
        var redirect = await ready.Task.WaitAsync(TestContext.Current.CancellationToken);
        cancellation.Cancel();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => attempt);
        using var handler = new SocketsHttpHandler { UseProxy = false };
        using var http = new HttpClient(handler);
        await Assert.ThrowsAsync<HttpRequestException>(() => http.GetAsync(redirect, TestContext.Current.CancellationToken));
    }
}
