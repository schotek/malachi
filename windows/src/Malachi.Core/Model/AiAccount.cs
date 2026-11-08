// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AIAccount.swift; GTK:
// ui/internal/aiaccount text.go (Strings, Texts, HTTPWarning,
// AddressExample, AddressExamples, AddressHint, ConsentStrings,
// ConsentTexts), failure.go (Field, Failure, FailureOf) and the address and
// model rules of controller.go (NormalizeBaseURL, hostOf, IsLoopback,
// checkModel, maxModelBytes, maxBaseURLBytes). The texts of the dialog that
// adds or edits an AI provider account (kind ai, docs/api.md §4.1; the
// design is docs/plans/ai-api.md §7.1), the consent it asks before the
// account is saved (D7, Q17), the fields it flags, how a failed call is
// worded by its error code, and the daemon's rules for a server address and
// a model id. Pure: no RPC; AiAccountController is the dialog over it.
//
// Windows: Swift's AIAccount enum is this static class with its types nested
// (AiAccount.Strings, AiAccount.Field, …). The address is read as Go's
// url.Parse reads it (UrlSyntax), its escaped path as url.URL.EscapedPath
// does, as the Swift port does; Go's strings.TrimSpace is string.Trim, as in
// the other ports. IsLoopback reads an IP address as Go's net.ParseIP does
// (dotted decimal IPv4 without leading zeros, IPv6 without a zone), not as
// IPAddress.TryParse, which also takes "127.1" and plain numbers.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Net;
using System.Net.Sockets;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Model;

/// <summary>The AI provider account dialog's texts and rules (Go's package <c>aiaccount</c>, pure parts).</summary>
public static class AiAccount
{
    /// <summary>aiaccount.maxModelBytes: the daemon's limit of a model id.</summary>
    public const int MaxModelBytes = 256;

    /// <summary>aiaccount.maxBaseURLBytes: the daemon's limit of <c>baseUrl</c>.</summary>
    public const int MaxBaseUrlBytes = 2048;

    /// <summary>aiaccount.Field: a field of the dialog that can be flagged and focused.</summary>
    public enum Field
    {
        /// <summary>No field.</summary>
        None,

        /// <summary>The account's name.</summary>
        Name,

        /// <summary>The server address of an OpenAI-compatible server.</summary>
        Server,

        /// <summary>The API key.</summary>
        Key,

        /// <summary>The model.</summary>
        Model,

        /// <summary>The board's model.</summary>
        BoardModel,

        /// <summary>The consent check box.</summary>
        Consent,
    }

    /// <summary>aiaccount.modelCheck.</summary>
    public enum ModelCheck
    {
        /// <summary>A model id the daemon takes.</summary>
        Ok,

        /// <summary>Empty.</summary>
        Missing,

        /// <summary>Too long, or with a control character.</summary>
        Invalid,
    }

    /// <summary>
    /// aiaccount.Strings: the dialog's texts. Every one is plain text
    /// (nothing is markup); the buttons carry mnemonics.
    /// </summary>
    public sealed record Strings
    {
        /// <summary>The dialog's title when adding.</summary>
        public required string AddTitle { get; init; }

        /// <summary>The dialog's title when editing.</summary>
        public required string EditTitle { get; init; }

        /// <summary>The read-only row naming the kind.</summary>
        public required string ProviderLabel { get; init; }

        /// <summary>The name row.</summary>
        public required string NameLabel { get; init; }

        /// <summary>The address row of an OpenAI-compatible server.</summary>
        public required string ServerLabel { get; init; }

        /// <summary>The line under it (<see cref="AddressHint"/>).</summary>
        public required string ServerHint { get; init; }

        /// <summary>The key row when adding a named provider.</summary>
        public required string KeyLabel { get; init; }

        /// <summary>The key row of a server that may have none.</summary>
        public required string KeyOptionalLabel { get; init; }

        /// <summary>The key row while a stored key is being replaced.</summary>
        public required string NewKeyLabel { get; init; }

        /// <summary>The row of a stored key.</summary>
        public required string KeyStored { get; init; }

        /// <summary>The button of the row of a stored key.</summary>
        public required string ReplaceKey { get; init; }

        /// <summary>Takes the replacement back.</summary>
        public required string KeepKey { get; init; }

