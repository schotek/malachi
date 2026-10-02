// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first SIWC protocol; no existing Swift/Go counterpart.
// Based on OpenAI's registration-and-sign-in and accounts-and-sessions guides.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Security.Cryptography;
using System.Text;

namespace Malachi.Core.ChatGPT;

public static class ChatGptOAuth
{
    public const string Issuer = "https://auth.openai.com";
    public const string Resource = "https://api.openai.com/v1";
    public const string Scope = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct";
    public const string DynamicClient = "dynamic_agent_client";
    public const string CallbackPath = "/auth/callback";
    public static Uri UsageUri { get; } = new("https://chatgpt.com/settings/usage");

    public static string RandomValue() => Base64Url(RandomNumberGenerator.GetBytes(32));
    public static string Challenge(string verifier) => Base64Url(SHA256.HashData(Encoding.ASCII.GetBytes(verifier)));
    private static string Base64Url(byte[] bytes) => Convert.ToBase64String(bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_');

    public static Uri AuthorizationUri(ChatGptRegistration registration, Uri redirect, string state, string nonce, string verifier)
    {
        if (redirect.Scheme != "http" || redirect.Host != "127.0.0.1" || redirect.AbsolutePath != CallbackPath
            || redirect.Query.Length != 0 || redirect.Fragment.Length != 0 || redirect.UserInfo.Length != 0)
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        var fields = new Dictionary<string, string>
        {
            ["client_id"] = registration.ClientId ?? DynamicClient,
            ["ext_agent_host_id"] = registration.HostId,
            ["response_type"] = "code", ["redirect_uri"] = redirect.AbsoluteUri,
            ["scope"] = Scope, ["resource"] = Resource, ["state"] = state,
            ["nonce"] = nonce, ["code_challenge_method"] = "S256", ["code_challenge"] = Challenge(verifier),
        };
        if (registration.ClientId is null) fields["agent_name_hint"] = "Malachi Mail";
        // Do not put retained tokens in a browser command line. The optional
        // login hint is enough; a returning sign-in shows the account selector.
        else if (registration.Email is not null) fields["login_hint"] = registration.Email;
        return new Uri(Issuer + "/api/accounts/authorize?" + string.Join('&', fields.Select(
            item => Uri.EscapeDataString(item.Key) + "=" + Uri.EscapeDataString(item.Value))));
    }

    public static IReadOnlyDictionary<string, string> ParseCallback(Uri callback, Uri redirect, string expectedState)
    {
        if (callback.GetLeftPart(UriPartial.Path) != redirect.AbsoluteUri || callback.Fragment.Length != 0)
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        var fields = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var field in callback.Query.TrimStart('?').Split('&', StringSplitOptions.RemoveEmptyEntries))
        {
            var pair = field.Split('=', 2);
            var name = Uri.UnescapeDataString(pair[0].Replace('+', ' '));
            var value = pair.Length == 2 ? Uri.UnescapeDataString(pair[1].Replace('+', ' ')) : "";
            if (!fields.TryAdd(name, value)) throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        }
        if (!fields.TryGetValue("state", out var state) || !CryptographicOperations.FixedTimeEquals(
                Encoding.UTF8.GetBytes(state), Encoding.UTF8.GetBytes(expectedState)))
            throw new ChatGptAuthException(ChatGptAuthError.InvalidResponse);
        return fields;
    }

    public static bool HasPlanScope(string scope)
    {
        var scopes = scope.Split(' ', StringSplitOptions.RemoveEmptyEntries).ToHashSet(StringComparer.Ordinal);
        return Scope.Split(' ').All(scopes.Contains);
    }
}
