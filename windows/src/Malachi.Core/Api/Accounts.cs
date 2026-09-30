// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Accounts.swift; Go:
// backend/pkg/api/types.go ("Pagination", "System", "Accounts"); contract:
// docs/api.md §3, §4.0, §4.1.
//
// A member that Swift's initializer requires is `required` here, and
// missing from the JSON it fails the decoding as in Swift; one that Swift's
// initializer defaults but its decoding requires has the default and
// [JsonRequired]; an optional one is nullable and never written as null.

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.Page: which slice of a list to return. <see cref="Cursor"/> is opaque
/// and passed back unchanged; null means from the beginning.
/// <see cref="Limit"/> null is the server default (50), clamped to 500.
/// </summary>
public sealed record Page
{
    /// <summary>The opaque position a previous <see cref="PageInfo.NextCursor"/> named.</summary>
    [JsonPropertyName("cursor")]
    public string? Cursor { get; init; }

    /// <summary>How many entries; null is the server default.</summary>
    [JsonPropertyName("limit")]
    public int? Limit { get; init; }
}

/// <summary>
/// api.PageInfo: comes with every list. <see cref="NextCursor"/> is null on
/// the last page; <see cref="Total"/> is -1 when the daemon cannot cheaply
/// compute it.
/// </summary>
public sealed record PageInfo
{
    /// <summary>The cursor of the next page; null on the last one.</summary>
    [JsonPropertyName("nextCursor")]
    public string? NextCursor { get; init; }

    /// <summary>The number of entries, or -1.</summary>
    [JsonPropertyName("total")]
    public required int Total { get; init; }
}

/// <summary>api.SystemInfoResult: the answer of <c>system.info</c> (docs/api.md §4.0).</summary>
public sealed record SystemInfoResult
{
    /// <summary>The daemon's release version.</summary>
    [JsonPropertyName("version")]
    public required string Version { get; init; }

    /// <summary>The protocol version; the same as the handshake's.</summary>
    [JsonPropertyName("protocolVersion")]
    public required int ProtocolVersion { get; init; }

    /// <summary>The daemon's process id.</summary>
    [JsonPropertyName("pid")]
    public required int Pid { get; init; }

    /// <summary>The path of the daemon's store.</summary>
    [JsonPropertyName("storePath")]
    public required string StorePath { get; init; }
}

/// <summary>
/// api.ServerConfig: one endpoint (IMAP or SMTP). <see cref="CertificateSha256"/>
/// pins the server's certificate (64 lowercase hex digits of the SHA-256 of
/// its DER encoding): the endpoint then accepts exactly that certificate
/// instead of verifying the chain and the name. Not allowed with security
/// <c>none</c> or authMethod <c>oauth2</c>. It must survive every round trip
/// through the wizard, or an edit would silently drop it.
/// </summary>
public sealed record ServerConfig
{
    /// <summary>An IP literal or a host name.</summary>
    [JsonPropertyName("host")]
    public required string Host { get; init; }

    /// <summary>1–65535.</summary>
    [JsonPropertyName("port")]
    public required int Port { get; init; }

    /// <summary>tls, starttls or none.</summary>
    [JsonPropertyName("security")]
    public required Security Security { get; init; }

    /// <summary>The login name.</summary>
    [JsonPropertyName("username")]
    public required string Username { get; init; }

    /// <summary>password or oauth2.</summary>
    [JsonPropertyName("authMethod")]
    public required AuthMethod AuthMethod { get; init; }

    /// <summary>The pinned certificate's fingerprint, when there is one.</summary>
    [JsonPropertyName("certificateSha256")]
    public string? CertificateSha256 { get; init; }
}

/// <summary>
/// api.OAuth2Config: present when an endpoint uses <c>oauth2</c>, or on a
/// Graph account whose token comes from the daemon's own sign-in. With
/// <see cref="Source"/> goa only <see cref="Provider"/> and
/// <see cref="GoaAccountId"/> are set; with source daemon
/// <see cref="Provider"/> (google on IMAP, office365 on Graph) and optionally
/// <see cref="ClientId"/>/<see cref="TenantId"/>. Without a source (provider
/// custom) it is reserved and not implemented.
/// </summary>
public sealed record OAuth2Config
{
    /// <summary>Who holds the sign-in.</summary>
    [JsonPropertyName("source")]
    public OAuth2Source? Source { get; init; }

    /// <summary>The GNOME Online Accounts id, with source goa.</summary>
    [JsonPropertyName("goaAccountId")]
    public string? GoaAccountId { get; init; }