        /// <summary>The model row: a text with the provider's models to pick from.</summary>
        public required string ModelLabel { get; init; }

        /// <summary>Under the model row.</summary>
        public required string ModelHint { get; init; }

        /// <summary>The board's model row.</summary>
        public required string BoardModelLabel { get; init; }

        /// <summary>Under the board's model row.</summary>
        public required string BoardModelHint { get; init; }

        /// <summary>The certificate pin of an https server (docs/plans/ai-api.md D8).</summary>
        public required string PinLabel { get; init; }

        /// <summary>Drops the pin.</summary>
        public required string PinForget { get; init; }

        /// <summary>Test lists the provider's models.</summary>
        public required string Test { get; init; }

        /// <summary>Test's progress.</summary>
        public required string Testing { get; init; }

        /// <summary>The status after a test without a model (<see cref="Connected"/> with the number).</summary>
        public required string ConnectedNoModels { get; init; }

        /// <summary>The primary button when adding.</summary>
        public required string Add { get; init; }

        /// <summary>The primary button when editing.</summary>
        public required string Save { get; init; }

        /// <summary>Add's progress.</summary>
        public required string Adding { get; init; }

        /// <summary>Save's progress.</summary>
        public required string Saving { get; init; }

        /// <summary>No name.</summary>
        public required string NameMissing { get; init; }

        /// <summary>No server address.</summary>
        public required string ServerMissing { get; init; }

        /// <summary>Not a web address.</summary>
        public required string ServerInvalid { get; init; }

        /// <summary>A pin needs https.</summary>
        public required string PinNeedsHttps { get; init; }

        /// <summary>No key.</summary>
        public required string KeyMissing { get; init; }

        /// <summary>No model.</summary>
        public required string ModelMissing { get; init; }

        /// <summary>Not a model id.</summary>
        public required string ModelInvalid { get; init; }

        /// <summary>Save with the consent check box off.</summary>
        public required string ConsentMissing { get; init; }

        /// <summary>The provider refused the key.</summary>
        public required string KeyRefused { get; init; }

        /// <summary>The provider could not be reached.</summary>
        public required string Unreachable { get; init; }

        /// <summary>The provider did not answer in time.</summary>
        public required string Timeout { get; init; }

        /// <summary>The connection is not secure.</summary>
        public required string Insecure { get; init; }

        /// <summary>The provider answered with an error.</summary>
        public required string ServerError { get; init; }

        /// <summary>aiaccount.Strings.Connected: the status after a test that listed <paramref name="n"/> models.</summary>
        public string Connected(int n) =>
            n <= 0
                ? ConnectedNoModels
                // TRANSLATORS: status of the AI provider dialog after a test; %d is the number of models the provider listed.
                : L10n.N("Connected · %d model", "Connected · %d models", n);
    }

    /// <summary>aiaccount.AddressExample: one example of an OpenAI-compatible server's address.</summary>
    /// <param name="Name">The server's name (a product, not translated).</param>
    /// <param name="Url">Its usual address.</param>
    public sealed record AddressExample(string Name, string Url);

    /// <summary>
    /// aiaccount.ConsentStrings: the consent the dialog asks before the
    /// account is saved (docs/plans/ai-api.md D7, Q17): the heading, the
    /// body (one common paragraph on what leaves, then one on where it goes,
    /// by the kind of provider) and the check box.
    /// </summary>
    /// <param name="Heading">The heading.</param>
    /// <param name="Common">What leaves.</param>
    /// <param name="Specific">Where it goes.</param>
    /// <param name="Checkbox">The check box.</param>
    public sealed record ConsentStrings(string Heading, string Common, string Specific, string Checkbox)
    {
        /// <summary>aiaccount.ConsentStrings.Body: the two paragraphs.</summary>
        public string Body => Common + "\n\n" + Specific;
    }

    /// <summary>
    /// aiaccount.Failure: what the dialog shows after a failed call: the
    /// banner and the field to flag. An empty banner means the client's
    /// general sentence for the error (RpcErrorText) with the step's action.
    /// </summary>
    /// <param name="Banner">The dialog's sentence; "" for the general one.</param>
    /// <param name="Field">The field to flag.</param>
    public sealed record Failure(string Banner = "", Field Field = Field.None);

