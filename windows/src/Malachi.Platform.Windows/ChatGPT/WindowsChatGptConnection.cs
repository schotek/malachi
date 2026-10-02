// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC composition, no existing Swift/Go counterpart.

using System;
using System.Net.Http;
using Malachi.Core.ChatGPT;

namespace Malachi.Platform.Windows.ChatGPT;

public static class WindowsChatGptConnection
{
    // Never follow an HTTP redirect with a code or token-bearing request.
    private static readonly HttpClient Http = new(new SocketsHttpHandler
    {
        AllowAutoRedirect = false,
        UseCookies = false,
        PooledConnectionLifetime = TimeSpan.FromMinutes(5),
    }) { Timeout = TimeSpan.FromSeconds(30) };

    public static ChatGptConnectionService Create(string directory) => new(Http,
        new WindowsChatGptCredentialStore(directory), new WindowsChatGptBrowser());
}