    /// <summary>google, office365 or custom.</summary>
    [JsonPropertyName("provider")]
    public required OAuth2Provider Provider { get; init; }

    /// <summary>An own OAuth client instead of the configured one.</summary>
    [JsonPropertyName("clientId")]
    public string? ClientId { get; init; }

    /// <summary>The Microsoft tenant; default common.</summary>
    [JsonPropertyName("tenantId")]
    public string? TenantId { get; init; }

    /// <summary>Reserved (provider custom).</summary>
    [JsonPropertyName("authUrl")]
    public string? AuthUrl { get; init; }

    /// <summary>Reserved (provider custom).</summary>
    [JsonPropertyName("tokenUrl")]
    public string? TokenUrl { get; init; }

    /// <summary>Reserved (provider custom).</summary>
    [JsonPropertyName("scopes")]
    public IReadOnlyList<string>? Scopes { get; init; }
}

/// <summary>
/// api.GraphConfig: present only for a <c>graph</c> account.
/// <see cref="GoaAccountId"/> only with source goa; a daemon account carries
/// an <c>oauth2</c> block too.
/// </summary>
public sealed record GraphConfig
{
    /// <summary>goa or daemon.</summary>
    [JsonPropertyName("source")]
    public required GraphSource Source { get; init; }

    /// <summary>The GNOME Online Accounts id, with source goa.</summary>
    [JsonPropertyName("goaAccountId")]
    public string? GoaAccountId { get; init; }
}

/// <summary>
/// api.AccountConfig: the non-secret part of an account. Secrets travel only
/// in <see cref="Credentials"/> at add/test time and are never returned by
/// any method. <see cref="Imap"/> and <see cref="Smtp"/> are set for an IMAP
/// account and absent for Graph; <see cref="Graph"/> the other way round.
/// </summary>
public sealed record AccountConfig
{
    /// <summary>Display name of the account.</summary>
    [JsonPropertyName("name")]
    public required string Name { get; init; }

    /// <summary>Primary address.</summary>
    [JsonPropertyName("email")]
    public required string Email { get; init; }

    /// <summary>The sender's name.</summary>
    [JsonPropertyName("displayName")]
    public string? DisplayName { get; init; }

    /// <summary>Null means <c>imap</c>; see <see cref="ProtocolKind"/>.</summary>
    [JsonPropertyName("kind")]
    public AccountKind? Kind { get; init; }

    /// <summary>The IMAP endpoint of an IMAP account.</summary>
    [JsonPropertyName("imap")]
    public ServerConfig? Imap { get; init; }

    /// <summary>The SMTP endpoint of an IMAP account.</summary>
    [JsonPropertyName("smtp")]
    public ServerConfig? Smtp { get; init; }

    /// <summary>The OAuth2 sign-in, when an endpoint or the Graph account uses one.</summary>
    [JsonPropertyName("oauth2")]
    public OAuth2Config? OAuth2 { get; init; }

    /// <summary>The token source of a Graph account.</summary>
    [JsonPropertyName("graph")]
    public GraphConfig? Graph { get; init; }

    /// <summary>
    /// The site of a Jira account (kind <c>jira</c> only); its
    /// <see cref="Email"/> is the user's address (the cloud login) and its
    /// token is <see cref="Credentials.Password"/>.
    /// </summary>
    [JsonPropertyName("jira")]
    public JiraConfig? Jira { get; init; }

    /// <summary>Null is the daemon's default.</summary>
    [JsonPropertyName("syncIntervalSeconds")]
    public int? SyncIntervalSeconds { get; init; }

    /// <summary>api.AccountConfig.Protocol: <see cref="Kind"/> with the empty default resolved.</summary>
    [JsonIgnore]
    public AccountKind ProtocolKind => Kind ?? AccountKind.Imap;
}

/// <summary>
/// api.Credentials: secrets for <c>account.add</c>, <c>account.update</c>
/// and <c>account.test</c>. Write-only: a password for password endpoints,
/// the id of a completed <c>account.oauthStart</c> session for a daemon
/// sign-in (its tokens never leave the daemon), nothing for a GNOME Online
/// Accounts account. Printing one (a log line, an assertion, a debugger)
/// shows whether a field is set, never its value.
/// </summary>
[DebuggerDisplay("{ToString(),nq}")]
public sealed record Credentials
{
    /// <summary>The password of password endpoints.</summary>
    [JsonPropertyName("password")]
    public string? Password { get; init; }