    /// <summary>aiaccount.AddressExamples: the examples the hint under the address field names; nothing is prefilled.</summary>
    public static IReadOnlyList<AddressExample> AddressExamples { get; } =
    [
        new("Ollama", "http://localhost:11434/v1"),
        new("llama.cpp", "http://127.0.0.1:9931/v1"),
        new("LM Studio", "http://localhost:1234/v1"),
        new("vLLM", "http://localhost:8000/v1"),
        new("OpenRouter", "https://openrouter.ai/api/v1"),
        new("Groq", "https://api.groq.com/openai/v1"),
        new("Mistral", "https://api.mistral.ai/v1"),
        new("Together", "https://api.together.ai/v1"),
    ];

    /// <summary>aiaccount.Texts: the dialog's texts, translated.</summary>
    public static Strings Texts()
    {
        var x = Assistant.ApiText();
        return new Strings
        {
            // TRANSLATORS: title of the dialog that adds an AI provider account.
            AddTitle = L10n.T("Add AI Provider"),
            EditTitle = L10n.T("Edit Account"),
            // TRANSLATORS: the read-only row of the AI provider dialog naming the kind of provider.
            ProviderLabel = L10n.T("Provider"),
            NameLabel = L10n.T("Account Name"),
            // TRANSLATORS: the base URL of an OpenAI-compatible server ("…/v1").
            ServerLabel = L10n.T("Server Address"),
            ServerHint = AddressHint(),
            KeyLabel = L10n.T("API Key"),
            // TRANSLATORS: the key row of an OpenAI-compatible server, which may need none.
            KeyOptionalLabel = L10n.T("API Key (optional)"),
            NewKeyLabel = L10n.T("New API Key"),
            // TRANSLATORS: the key row of an existing AI provider account: the key is in the keyring and never shown.
            KeyStored = L10n.T("Key stored"),
            // TRANSLATORS: button of the key row of an existing AI provider account.
            ReplaceKey = L10n.T("Replace Key…"),
            // TRANSLATORS: button that takes a key replacement back and keeps the stored key.
            KeepKey = L10n.T("Keep the Stored Key"),
            ModelLabel = x.ModelRow,
            // TRANSLATORS: under the model row of the AI provider dialog; the list comes from Test.
            ModelHint = L10n.T("Choose one of the provider's models or type its id"),
            // TRANSLATORS: the model of the board's triage, an optional second model of the AI provider account.
            BoardModelLabel = L10n.T("Board Model"),
            BoardModelHint = x.BoardModelSubtitle,
            // TRANSLATORS: row of the AI provider dialog showing the pinned certificate of an https server.
            PinLabel = L10n.T("Trusted Certificate"),
            // TRANSLATORS: button that drops the pinned certificate.
            PinForget = L10n.T("Forget"),
            // TRANSLATORS: button of the AI provider dialog: asks the provider for its models with the key.
            Test = L10n.T("_Test"),
            // TRANSLATORS: progressive form for the RPC error text
            Testing = L10n.T("Testing the connection"),
            // TRANSLATORS: status of the AI provider dialog after a test that listed no models.
            ConnectedNoModels = L10n.T("Connected"),
            Add = L10n.T("_Add Account"),
            Save = L10n.T("_Save"),
            Adding = L10n.T("Adding the account"),
            Saving = L10n.T("Saving the account"),
            NameMissing = L10n.T("Enter a name for the account"),
            // TRANSLATORS: the address example stays as it is.
            ServerMissing = L10n.T("Enter the server's address, such as http://localhost:11434/v1"),
            ServerInvalid = L10n.T("This is not a web address"),
            PinNeedsHttps = L10n.T("A certificate can be pinned only for an https address"),
            KeyMissing = L10n.T("Enter the API key"),
            ModelMissing = L10n.T("Enter or choose a model"),
            ModelInvalid = L10n.T("This is not a model id"),
            // TRANSLATORS: Save of the AI provider dialog with the consent check box off.
            ConsentMissing = L10n.T("Allow sending mail to the provider first"),
            KeyRefused = x.AccountKeyRefused,
            Unreachable = L10n.T("The provider could not be reached"),
            Timeout = L10n.T("The provider did not answer in time"),
            Insecure = L10n.T("The connection to the provider is not secure"),
            ServerError = L10n.T("The provider answered with an error"),
        };
    }

