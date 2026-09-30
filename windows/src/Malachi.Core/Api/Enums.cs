// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Enums.swift; Go:
// backend/pkg/api/types.go (the string types and their constants).
//
// The string enumerations of backend/pkg/api/types.go as extensible value
// types: a value a newer daemon adds decodes as itself instead of failing
// the whole result (docs/api.md §6: clients ignore what they do not know).
// The named constants are the values of the contract (protocol version 2).
// They are const strings, so that a switch over Value can name them as
// constant patterns (case FolderRole.Inbox:), and a string converts to the
// type implicitly (Swift's string literals), so that
// role == FolderRole.Inbox compares two FolderRoles.

using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>A wire enum (Swift <c>WireEnum</c>): a known set of string values that may grow.</summary>
public interface IWireEnumeration<TSelf> : IStringWireValue<TSelf>
    where TSelf : struct, IWireEnumeration<TSelf>;

/// <summary>api.Security: transport security of an IMAP/SMTP endpoint.</summary>
[JsonConverter(typeof(StringWireValueConverter<Security>))]
public readonly record struct Security(string Value) : IWireEnumeration<Security>
{
    /// <summary>Implicit TLS (IMAPS 993 / SMTPS 465).</summary>
    public const string Tls = "tls";

    /// <summary>Upgrade on a plain port (143 / 587).</summary>
    public const string Starttls = "starttls";

    /// <summary>Plaintext; the daemon accepts it only for localhost.</summary>
    public const string None = "none";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator Security(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.AuthMethod: how the daemon signs in to an endpoint.</summary>
[JsonConverter(typeof(StringWireValueConverter<AuthMethod>))]
public readonly record struct AuthMethod(string Value) : IWireEnumeration<AuthMethod>
{
    /// <summary>PLAIN / LOGIN with the stored password.</summary>
    public const string Password = "password";

    /// <summary>SASL XOAUTH2 (OAUTHBEARER as fallback) with an access token.</summary>
    public const string OAuth2 = "oauth2";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator AuthMethod(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.OAuth2Source: who holds the sign-in of an oauth2 endpoint.</summary>
[JsonConverter(typeof(StringWireValueConverter<OAuth2Source>))]
public readonly record struct OAuth2Source(string Value) : IWireEnumeration<OAuth2Source>
{
    /// <summary>GNOME Online Accounts; not available on Windows.</summary>
    public const string Goa = "goa";

    /// <summary>
    /// The daemon's own sign-in (authorization code with PKCE,
    /// <c>account.oauthStart</c>); the refresh token is in the keyring.
    /// </summary>
    public const string Daemon = "daemon";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator OAuth2Source(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.OAuth2Provider* constants: whose OAuth2 account an endpoint uses.</summary>
[JsonConverter(typeof(StringWireValueConverter<OAuth2Provider>))]
public readonly record struct OAuth2Provider(string Value) : IWireEnumeration<OAuth2Provider>
{
    /// <summary>IMAP accounts, with source goa or daemon.</summary>
    public const string Google = "google";

    /// <summary>Graph accounts with source daemon.</summary>
    public const string Office365 = "office365";

    /// <summary>Reserved: any other authorisation server; not implemented.</summary>
    public const string Custom = "custom";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator OAuth2Provider(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.AccountKind: the protocol behind an account.</summary>
[JsonConverter(typeof(StringWireValueConverter<AccountKind>))]
public readonly record struct AccountKind(string Value) : IWireEnumeration<AccountKind>
{
    /// <summary>IMAP for the mailbox, SMTP for sending. The default when absent.</summary>
    public const string Imap = "imap";

    /// <summary>Microsoft 365 / Outlook.com through the Graph API.</summary>
    public const string Graph = "graph";

    /// <summary>
    /// An issue tracker (Jira Cloud or Data Center): issues and their
    /// comments as messages, spaces as folders; the account's <c>jira</c> block.
    /// </summary>
    public const string Jira = "jira";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator AccountKind(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.GraphSource: who holds the OAuth2 session of a Graph account.</summary>
[JsonConverter(typeof(StringWireValueConverter<GraphSource>))]
public readonly record struct GraphSource(string Value) : IWireEnumeration<GraphSource>
{
    /// <summary>GNOME Online Accounts owns the sign-in.</summary>
    public const string Goa = "goa";

    /// <summary>
    /// The daemon's own sign-in; the account's <c>oauth2</c> block says
    /// <c>{source: daemon, provider: office365}</c>.
    /// </summary>
    public const string Daemon = "daemon";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator GraphSource(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.DiscoverSource: where an <c>account.discover</c> suggestion came
/// from, from most to least trustworthy.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<DiscoverSource>))]
public readonly record struct DiscoverSource(string Value) : IWireEnumeration<DiscoverSource>
{
    /// <summary>The address is signed in through GNOME Online Accounts.</summary>
    public const string Goa = "goa";

    /// <summary>Mozilla's autoconfig database.</summary>
    public const string Ispdb = "ispdb";

    /// <summary>The provider's own autoconfig document.</summary>
    public const string Autoconfig = "autoconfig";

    /// <summary>RFC 6186 DNS SRV records.</summary>
    public const string Srv = "srv";

    /// <summary>
    /// A known provider that signs in with OAuth2 (GNOME Online Accounts or
    /// the daemon's own sign-in).
    /// </summary>
    public const string Provider = "provider";

    /// <summary>Common host names verified by a TLS connection.</summary>
    public const string Guess = "guess";

    /// <summary>Nothing found; <c>config</c> is absent.</summary>
    public const string None = "none";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator DiscoverSource(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.LinkedAccount.provider: the desktop service's account type.</summary>
[JsonConverter(typeof(StringWireValueConverter<LinkedProvider>))]
public readonly record struct LinkedProvider(string Value) : IWireEnumeration<LinkedProvider>
{
    /// <summary>A Microsoft 365 account.</summary>
    public const string Microsoft365 = "microsoft365";

    /// <summary>A Google account.</summary>
    public const string Google = "google";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator LinkedProvider(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.OAuthSessionStatus: what <c>account.oauthWait</c> reports of a sign-in.</summary>
[JsonConverter(typeof(StringWireValueConverter<OAuthSessionStatus>))]
public readonly record struct OAuthSessionStatus(string Value) : IWireEnumeration<OAuthSessionStatus>
{
    /// <summary>The browser has not come back yet; call again.</summary>
    public const string Pending = "pending";

    /// <summary>Signed in; the result carries the config.</summary>
    public const string Complete = "complete";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator OAuthSessionStatus(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.FolderRole: the special-use classification of a folder.</summary>
[JsonConverter(typeof(StringWireValueConverter<FolderRole>))]
public readonly record struct FolderRole(string Value) : IWireEnumeration<FolderRole>
{
    /// <summary>An ordinary folder.</summary>
    public const string None = "none";

    /// <summary>The inbox.</summary>
    public const string Inbox = "inbox";

    /// <summary>Sent mail.</summary>
    public const string Sent = "sent";

    /// <summary>Drafts.</summary>
    public const string Drafts = "drafts";

    /// <summary>Trash.</summary>
    public const string Trash = "trash";

    /// <summary>Junk (spam).</summary>
    public const string Junk = "junk";

    /// <summary>The archive.</summary>
    public const string Archive = "archive";

    /// <summary>A server's <c>\All</c> folder.</summary>
    public const string All = "all";

    /// <summary>The local-only queue of messages to send.</summary>
    public const string Outbox = "outbox";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator FolderRole(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.Flag: a message flag; the daemon maps them to IMAP flags.</summary>
[JsonConverter(typeof(StringWireValueConverter<Flag>))]
public readonly record struct Flag(string Value) : IWireEnumeration<Flag>
{
    /// <summary>Read.</summary>
    public const string Seen = "seen";

    /// <summary>Replied to.</summary>
    public const string Answered = "answered";

    /// <summary>Starred.</summary>
    public const string Flagged = "flagged";

    /// <summary>A draft.</summary>
    public const string Draft = "draft";

    /// <summary>Marked for deletion (only through <c>message.delete</c>).</summary>
    public const string Deleted = "deleted";

    /// <summary>Junk.</summary>
    public const string Junk = "junk";

    /// <summary>Forwarded.</summary>
    public const string Forwarded = "forwarded";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator Flag(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.OutboxState: the delivery state of a queued message.</summary>
[JsonConverter(typeof(StringWireValueConverter<OutboxState>))]
public readonly record struct OutboxState(string Value) : IWireEnumeration<OutboxState>
{
    /// <summary>Waiting for the next attempt.</summary>
    public const string Queued = "queued";

    /// <summary>An SMTP session is running.</summary>
    public const string Sending = "sending";

    /// <summary>Delivered; the Sent copy is pending.</summary>
    public const string Sent = "sent";

    /// <summary>Permanent failure; <c>outbox.retry</c> re-queues it.</summary>
    public const string Failed = "failed";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator OutboxState(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.SortOrder for message and thread lists.</summary>
[JsonConverter(typeof(StringWireValueConverter<SortOrder>))]
public readonly record struct SortOrder(string Value) : IWireEnumeration<SortOrder>
{
    /// <summary>Newest first (the default).</summary>
    public const string DateDesc = "dateDesc";

    /// <summary>Oldest first.</summary>
    public const string DateAsc = "dateAsc";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator SortOrder(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.MessageFilter: the subset of a folder a listing shows.</summary>
[JsonConverter(typeof(StringWireValueConverter<MessageFilter>))]
public readonly record struct MessageFilter(string Value) : IWireEnumeration<MessageFilter>
{
    /// <summary>Every message (the default).</summary>
    public const string All = "all";

    /// <summary>Without the <c>seen</c> flag.</summary>
    public const string Unread = "unread";

    /// <summary>With the <c>flagged</c> flag.</summary>
    public const string Flagged = "flagged";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator MessageFilter(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.RemoteContentPolicy: how the daemon treats remote references when
/// producing a body. <c>knownSenders</c> is valid only as the stored
/// preference; a result reports <c>block</c> or <c>allow</c>, never
/// <c>knownSenders</c>.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<RemoteContentPolicy>))]
public readonly record struct RemoteContentPolicy(string Value) : IWireEnumeration<RemoteContentPolicy>
{
    /// <summary>Every remote reference stripped. The default.</summary>
    public const string Block = "block";

    /// <summary><c>https:</c> images fetched by the daemon and inlined, after consent.</summary>
    public const string Allow = "allow";

    /// <summary>Resolved per message to <c>allow</c> when every sender is a known sender.</summary>
    public const string KnownSenders = "knownSenders";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RemoteContentPolicy(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.BodyState: whether the daemon holds the message content.</summary>
[JsonConverter(typeof(StringWireValueConverter<BodyState>))]
public readonly record struct BodyState(string Value) : IWireEnumeration<BodyState>
{
    /// <summary>Text and HTML available.</summary>
    public const string Fetched = "fetched";

    /// <summary>The sync engine has not downloaded the body yet.</summary>
    public const string Pending = "pending";

    /// <summary>Over the daemon's raw-message cap; never downloaded.</summary>
    public const string TooBig = "tooBig";

    /// <summary>Downloaded but unparsable; nothing shown.</summary>
    public const string Failed = "failed";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator BodyState(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.ComposeMode: how <c>draft.create</c> pre-fills a draft.</summary>
[JsonConverter(typeof(StringWireValueConverter<ComposeMode>))]
public readonly record struct ComposeMode(string Value) : IWireEnumeration<ComposeMode>
{
    /// <summary>An empty draft, or a parsed <c>mailto:</c>.</summary>
    public const string New = "new";

    /// <summary>A reply to the sender.</summary>
    public const string Reply = "reply";

    /// <summary>A reply to the sender and the other recipients.</summary>
    public const string ReplyAll = "replyAll";

    /// <summary>A forward.</summary>
    public const string Forward = "forward";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator ComposeMode(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.QuoteForm: how much of the original a <c>draft.create</c> result quotes.</summary>
[JsonConverter(typeof(StringWireValueConverter<QuoteForm>))]
public readonly record struct QuoteForm(string Value) : IWireEnumeration<QuoteForm>
{
    /// <summary>The sanitised HTML of the original, with its pictures.</summary>
    public const string Html = "html";

    /// <summary>The original's text.</summary>
    public const string Text = "text";

    /// <summary>Nothing: the body is not downloaded, or the mode is <c>new</c>.</summary>
    public const string None = "none";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator QuoteForm(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.SyncStatus: the coarse state of one account.</summary>
[JsonConverter(typeof(StringWireValueConverter<SyncStatus>))]
public readonly record struct SyncStatus(string Value) : IWireEnumeration<SyncStatus>
{
    /// <summary>Connected or between passes, no work.</summary>
    public const string Idle = "idle";

    /// <summary>A pass is running.</summary>
    public const string Syncing = "syncing";

    /// <summary>The last attempt failed for a network or TLS reason; retrying.</summary>
    public const string Offline = "offline";

    /// <summary>Missing or refused credentials.</summary>
    public const string AuthRequired = "authRequired";

    /// <summary>A server or storage error.</summary>
    public const string Error = "error";

    /// <summary>Paused, or no syncer.</summary>
    public const string Disabled = "disabled";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator SyncStatus(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.KnownSenderSource* constants: why an address is a known sender.</summary>
[JsonConverter(typeof(StringWireValueConverter<KnownSenderSource>))]
public readonly record struct KnownSenderSource(string Value) : IWireEnumeration<KnownSenderSource>
{
    /// <summary>A recipient of mail the user sent.</summary>
    public const string Sent = "sent";

    /// <summary>An explicit decision of the user.</summary>
    public const string User = "user";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator KnownSenderSource(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.ContactSource: where a recipient suggestion came from.</summary>
[JsonConverter(typeof(StringWireValueConverter<ContactSource>))]
public readonly record struct ContactSource(string Value) : IWireEnumeration<ContactSource>
{
    /// <summary>A recipient of mail the user sent; never an incoming <c>From</c>.</summary>
    public const string Sent = "sent";

    /// <summary>A system address book, read only.</summary>
    public const string AddressBook = "addressBook";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator ContactSource(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.AccountCapability: what the user can do with an account and its
/// messages (<see cref="Account.Capabilities"/>). Flags (seen, flagged) are
/// always allowed; archive and junk also need the role folder.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<Capability>))]
public readonly record struct Capability(string Value) : IWireEnumeration<Capability>
{
    /// <summary>Can be the From of a new message and the account a forward goes out of.</summary>
    public const string Compose = "compose";

    /// <summary>Its messages can be answered by e-mail.</summary>
    public const string Reply = "reply";

    /// <summary>… to all recipients.</summary>
    public const string ReplyAll = "replyAll";

    /// <summary>
    /// Its messages can be forwarded; a jira message through a compose
    /// account (<see cref="DraftCreateParams.MessageAccountId"/>).
    /// </summary>
    public const string Forward = "forward";

    /// <summary>Reply creates a comment draft; the UI calls Reply "Comment".</summary>
    public const string Comment = "comment";

    /// <summary><c>message.move</c>.</summary>
    public const string Move = "move";

    /// <summary><c>message.delete</c>.</summary>
    public const string Delete = "delete";

    /// <summary>
    /// The status of an issue can be changed (<c>issue.transitions</c>,
    /// <c>issue.transition</c>): the Change Status menu.
    /// </summary>
    public const string Transition = "transition";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator Capability(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.JiraDeployment: where a Jira site runs.</summary>
[JsonConverter(typeof(StringWireValueConverter<JiraDeployment>))]
public readonly record struct JiraDeployment(string Value) : IWireEnumeration<JiraDeployment>
{
    /// <summary>Atlassian's cloud: REST v3, ADF, e-mail + API token (Basic).</summary>
    public const string Cloud = "cloud";

    /// <summary>Self-hosted Data Center: REST v2, wiki markup, personal access token (Bearer).</summary>
    public const string Datacenter = "datacenter";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator JiraDeployment(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.VirtualFolder: a folder of a jira account computed over its spaces
/// (<see cref="Folder.Virtual"/>); its role stays <c>none</c> and its name
/// is an English fallback the UI replaces by the code.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<VirtualFolder>))]
public readonly record struct VirtualFolder(string Value) : IWireEnumeration<VirtualFolder>
{
    /// <summary>Issues assigned to the user.</summary>
    public const string AssignedToMe = "assignedToMe";

    /// <summary>Issues the user watches.</summary>
    public const string Watching = "watching";

    /// <summary>Issues not in a closed status (<see cref="JiraConfig.ClosedStatuses"/>).</summary>
    public const string Open = "open";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator VirtualFolder(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.NotificationMailMode: what a Jira notification mail in a mail
/// account does. Absent (or empty) is <see cref="Sync"/>.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<NotificationMailMode>))]
public readonly record struct NotificationMailMode(string Value) : IWireEnumeration<NotificationMailMode>
{
    /// <summary>The mail syncs its issue at once.</summary>
    public const string Sync = "sync";

    /// <summary><see cref="Sync"/>, and the mail is hidden in the mail account (a display filter).</summary>
    public const string Hide = "hide";

    /// <summary>Nothing.</summary>
    public const string Ignore = "ignore";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator NotificationMailMode(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.IssueStatusCategory: the coarse class of an issue status. Empty
/// when unknown; a client treats a value it does not know as empty.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<IssueStatusCategory>))]
public readonly record struct IssueStatusCategory(string Value) : IWireEnumeration<IssueStatusCategory>
{
    /// <summary>To do.</summary>
    public const string Todo = "todo";

    /// <summary>In progress.</summary>
    public const string InProgress = "inProgress";

    /// <summary>Done.</summary>
    public const string Done = "done";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator IssueStatusCategory(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.IssueItemKind: what part of an issue a message of a jira account is.</summary>
[JsonConverter(typeof(StringWireValueConverter<IssueItemKind>))]
public readonly record struct IssueItemKind(string Value) : IWireEnumeration<IssueItemKind>
{
    /// <summary>The issue itself, its description as the body.</summary>
    public const string Description = "description";

    /// <summary>A comment.</summary>
    public const string Comment = "comment";

    /// <summary>
    /// A status or assignee change (<see cref="MessageIssue.Changes"/>);
    /// stored seen, never notified, its body language-neutral.
    /// </summary>
    public const string Event = "event";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator IssueItemKind(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.CommentVisibility: who sees a comment of a service-desk request.</summary>
[JsonConverter(typeof(StringWireValueConverter<CommentVisibility>))]
public readonly record struct CommentVisibility(string Value) : IWireEnumeration<CommentVisibility>
{
    /// <summary>A reply the customer sees.</summary>
    public const string Public = "public";

    /// <summary>An internal note for the agents only.</summary>
    public const string Internal = "internal";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator CommentVisibility(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.IssueField: the field an event row changed; a client skips a change
/// of a field it does not know.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<IssueField>))]
public readonly record struct IssueField(string Value) : IWireEnumeration<IssueField>
{
    /// <summary>The status.</summary>
    public const string Status = "status";

    /// <summary>The assignee.</summary>
    public const string Assignee = "assignee";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator IssueField(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}