    /// <summary>
    /// A completed <c>account.oauthStart</c> session. Consumed by
    /// <c>account.add</c> / <c>account.update</c>; <c>account.test</c> only
    /// reads it.
    /// </summary>
    [JsonPropertyName("oauthSession")]
    public string? OAuthSession { get; init; }

    /// <summary>Whether each field is set; never a value.</summary>
    public override string ToString() =>
        $"Credentials(password: {(Password is null ? "nil" : "<redacted>")}, oauthSession: {(OAuthSession is null ? "nil" : "<set>")})";
}

/// <summary>api.Account: what <c>account.list</c> returns, config plus derived state.</summary>
public sealed record Account
{
    /// <summary>The account.</summary>
    [JsonPropertyName("id")]
    public required AccountId Id { get; init; }

    /// <summary>Its configuration.</summary>
    [JsonPropertyName("config")]
    public required AccountConfig Config { get; init; }

    /// <summary>False while paused.</summary>
    [JsonPropertyName("enabled")]
    public required bool Enabled { get; init; }

    /// <summary>The live state of its syncer.</summary>
    [JsonPropertyName("state")]
    public required SyncState State { get; init; }

    /// <summary>
    /// What the account can do. Null (a daemon that predates it) is
    /// <see cref="API.MailCapabilities"/>, which is why it is not read as an
    /// empty list: an empty list (a Jira account) can do none of them. Ask
    /// <see cref="Can"/>.
    /// </summary>
    [JsonPropertyName("capabilities")]
    public IReadOnlyList<Capability>? Capabilities { get; init; }

    /// <summary>
    /// api.Account.Can: whether the account has the capability, null
    /// <see cref="Capabilities"/> read as <see cref="API.MailCapabilities"/>.
    /// </summary>
    public bool Can(Capability capability)
    {
        foreach (var have in Capabilities ?? API.MailCapabilities)
        {
            if (have == capability)
            {
                return true;
            }
        }
        return false;
    }
}

/// <summary>api.AccountListResult.</summary>
public sealed record AccountListResult
{
    /// <summary>The accounts in display order.</summary>
    [JsonPropertyName("accounts")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Account>))]
    public IReadOnlyList<Account> Accounts { get; init => field = value ?? []; } = [];
}

/// <summary>api.AccountAddParams.</summary>
public sealed record AccountAddParams
{
    /// <summary>The account to add.</summary>
    [JsonPropertyName("config")]
    public required AccountConfig Config { get; init; }

    /// <summary>Its secrets; sent as <c>{}</c> when there are none.</summary>
    [JsonPropertyName("credentials")]
    [JsonRequired]
    public Credentials Credentials { get; init; } = new();
}

/// <summary>api.AccountAddResult.</summary>
public sealed record AccountAddResult
{
    /// <summary>The new account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }
}

/// <summary>
/// api.AccountRemoveParams. <see cref="DeleteLocalData"/> also purges the
/// account's drafts and attachments; the mail cache always goes.
/// </summary>
public sealed record AccountRemoveParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Also delete the drafts and attachments; always sent.</summary>
    [JsonPropertyName("deleteLocalData")]
    public required bool DeleteLocalData { get; init; }
}

/// <summary>api.AccountSetEnabledParams: pause (false) or resume (true) an account.</summary>
public sealed record AccountSetEnabledParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Resume (true) or pause (false).</summary>
    [JsonPropertyName("enabled")]
    public required bool Enabled { get; init; }
}

/// <summary>
/// api.AccountReorderParams: the listed accounts take the head in this
/// order; the rest keep their relative order behind them.
/// </summary>
public sealed record AccountReorderParams
{
    /// <summary>The new head of the order; always sent.</summary>
    [JsonPropertyName("accountIds")]
    public required IReadOnlyList<AccountId> AccountIds { get; init; }
}

/// <summary>
/// api.AccountDiscoverParams: only the domain leaves the machine for ISPDB
/// and DNS; the provider's own autoconfig URL receives the address.
/// </summary>
public sealed record AccountDiscoverParams
{
    /// <summary>A bare address.</summary>
    [JsonPropertyName("email")]
    public required string Email { get; init; }
}