    /// <summary>
    /// aiaccount.HTTPWarning: the warning under an http address outside a
    /// loopback address (docs/plans/ai-api.md D8); <paramref name="host"/> is
    /// the address's host.
    /// </summary>
    public static string HttpWarning(string host) =>
        // TRANSLATORS: %s is the host of an OpenAI-compatible server reached over plain http.
        L10n.T("The API key and your mail travel unencrypted over http to %s.", host);

    /// <summary>aiaccount.AddressHint: the line under the address field: the examples, one per line after the sentence.</summary>
    public static string AddressHint()
    {
        // TRANSLATORS: under the address field of an OpenAI-compatible server; the examples follow, one per line.
        var output = new StringBuilder(L10n.T("The server's base URL, usually ending in /v1. For example:"));
        foreach (var e in AddressExamples)
        {
            output.Append('\n').Append(e.Name).Append("  ").Append(e.Url);
        }
        return output.ToString();
    }

    /// <summary>aiaccount.ConsentTexts: the consent of a provider kind.</summary>
    public static ConsentStrings ConsentTexts(AiProvider p)
    {
        var common = L10n.T("The assistant sends the prompt, the text you write, the mail it reads for you and that mail's attachments to the provider. It can read mail and prepare drafts; it cannot send, delete or move messages.");
        // TRANSLATORS: check box of the AI provider dialog; Save needs it on.
        var checkbox = L10n.T("Allow Malachi Mail to send mail to this provider");
        return p.Value switch
        {
            AiProvider.Anthropic => new ConsentStrings(
                L10n.T("Send Mail to Anthropic?"),
                common,
                L10n.T("They go to Anthropic under its API terms, which do not use data sent through the API for training by default."),
                checkbox),
            AiProvider.OpenAi => new ConsentStrings(
                L10n.T("Send Mail to OpenAI?"),
                common,
                L10n.T("They go to OpenAI under its API terms, which do not use data sent through the API for training by default."),
                checkbox),
            _ => new ConsentStrings(
                // TRANSLATORS: consent heading of an OpenAI-compatible server the user set the address of.
                L10n.T("Send Mail to Your Server?"),
                common,
                L10n.T("They go to the server at the address you set. Over http they travel unencrypted."),
                checkbox),
        };
    }

    /// <summary>
    /// aiaccount.FailureOf: words a failed <c>account.test</c>,
    /// <c>account.add</c> or <c>account.update</c> of an AI provider account
    /// by the error's code (docs/api.md §2): a refused key, an unreachable or
    /// slow provider, an insecure connection (the TLS details are the trust
    /// offer's), a provider error. Anything else (invalidArgument,
    /// keyringError, a failure of the client, …) gets the general sentence.
    /// </summary>
    public static Failure FailureOf(Exception? error) => FailureOf(RpcErrorText.DaemonError(error));

    /// <summary><see cref="FailureOf(Exception?)"/> of the daemon's error itself (an endpoint's error of account.test).</summary>
    public static Failure FailureOf(RpcError? error)
    {
        if (error is null)
        {
            return new Failure();
        }
        var s = Texts();
        return error.Code.Value switch
        {
            ErrorCode.AuthFailed => new Failure(s.KeyRefused, Field.Key),
            ErrorCode.AuthRequired => new Failure(s.KeyMissing, Field.Key),
            ErrorCode.NetworkError or ErrorCode.Offline => new Failure(s.Unreachable, Field.Server),
            ErrorCode.ServerTimeout => new Failure(s.Timeout, Field.Server),
            ErrorCode.TlsError => new Failure(s.Insecure, Field.Server),
            ErrorCode.ServerError => new Failure(s.ServerError),
            ErrorCode.AssistantConsentRequired => new Failure(s.ConsentMissing, Field.Consent),
            _ => new Failure(),
        };
    }