/// <summary>
/// api.AccountDiscoverResult: a suggestion only; nothing is stored or
/// authenticated. <see cref="Config"/> is absent for source <c>none</c>. With
/// <c>provider</c> it is the primary way to add the address: the GNOME
/// Online Accounts hint (without <c>goaAccountId</c>, does not pass
/// <c>account.add</c>) where GNOME Online Accounts runs, the daemon's own
/// sign-in (source daemon) elsewhere. <see cref="Alternatives"/> are further
/// ways, in the daemon's order of preference: the own sign-in when it is not
/// <see cref="Config"/>, and for Google an IMAP/SMTP account with an app
/// password.
/// </summary>
public sealed record AccountDiscoverResult
{
    /// <summary>The suggested account; null when nothing was found.</summary>
    [JsonPropertyName("config")]
    public AccountConfig? Config { get; init; }

    /// <summary>Where the suggestion came from.</summary>
    [JsonPropertyName("source")]
    public required DiscoverSource Source { get; init; }

    /// <summary>Display-only, untrusted text.</summary>
    [JsonPropertyName("providerName")]
    public string? ProviderName { get; init; }

    /// <summary>Further ways to add the address; absent reads as empty.</summary>
    [JsonPropertyName("alternatives")]
    [JsonConverter(typeof(NullAsEmptyListConverter<AccountConfig>))]
    public IReadOnlyList<AccountConfig> Alternatives { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.LinkedAccount: an account another desktop service is signed in to.
/// <see cref="Name"/> and <see cref="Email"/> are untrusted text from the
/// service.
/// </summary>
public sealed record LinkedAccount
{
    /// <summary>microsoft365 or google.</summary>
    [JsonPropertyName("provider")]
    public required LinkedProvider Provider { get; init; }

    /// <summary>A bare valid address.</summary>
    [JsonPropertyName("email")]
    public required string Email { get; init; }

    /// <summary>The display name the service knows.</summary>
    [JsonPropertyName("name")]
    public string? Name { get; init; }

    /// <summary>The GNOME Online Accounts id.</summary>
    [JsonPropertyName("goaAccountId")]
    public required string GoaAccountId { get; init; }

    /// <summary>A Malachi account with that address exists already.</summary>
    [JsonPropertyName("configured")]
    public required bool Configured { get; init; }

    /// <summary>The service wants the user to sign in again.</summary>
    [JsonPropertyName("attentionNeeded")]
    public required bool AttentionNeeded { get; init; }

    /// <summary>The account to add, complete; passes <c>account.add</c> without credentials.</summary>
    [JsonPropertyName("config")]
    public AccountConfig? Config { get; init; }
}

/// <summary>
/// api.AccountLinkedResult. Empty, not an error, without GNOME Online
/// Accounts (always on Windows).
/// </summary>
public sealed record AccountLinkedResult
{
    /// <summary>The accounts the service offers.</summary>
    [JsonPropertyName("accounts")]
    [JsonConverter(typeof(NullAsEmptyListConverter<LinkedAccount>))]
    public IReadOnlyList<LinkedAccount> Accounts { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.OAuthBrowserPage: the texts of the page the browser shows after the
/// provider redirects back to the daemon, in the user's language. Plain
/// text, at most 200 characters each, escaped by the daemon.
/// </summary>
public sealed record OAuthBrowserPage
{
    /// <summary>The title after a sign-in.</summary>
    [JsonPropertyName("successTitle")]
    public string? SuccessTitle { get; init; }

    /// <summary>The sentence after a sign-in.</summary>
    [JsonPropertyName("successText")]
    public string? SuccessText { get; init; }

    /// <summary>The title after a failure.</summary>
    [JsonPropertyName("failureTitle")]
    public string? FailureTitle { get; init; }

    /// <summary>The sentence after a failure.</summary>
    [JsonPropertyName("failureText")]
    public string? FailureText { get; init; }
}

/// <summary>
/// api.AccountOAuthStartParams: the daemon's own sign-in for a new account
/// (<see cref="Config"/>, source daemon) or to sign an existing daemon
/// account in again (<see cref="AccountId"/>); exactly one is set.
/// </summary>
public sealed record AccountOAuthStartParams
{
    /// <summary>The account to sign in again.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>The new account to sign in.</summary>
    [JsonPropertyName("config")]
    public AccountConfig? Config { get; init; }

    /// <summary>The texts of the page the browser shows at the end.</summary>
    [JsonPropertyName("browserPage")]
    public OAuthBrowserPage? BrowserPage { get; init; }
}

/// <summary>
/// api.AccountOAuthStartResult: the UI opens <see cref="AuthUrl"/> in the
/// browser; the daemon listens for the redirect on 127.0.0.1 until
/// <see cref="ExpiresAt"/>.
/// </summary>
public sealed record AccountOAuthStartResult
{
    /// <summary>The session to wait for.</summary>
    [JsonPropertyName("sessionId")]
    public required string SessionId { get; init; }

    /// <summary>The provider's authorisation URL.</summary>
    [JsonPropertyName("authUrl")]
    public required string AuthUrl { get; init; }

    /// <summary>When the session ends (10 minutes).</summary>
    [JsonPropertyName("expiresAt")]
    public required DateTimeOffset ExpiresAt { get; init; }
}

/// <summary>api.AccountOAuthWaitParams.</summary>
public sealed record AccountOAuthWaitParams
{
    /// <summary>The session.</summary>
    [JsonPropertyName("sessionId")]
    public required string SessionId { get; init; }
}

/// <summary>
/// api.AccountOAuthWaitResult: with <c>complete</c>, <see cref="Config"/> is
/// the account to pass to account.test / account.add with
/// <c>credentials.oauthSession</c> (for a re-sign-in, the account's own).
/// </summary>
public sealed record AccountOAuthWaitResult
{
    /// <summary>pending or complete.</summary>
    [JsonPropertyName("status")]
    public required OAuthSessionStatus Status { get; init; }

    /// <summary>The signed-in account, with complete.</summary>
    [JsonPropertyName("config")]
    public AccountConfig? Config { get; init; }
}

/// <summary>api.AccountOAuthCancelParams.</summary>
public sealed record AccountOAuthCancelParams
{
    /// <summary>The session.</summary>
    [JsonPropertyName("sessionId")]
    public required string SessionId { get; init; }
}

/// <summary>
/// api.AccountUpdateParams: replaces the configuration; an empty password
/// keeps the stored one; <c>enabled</c> is not touched.
/// </summary>
public sealed record AccountUpdateParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Its whole new configuration.</summary>
    [JsonPropertyName("config")]
    public required AccountConfig Config { get; init; }

    /// <summary>Its secrets; sent as <c>{}</c> when there are none.</summary>
    [JsonPropertyName("credentials")]
    [JsonRequired]
    public Credentials Credentials { get; init; } = new();
}

/// <summary>
/// api.AccountTestParams: with <see cref="AccountId"/> and no password, the
/// stored password of that account is used; for a daemon sign-in the token
/// of <c>credentials.oauthSession</c> (read, not consumed) or, with
/// <see cref="AccountId"/>, the stored sign-in.
/// </summary>
public sealed record AccountTestParams
{
    /// <summary>The existing account being tested, if any.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>The configuration to test.</summary>
    [JsonPropertyName("config")]
    public required AccountConfig Config { get; init; }

    /// <summary>Its secrets; sent as <c>{}</c> when there are none.</summary>
    [JsonPropertyName("credentials")]
    [JsonRequired]
    public Credentials Credentials { get; init; } = new();
}

/// <summary>api.EndpointTestResult: one endpoint's outcome; <see cref="Error"/> set on failure.</summary>
public sealed record EndpointTestResult
{
    /// <summary>Whether the endpoint works.</summary>
    [JsonPropertyName("ok")]
    public required bool Ok { get; init; }

    /// <summary>Why it does not.</summary>
    [JsonPropertyName("error")]
    public RpcError? Error { get; init; }

    /// <summary>The server's capabilities or EHLO keywords, scrubbed.</summary>
    [JsonPropertyName("capabilities")]
    public IReadOnlyList<string>? Capabilities { get; init; }

    /// <summary>Dial to ready, in milliseconds.</summary>
    [JsonPropertyName("latencyMs")]
    public required int LatencyMs { get; init; }
}

/// <summary>
/// api.AccountTestResult: <see cref="Imap"/> and <see cref="Smtp"/> for an
/// IMAP account, <see cref="Graph"/> for a Graph account, <see cref="Jira"/>
/// for a Jira account (its capabilities carry <c>cloud</c> or
/// <c>datacenter</c>, and <c>gateway</c> when the gateway route was used).
/// </summary>
public sealed record AccountTestResult
{
    /// <summary>The IMAP endpoint's outcome.</summary>
    [JsonPropertyName("imap")]
    public EndpointTestResult? Imap { get; init; }

    /// <summary>The SMTP endpoint's outcome.</summary>
    [JsonPropertyName("smtp")]
    public EndpointTestResult? Smtp { get; init; }

    /// <summary>The Graph mailbox's outcome.</summary>
    [JsonPropertyName("graph")]
    public EndpointTestResult? Graph { get; init; }

    /// <summary>The Jira site's outcome.</summary>
    [JsonPropertyName("jira")]
    public EndpointTestResult? Jira { get; init; }
}