    /// <summary>
    /// aiaccount.NormalizeBaseURL: the server address as the daemon stores
    /// it: http or https, a host, no user info, query or fragment, the host
    /// lower case, no trailing slash, at most 2048 bytes; null for anything
    /// else (an empty address, another scheme, a space or control character,
    /// no host).
    /// </summary>
    public static string? NormalizeBaseUrl(string? raw)
    {
        var s = (raw ?? "").Trim();
        if (s.Length == 0 || Encoding.UTF8.GetByteCount(s) > MaxBaseUrlBytes)
        {
            return null;
        }
        foreach (var r in s.EnumerateRunes())
        {
            if (Rune.IsWhiteSpace(r) || r.Value == '\\')
            {
                return null;
            }
            var cat = Rune.GetUnicodeCategory(r);
            if (cat is UnicodeCategory.Control or UnicodeCategory.Format)
            {
                return null;
            }
        }
        if (Parse(s) is not { } u)
        {
            return null;
        }
        if ((u.Scheme != "http" && u.Scheme != "https") || u.Opaque)
        {
            return null;
        }
        var (hostname, port) = UrlSyntax.SplitHostPort(u.Host);
        if (hostname.Length == 0)
        {
            return null;
        }
        var host = CodePoints.ToLower(hostname);
        if (host.Contains(':', StringComparison.Ordinal))
        {
            host = "[" + host + "]";
        }
        if (port.Length > 0)
        {
            host += ":" + port;
        }
        var output = u.Scheme + "://" + host + u.EscapedPath.TrimEnd('/');
        return Encoding.UTF8.GetByteCount(output) > MaxBaseUrlBytes ? null : output;
    }

    /// <summary>aiaccount.hostOf: the host of an address as typed; "" when it is none.</summary>
    public static string HostOf(string? raw) =>
        NormalizeBaseUrl(raw) is { } u && Parse(u) is { } p ? UrlSyntax.SplitHostPort(p.Host).Host : "";

    /// <summary>
    /// aiaccount.IsLoopback: a host that is this computer: localhost, a name
    /// under .localhost, or a loopback IP address (127.0.0.0/8, ::1).
    /// </summary>
    public static bool IsLoopback(string? host)
    {
        var h = CodePoints.ToLower(host ?? "");
        if (h.EndsWith('.'))
        {
            h = h[..^1];
        }
        if (h == "localhost" || h.EndsWith(".localhost", StringComparison.Ordinal))
        {
            return true;
        }
        h = h.Trim('[', ']');
        if (ParseIpv4(h) is { } v4)
        {
            return v4[0] == 127;
        }
        if (h.Contains(':', StringComparison.Ordinal) && !h.Contains('%', StringComparison.Ordinal)
            && IPAddress.TryParse(h, out var ip) && ip.AddressFamily == AddressFamily.InterNetworkV6)
        {
            if (ip.Equals(IPAddress.IPv6Loopback))
            {
                return true;
            }
            // An IPv4-mapped address (::ffff:127.0.0.1) is its IPv4.
            return ip.IsIPv4MappedToIPv6 && ip.MapToIPv4().GetAddressBytes()[0] == 127;
        }
        return false;
    }

    /// <summary>
    /// aiaccount.checkModel: a model id as the daemon checks it: not empty,
    /// at most 256 bytes, no control characters.
    /// </summary>
    public static ModelCheck CheckModel(string? raw)
    {
        var s = (raw ?? "").Trim();
        if (s.Length == 0)
        {
            return ModelCheck.Missing;
        }
        if (Encoding.UTF8.GetByteCount(s) > MaxModelBytes)
        {
            return ModelCheck.Invalid;
        }
        foreach (var r in s.EnumerateRunes())
        {
            if (Rune.GetUnicodeCategory(r) == UnicodeCategory.Control)
            {
                return ModelCheck.Invalid;
            }
        }
        // A lone surrogate is not valid UTF-8 (Go's utf8.ValidString).
        for (var i = 0; i < s.Length; i++)
        {
            if (char.IsSurrogate(s[i]) && !(char.IsHighSurrogate(s[i]) && i + 1 < s.Length && char.IsLowSurrogate(s[++i])))
            {
                return ModelCheck.Invalid;
            }
        }
        return ModelCheck.Ok;
    }

    // What NormalizeBaseUrl reads of Go's url.URL: the scheme, whether the URL
    // is opaque, URL.Host and URL.EscapedPath.
    private sealed record ParsedAddress(string Scheme, bool Opaque, string Host, string EscapedPath);

    // url.Parse of an address, null where Go reports an error.
    private static ParsedAddress? Parse(string raw)
    {
        if (UrlSyntax.Parse(raw) is not { } parsed)
        {
            return null;
        }
        if (!parsed.Rest.Opaque.IsEmpty)
        {
            return new ParsedAddress(parsed.Scheme, true, "", "");
        }
        var (u, _, _) = UrlSyntax.Cut(UrlSyntax.Bytes(raw), (byte)'#');
        var rest = UrlSyntax.Scheme(u) is { } s ? s.Remainder : u;
        return new ParsedAddress(parsed.Scheme, false, parsed.Rest.Host, EscapedPath(rest, parsed.Scheme));
    }

    // url.URL.EscapedPath of the part after the scheme: the path as typed when
    // it is a valid encoding of itself, else the decoded path escaped again as
    // Go's escape(path, encodePath) does.
    private static string EscapedPath(ReadOnlyMemory<byte> rest0, string scheme)
    {
        var r = rest0;
        var span = r.Span;
        if (span.Length > 0 && span[^1] == (byte)'?' && span.Count((byte)'?') == 1)
        {
            r = r[..^1];
        }
        else
        {
            r = UrlSyntax.Cut(r, (byte)'?').Before;
        }
        span = r.Span;
        if ((scheme.Length > 0 || !span.StartsWith("///"u8)) && span.StartsWith("//"u8))
        {
            var authority = r[2..];
            var slash = authority.Span.IndexOf((byte)'/');
            r = slash >= 0 ? authority[slash..] : ReadOnlyMemory<byte>.Empty;
        }
        if (UrlSyntax.Unescape(r.Span, UrlSyntax.Mode.Path) is not { } path)
        {
            return "";
        }
        return ValidEncodedPath(r.Span) ? Encoding.UTF8.GetString(r.Span) : EscapePath(path);
    }

    // url.shouldEscape(c, encodePath).
    private static bool ShouldEscapeInPath(byte c) => c switch
    {
        (>= (byte)'a' and <= (byte)'z') or (>= (byte)'A' and <= (byte)'Z') or (>= (byte)'0' and <= (byte)'9') => false,
        (byte)'-' or (byte)'_' or (byte)'.' or (byte)'~' => false,
        (byte)'$' or (byte)'&' or (byte)'+' or (byte)',' or (byte)'/' or (byte)':' or (byte)';' or (byte)'=' or (byte)'@' => false,
        _ => true,
    };

    // url.validEncoded(s, encodePath).
    private static bool ValidEncodedPath(ReadOnlySpan<byte> s)
    {
        foreach (var c in s)
        {
            switch (c)
            {
                case (byte)'!' or (byte)'$' or (byte)'&' or (byte)'\'' or (byte)'(' or (byte)')' or (byte)'*' or (byte)'+'
                    or (byte)',' or (byte)';' or (byte)'=' or (byte)':' or (byte)'@' or (byte)'[' or (byte)']' or (byte)'%':
                    continue;
                default:
                    if (ShouldEscapeInPath(c))
                    {
                        return false;
                    }
                    break;
            }
        }
        return true;
    }

    // url.escape(s, encodePath).
    private static string EscapePath(string s)
    {
        const string hex = "0123456789ABCDEF";
        var output = new StringBuilder();
        foreach (var c in Encoding.UTF8.GetBytes(s))
        {
            if (ShouldEscapeInPath(c))
            {
                output.Append('%').Append(hex[c >> 4]).Append(hex[c & 15]);
            }
            else
            {
                output.Append((char)c);
            }
        }
        return output.ToString();
    }

    // net.ParseIP's IPv4 form: four decimal fields of 0–255 without leading
    // zeros; null otherwise.
    private static byte[]? ParseIpv4(string s)
    {
        var parts = s.Split('.');
        if (parts.Length != 4)
        {
            return null;
        }
        var output = new byte[4];
        for (var i = 0; i < 4; i++)
        {
            var p = parts[i];
            if (p.Length is 0 or > 3 || (p.Length > 1 && p[0] == '0'))
            {
                return null;
            }
            var n = 0;
            foreach (var c in p)
            {
                if (c is < '0' or > '9')
                {
                    return null;
                }
                n = (n * 10) + (c - '0');
            }
            if (n > 255)
            {
                return null;
            }
            output[i] = (byte)n;
        }
        return output;
    }
}
