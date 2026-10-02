// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/API.swift (and EmptyParams of
// macos/Sources/MalachiCore/Transport/JSONRPC.swift); Go:
// backend/pkg/api/methods.go (Method*, Notify*, AllMethods,
// AllNotifications), backend/pkg/api/types.go (the limits); contract:
// docs/api.md §4, §5. The board methods (board.go, §4.13) have no Swift
// descriptors yet.
//
// backend/pkg/api re-declared in C#: the method table of methods.go with the
// params and result type of each, the notification names, the limits. The
// contract is docs/api.md; the numbers and names here must follow it.
//
// The table is the static class API, the Swift name kept: a class named Api
// would sit in the namespace Malachi.Core.Api, and every other Malachi.Core.*
// namespace would read Api.MessageList as that namespace, not the class.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json.Serialization.Metadata;
using Malachi.Core.Platform;

namespace Malachi.Core.Api;

/// <summary>
/// One RPC method, whatever its types (Swift <c>any RPCMethod.Type</c>): its
/// wire name and how long the client waits for it by default.
/// </summary>
public interface IRpcMethod
{
    /// <summary>The wire name, e.g. <c>message.list</c>.</summary>
    string Name { get; }

    /// <summary>The default timeout of a call (<see cref="RpcTimeouts"/>).</summary>
    TimeSpan Timeout { get; }
}

/// <summary>
/// One RPC method (Swift protocol <c>RPCMethod</c>): its wire name, the type
/// of its params object, the type of its result, and how long the client
/// waits for it by default. The descriptors of <see cref="API"/> are the only
/// ones there are, so a method name cannot be mistyped at a call site; a test
/// may make its own for a method the contract does not have.
/// </summary>
/// <typeparam name="TParams">The params object.</typeparam>
/// <typeparam name="TResult">The result.</typeparam>
public sealed class RpcMethod<TParams, TResult> : IRpcMethod
{
    /// <summary>A method with its name, default timeout and the metadata of its two types.</summary>
    public RpcMethod(string name, TimeSpan timeout, JsonTypeInfo<TParams> paramsInfo, JsonTypeInfo<TResult> resultInfo)
    {
        ArgumentException.ThrowIfNullOrEmpty(name);
        ArgumentNullException.ThrowIfNull(paramsInfo);
        ArgumentNullException.ThrowIfNull(resultInfo);
        Name = name;
        Timeout = timeout;
        ParamsInfo = paramsInfo;
        ResultInfo = resultInfo;
    }

    /// <inheritdoc/>
    public string Name { get; }

    /// <inheritdoc/>
    public TimeSpan Timeout { get; }

    /// <summary>How the params are written (the wire options of <see cref="ApiJsonContext"/>).</summary>
    public JsonTypeInfo<TParams> ParamsInfo { get; }

    /// <summary>How the result is read (the wire options of <see cref="ApiJsonContext"/>).</summary>
    public JsonTypeInfo<TResult> ResultInfo { get; }

    /// <summary>The wire name.</summary>
    public override string ToString() => Name;
}

/// <summary>Encodes as <c>{}</c>, for methods that take no parameters.</summary>
public sealed record EmptyParams;

/// <summary>Decodes from <c>{}</c>, for methods whose result carries nothing.</summary>
public sealed record EmptyResult;

/// <summary>The contract's methods, notifications and limits (backend/pkg/api).</summary>
public static class API
{
    /// <summary>
    /// api.ProtocolVersion. A daemon whose <c>system.hello</c> answer carries
    /// another value is refused before the key is read; <c>system.info</c>
    /// reports the same value.
    /// </summary>
    public const int ProtocolVersion = 2;

    /// <summary>
    /// The name of <c>system.info</c>, for a call by name (Swift
    /// <c>API.systemInfo</c>, renamed because the descriptor
    /// <see cref="SystemInfo"/> has the name).
    /// </summary>
    public const string SystemInfoName = "system.info";

    private static ApiJsonContext Wire => ApiJsonContext.Wire;

    // System

    /// <summary><c>system.info</c>: health check and daemon details.</summary>
    public static readonly RpcMethod<EmptyParams, SystemInfoResult> SystemInfo =
        new(SystemInfoName, RpcTimeouts.SystemInfo, Wire.EmptyParams, Wire.SystemInfoResult);

    /// <summary>
    /// <c>system.hello</c>: the first line of every connection (docs/api.md
    /// §1.4). The transport sends it itself when it connects, never as a call,
    /// which it refuses until the handshake is done.
    /// </summary>
    public static readonly RpcMethod<SystemHelloParams, SystemHelloResult> SystemHello =
        new("system.hello", RpcTimeouts.Handshake, Wire.SystemHelloParams, Wire.SystemHelloResult);

    /// <summary>
    /// <c>system.authenticate</c>: the second line of every connection
    /// (docs/api.md §1.4), sent by the transport as well; once it is answered
    /// the connection is usable.
    /// </summary>
    public static readonly RpcMethod<SystemAuthenticateParams, EmptyResult> SystemAuthenticate =
        new("system.authenticate", RpcTimeouts.Handshake, Wire.SystemAuthenticateParams, Wire.EmptyResult);

    /// <summary>
    /// <c>system.storage</c>: how much disk the mail store uses; cheap enough
    /// to ask every few seconds while the preferences are open.
    /// </summary>
    public static readonly RpcMethod<EmptyParams, SystemStorageResult> SystemStorage =
        new("system.storage", RpcTimeouts.Default, Wire.EmptyParams, Wire.SystemStorageResult);

    // Accounts

    /// <summary><c>account.list</c>.</summary>
    public static readonly RpcMethod<EmptyParams, AccountListResult> AccountList =
        new("account.list", RpcTimeouts.Default, Wire.EmptyParams, Wire.AccountListResult);

    /// <summary><c>account.add</c>.</summary>
    public static readonly RpcMethod<AccountAddParams, AccountAddResult> AccountAdd =
        new("account.add", RpcTimeouts.Save, Wire.AccountAddParams, Wire.AccountAddResult);

    /// <summary><c>account.remove</c>.</summary>
    public static readonly RpcMethod<AccountRemoveParams, EmptyResult> AccountRemove =
        new("account.remove", RpcTimeouts.Default, Wire.AccountRemoveParams, Wire.EmptyResult);

    /// <summary><c>account.setEnabled</c>.</summary>
    public static readonly RpcMethod<AccountSetEnabledParams, EmptyResult> AccountSetEnabled =
        new("account.setEnabled", RpcTimeouts.Default, Wire.AccountSetEnabledParams, Wire.EmptyResult);

    /// <summary><c>account.update</c>.</summary>
    public static readonly RpcMethod<AccountUpdateParams, EmptyResult> AccountUpdate =
        new("account.update", RpcTimeouts.Save, Wire.AccountUpdateParams, Wire.EmptyResult);

    /// <summary><c>account.discover</c>.</summary>
    public static readonly RpcMethod<AccountDiscoverParams, AccountDiscoverResult> AccountDiscover =
        new("account.discover", RpcTimeouts.Discover, Wire.AccountDiscoverParams, Wire.AccountDiscoverResult);

    /// <summary><c>account.test</c>.</summary>
    public static readonly RpcMethod<AccountTestParams, AccountTestResult> AccountTest =
        new("account.test", RpcTimeouts.Test, Wire.AccountTestParams, Wire.AccountTestResult);

    /// <summary><c>account.linked</c>.</summary>
    public static readonly RpcMethod<EmptyParams, AccountLinkedResult> AccountLinked =
        new("account.linked", RpcTimeouts.Default, Wire.EmptyParams, Wire.AccountLinkedResult);

    /// <summary><c>account.reorder</c>.</summary>
    public static readonly RpcMethod<AccountReorderParams, EmptyResult> AccountReorder =
        new("account.reorder", RpcTimeouts.Default, Wire.AccountReorderParams, Wire.EmptyResult);

    /// <summary><c>account.oauthStart</c>.</summary>
    public static readonly RpcMethod<AccountOAuthStartParams, AccountOAuthStartResult> AccountOAuthStart =
        new("account.oauthStart", RpcTimeouts.OAuthStart, Wire.AccountOAuthStartParams, Wire.AccountOAuthStartResult);

    /// <summary><c>account.oauthWait</c>: blocks up to 60 s in the daemon and answers <c>pending</c>; call again.</summary>
    public static readonly RpcMethod<AccountOAuthWaitParams, AccountOAuthWaitResult> AccountOAuthWait =
        new("account.oauthWait", RpcTimeouts.OAuthWaitCall, Wire.AccountOAuthWaitParams, Wire.AccountOAuthWaitResult);

    /// <summary><c>account.oauthCancel</c>.</summary>
    public static readonly RpcMethod<AccountOAuthCancelParams, EmptyResult> AccountOAuthCancel =
        new("account.oauthCancel", RpcTimeouts.Default, Wire.AccountOAuthCancelParams, Wire.EmptyResult);

    /// <summary><c>account.detectSite</c>: what kind of Jira site an address names, anonymously.</summary>
    public static readonly RpcMethod<AccountDetectSiteParams, AccountDetectSiteResult> AccountDetectSite =
        new("account.detectSite", RpcTimeouts.DetectSite, Wire.AccountDetectSiteParams, Wire.AccountDetectSiteResult);

    /// <summary><c>account.listSpaces</c>: signs in to a Jira site and lists its spaces and statuses.</summary>
    public static readonly RpcMethod<AccountListSpacesParams, AccountListSpacesResult> AccountListSpaces =
        new("account.listSpaces", RpcTimeouts.ListSpaces, Wire.AccountListSpacesParams, Wire.AccountListSpacesResult);

    // Folders

    /// <summary><c>folder.list</c>.</summary>
    public static readonly RpcMethod<FolderListParams, FolderListResult> FolderList =
        new("folder.list", RpcTimeouts.Default, Wire.FolderListParams, Wire.FolderListResult);

    /// <summary><c>folder.subscribe</c>; the daemon answers notImplemented so far.</summary>
    public static readonly RpcMethod<FolderSubscribeParams, EmptyResult> FolderSubscribe =
        new("folder.subscribe", RpcTimeouts.Default, Wire.FolderSubscribeParams, Wire.EmptyResult);

    // Messages

    /// <summary><c>message.list</c>.</summary>
    public static readonly RpcMethod<MessageListParams, MessageListResult> MessageList =
        new("message.list", RpcTimeouts.Default, Wire.MessageListParams, Wire.MessageListResult);

    /// <summary><c>message.get</c>.</summary>
    public static readonly RpcMethod<MessageGetParams, MessageGetResult> MessageGet =
        new("message.get", RpcTimeouts.Default, Wire.MessageGetParams, Wire.MessageGetResult);

    /// <summary>
    /// <c>message.body</c>: the remote timeout even without an override, since
    /// a stored <c>allow</c> or a known sender lets the daemon fetch the images
    /// before it answers.
    /// </summary>
    public static readonly RpcMethod<MessageBodyParams, MessageBodyResult> MessageBody =
        new("message.body", RpcTimeouts.Remote, Wire.MessageBodyParams, Wire.MessageBodyResult);

    /// <summary><c>message.part</c>.</summary>
    public static readonly RpcMethod<MessagePartParams, MessagePartResult> MessagePart =
        new("message.part", RpcTimeouts.Part, Wire.MessagePartParams, Wire.MessagePartResult);

    /// <summary><c>message.embedded</c>.</summary>
    public static readonly RpcMethod<MessageEmbeddedParams, MessageEmbeddedResult> MessageEmbedded =
        new("message.embedded", RpcTimeouts.Remote, Wire.MessageEmbeddedParams, Wire.MessageEmbeddedResult);

    /// <summary>
    /// <c>message.download</c>: fetches the parts of a message kept on the
    /// server (and a body not downloaded yet); the daemon's budget is 4
    /// minutes, one download per message shared by every caller.
    /// </summary>
    public static readonly RpcMethod<MessageDownloadParams, MessageDownloadResult> MessageDownload =
        new("message.download", RpcTimeouts.Download, Wire.MessageDownloadParams, Wire.MessageDownloadResult);

    /// <summary>
    /// <c>message.unsubscribe</c>: acts on the sender's unsubscribe offer
    /// (the daemon may verify the message and ask the sender's server, 15 s,
    /// first).
    /// </summary>
    public static readonly RpcMethod<MessageUnsubscribeParams, MessageUnsubscribeResult> MessageUnsubscribe =
        new("message.unsubscribe", RpcTimeouts.Unsubscribe, Wire.MessageUnsubscribeParams, Wire.MessageUnsubscribeResult);

    /// <summary><c>message.flag</c>.</summary>
    public static readonly RpcMethod<MessageFlagParams, EmptyResult> MessageFlag =
        new("message.flag", RpcTimeouts.Default, Wire.MessageFlagParams, Wire.EmptyResult);

    /// <summary><c>message.move</c>.</summary>
    public static readonly RpcMethod<MessageMoveParams, EmptyResult> MessageMove =
        new("message.move", RpcTimeouts.Default, Wire.MessageMoveParams, Wire.EmptyResult);

    /// <summary><c>message.delete</c>.</summary>
    public static readonly RpcMethod<MessageDeleteParams, EmptyResult> MessageDelete =
        new("message.delete", RpcTimeouts.Default, Wire.MessageDeleteParams, Wire.EmptyResult);

    /// <summary><c>message.send</c>.</summary>
    public static readonly RpcMethod<MessageSendParams, MessageSendResult> MessageSend =
        new("message.send", RpcTimeouts.Default, Wire.MessageSendParams, Wire.MessageSendResult);

    // Outbox

    /// <summary><c>outbox.retry</c>.</summary>
    public static readonly RpcMethod<OutboxRetryParams, EmptyResult> OutboxRetry =
        new("outbox.retry", RpcTimeouts.Default, Wire.OutboxRetryParams, Wire.EmptyResult);

    // Threads

    /// <summary><c>thread.list</c>.</summary>
    public static readonly RpcMethod<ThreadListParams, ThreadListResult> ThreadList =
        new("thread.list", RpcTimeouts.Default, Wire.ThreadListParams, Wire.ThreadListResult);

    /// <summary><c>thread.get</c>.</summary>
    public static readonly RpcMethod<ThreadGetParams, ThreadGetResult> ThreadGet =
        new("thread.get", RpcTimeouts.Default, Wire.ThreadGetParams, Wire.ThreadGetResult);

    // Drafts

    /// <summary><c>draft.save</c>.</summary>
    public static readonly RpcMethod<DraftSaveParams, DraftSaveResult> DraftSave =
        new("draft.save", RpcTimeouts.Default, Wire.DraftSaveParams, Wire.DraftSaveResult);

    /// <summary><c>draft.list</c>.</summary>
    public static readonly RpcMethod<DraftListParams, DraftListResult> DraftList =
        new("draft.list", RpcTimeouts.Default, Wire.DraftListParams, Wire.DraftListResult);

    /// <summary><c>draft.delete</c>.</summary>
    public static readonly RpcMethod<DraftDeleteParams, EmptyResult> DraftDelete =
        new("draft.delete", RpcTimeouts.Default, Wire.DraftDeleteParams, Wire.EmptyResult);

    /// <summary><c>draft.create</c>.</summary>
    public static readonly RpcMethod<DraftCreateParams, DraftCreateResult> DraftCreate =
        new("draft.create", RpcTimeouts.Compose, Wire.DraftCreateParams, Wire.DraftCreateResult);

    /// <summary><c>draft.open</c>.</summary>
    public static readonly RpcMethod<DraftOpenParams, DraftOpenResult> DraftOpen =
        new("draft.open", RpcTimeouts.Compose, Wire.DraftOpenParams, Wire.DraftOpenResult);

    /// <summary><c>draft.markdown</c>: pasted text rendered as sanitised HTML when it reads as Markdown.</summary>
    public static readonly RpcMethod<DraftMarkdownParams, DraftMarkdownResult> DraftMarkdown =
        new("draft.markdown", RpcTimeouts.Default, Wire.DraftMarkdownParams, Wire.DraftMarkdownResult);

    /// <summary><c>draft.get</c>: one stored draft by id, as <c>draft.list</c> lists it.</summary>
    public static readonly RpcMethod<DraftGetParams, DraftGetResult> DraftGet =
        new("draft.get", RpcTimeouts.Default, Wire.DraftGetParams, Wire.DraftGetResult);

    // Attachments

    /// <summary><c>attachment.import</c>.</summary>
    public static readonly RpcMethod<AttachmentImportParams, AttachmentImportResult> AttachmentImport =
        new("attachment.import", RpcTimeouts.Default, Wire.AttachmentImportParams, Wire.AttachmentImportResult);

    /// <summary><c>attachment.remove</c>.</summary>
    public static readonly RpcMethod<AttachmentRemoveParams, EmptyResult> AttachmentRemove =
        new("attachment.remove", RpcTimeouts.Default, Wire.AttachmentRemoveParams, Wire.EmptyResult);

    /// <summary><c>attachment.get</c>.</summary>
    public static readonly RpcMethod<AttachmentGetParams, AttachmentGetResult> AttachmentGet =
        new("attachment.get", RpcTimeouts.Part, Wire.AttachmentGetParams, Wire.AttachmentGetResult);

    // Search

    /// <summary><c>search.query</c>.</summary>
    public static readonly RpcMethod<SearchQueryParams, SearchQueryResult> SearchQuery =
        new("search.query", RpcTimeouts.Default, Wire.SearchQueryParams, Wire.SearchQueryResult);

    // Sync

    /// <summary><c>sync.status</c>.</summary>
    public static readonly RpcMethod<SyncStatusParams, SyncStatusResult> SyncStatus =
        new("sync.status", RpcTimeouts.Default, Wire.SyncStatusParams, Wire.SyncStatusResult);

    /// <summary><c>sync.trigger</c>.</summary>
    public static readonly RpcMethod<SyncTriggerParams, EmptyResult> SyncTrigger =
        new("sync.trigger", RpcTimeouts.Default, Wire.SyncTriggerParams, Wire.EmptyResult);

    // Config

    /// <summary><c>config.get</c>.</summary>
    public static readonly RpcMethod<EmptyParams, ConfigGetResult> ConfigGet =
        new("config.get", RpcTimeouts.Default, Wire.EmptyParams, Wire.ConfigGetResult);

    /// <summary><c>config.set</c>.</summary>
    public static readonly RpcMethod<ConfigSetParams, ConfigSetResult> ConfigSet =
        new("config.set", RpcTimeouts.Default, Wire.ConfigSetParams, Wire.ConfigSetResult);

    // Known senders

    /// <summary><c>sender.list</c>.</summary>
    public static readonly RpcMethod<EmptyParams, SenderListResult> SenderList =
        new("sender.list", RpcTimeouts.Default, Wire.EmptyParams, Wire.SenderListResult);

    /// <summary><c>sender.add</c>.</summary>
    public static readonly RpcMethod<SenderAddParams, EmptyResult> SenderAdd =
        new("sender.add", RpcTimeouts.Default, Wire.SenderAddParams, Wire.EmptyResult);

    /// <summary><c>sender.remove</c>.</summary>
    public static readonly RpcMethod<SenderRemoveParams, EmptyResult> SenderRemove =
        new("sender.remove", RpcTimeouts.Default, Wire.SenderRemoveParams, Wire.EmptyResult);

    // Contacts

    /// <summary><c>contact.search</c>.</summary>
    public static readonly RpcMethod<ContactSearchParams, ContactSearchResult> ContactSearch =
        new("contact.search", RpcTimeouts.Default, Wire.ContactSearchParams, Wire.ContactSearchResult);

    // Issues

    /// <summary><c>issue.transitions</c>: the status changes the site offers the user on an issue.</summary>
    public static readonly RpcMethod<IssueTransitionsParams, IssueTransitionsResult> IssueTransitions =
        new("issue.transitions", RpcTimeouts.Transitions, Wire.IssueTransitionsParams, Wire.IssueTransitionsResult);

    /// <summary><c>issue.transition</c>: performs one; the issue is refreshed before the answer.</summary>
    public static readonly RpcMethod<IssueTransitionParams, IssueTransitionResult> IssueTransition =
        new("issue.transition", RpcTimeouts.Transition, Wire.IssueTransitionParams, Wire.IssueTransitionResult);

    // Board (docs/api.md §4.13)

    /// <summary><c>board.list</c>: the cases, the open commitments of the live ones and the state of triage.</summary>
    public static readonly RpcMethod<BoardListParams, BoardListResult> BoardList =
        new("board.list", RpcTimeouts.Default, Wire.BoardListParams, Wire.BoardListResult);

    /// <summary><c>board.get</c>: one case with the members that count, as plain text.</summary>
    public static readonly RpcMethod<BoardGetParams, BoardGetResult> BoardGet =
        new("board.get", RpcTimeouts.Default, Wire.BoardGetParams, Wire.BoardGetResult);

    /// <summary><c>board.setState</c>: the user's own state of a case, or back to automatic.</summary>
    public static readonly RpcMethod<BoardSetStateParams, BoardSetStateResult> BoardSetState =
        new("board.setState", RpcTimeouts.Default, Wire.BoardSetStateParams, Wire.BoardSetStateResult);

    /// <summary><c>board.setDone</c>.</summary>
    public static readonly RpcMethod<BoardSetDoneParams, BoardSetDoneResult> BoardSetDone =
        new("board.setDone", RpcTimeouts.Default, Wire.BoardSetDoneParams, Wire.BoardSetDoneResult);

    /// <summary><c>board.remind</c>: snoozes a case until a time, or ends the remind.</summary>
    public static readonly RpcMethod<BoardRemindParams, BoardRemindResult> BoardRemind =
        new("board.remind", RpcTimeouts.Default, Wire.BoardRemindParams, Wire.BoardRemindResult);

    /// <summary><c>board.archive</c>: moves the case's inbox members to the archive (local first) and marks it done.</summary>
    public static readonly RpcMethod<BoardArchiveParams, BoardArchiveResult> BoardArchive =
        new("board.archive", RpcTimeouts.Default, Wire.BoardArchiveParams, Wire.BoardArchiveResult);

    /// <summary><c>board.unflag</c>: clears the flag of every copy that makes the case <c>hot.flagged</c>.</summary>
    public static readonly RpcMethod<BoardUnflagParams, BoardUnflagResult> BoardUnflag =
        new("board.unflag", RpcTimeouts.Default, Wire.BoardUnflagParams, Wire.BoardUnflagResult);

    /// <summary><c>board.discardDraft</c>: deletes the draft linked to a case.</summary>
    public static readonly RpcMethod<BoardDiscardDraftParams, BoardDiscardDraftResult> BoardDiscardDraft =
        new("board.discardDraft", RpcTimeouts.Default, Wire.BoardDiscardDraftParams, Wire.BoardDiscardDraftResult);

    /// <summary><c>board.setDraft</c>: links a reply draft to a case on the user's request.</summary>
    public static readonly RpcMethod<BoardSetDraftParams, BoardSetDraftResult> BoardSetDraft =
        new("board.setDraft", RpcTimeouts.Default, Wire.BoardSetDraftParams, Wire.BoardSetDraftResult);

    /// <summary><c>board.queue</c>: the cases an assistant should triage, with their text (the MCP bridge's).</summary>
    public static readonly RpcMethod<BoardQueueParams, BoardQueueResult> BoardQueue =
        new("board.queue", RpcTimeouts.Default, Wire.BoardQueueParams, Wire.BoardQueueResult);

    /// <summary><c>board.annotate</c>: an assistant's annotation of a case (the MCP bridge's).</summary>
    public static readonly RpcMethod<BoardAnnotateParams, BoardAnnotateResult> BoardAnnotate =
        new("board.annotate", RpcTimeouts.Default, Wire.BoardAnnotateParams, Wire.BoardAnnotateResult);

    /// <summary><c>board.commit</c>: a commitment an assistant found in the user's message (the MCP bridge's).</summary>
    public static readonly RpcMethod<BoardCommitParams, BoardCommitResult> BoardCommit =
        new("board.commit", RpcTimeouts.Default, Wire.BoardCommitParams, Wire.BoardCommitResult);

    /// <summary><c>board.setCommitment</c>: ticks a commitment off or reopens it.</summary>
    public static readonly RpcMethod<BoardSetCommitmentParams, BoardSetCommitmentResult> BoardSetCommitment =
        new("board.setCommitment", RpcTimeouts.Default, Wire.BoardSetCommitmentParams, Wire.BoardSetCommitmentResult);

    /// <summary><c>board.preferences</c>.</summary>
    public static readonly RpcMethod<EmptyParams, BoardPreferencesResult> BoardPreferences =
        new("board.preferences", RpcTimeouts.Default, Wire.EmptyParams, Wire.BoardPreferencesResult);

    /// <summary><c>board.setPreferences</c>: replaces every preference.</summary>
    public static readonly RpcMethod<BoardSetPreferencesParams, BoardSetPreferencesResult> BoardSetPreferences =
        new("board.setPreferences", RpcTimeouts.Default, Wire.BoardSetPreferencesParams, Wire.BoardSetPreferencesResult);

    /// <summary><c>board.runStart</c>: starts a triage run.</summary>
    public static readonly RpcMethod<BoardRunStartParams, BoardRunStartResult> BoardRunStart =
        new("board.runStart", RpcTimeouts.Default, Wire.BoardRunStartParams, Wire.BoardRunStartResult);

    /// <summary><c>board.runEnd</c>: ends a triage run, with the class of its failure if any.</summary>
    public static readonly RpcMethod<BoardRunEndParams, EmptyResult> BoardRunEnd =
        new("board.runEnd", RpcTimeouts.Default, Wire.BoardRunEndParams, Wire.EmptyResult);

    // Tables

    /// <summary>Every method, in the order of methods.go.</summary>
    public static IReadOnlyList<IRpcMethod> Methods { get; } =
    [
        SystemInfo, SystemHello, SystemAuthenticate, SystemStorage,
        AccountList, AccountAdd, AccountRemove, AccountSetEnabled,
        AccountUpdate, AccountDiscover, AccountTest, AccountLinked,
        AccountReorder, AccountOAuthStart, AccountOAuthWait, AccountOAuthCancel,
        AccountDetectSite, AccountListSpaces,
        FolderList, FolderSubscribe,
        MessageList, MessageGet, MessageBody, MessagePart,
        MessageEmbedded, MessageDownload, MessageFlag, MessageMove, MessageDelete,
        MessageSend, MessageUnsubscribe,
        OutboxRetry,
        ThreadList, ThreadGet,
        DraftSave, DraftList, DraftDelete, DraftCreate, DraftOpen,
        DraftMarkdown, DraftGet,
        AttachmentImport, AttachmentRemove, AttachmentGet,
        SearchQuery,
        SyncStatus, SyncTrigger,
        ConfigGet, ConfigSet,
        SenderList, SenderAdd, SenderRemove,
        ContactSearch,
        IssueTransitions, IssueTransition,
        BoardList, BoardGet, BoardSetState, BoardSetDone, BoardRemind,
        BoardArchive, BoardUnflag, BoardDiscardDraft, BoardSetDraft, BoardQueue, BoardAnnotate,
        BoardCommit, BoardSetCommitment, BoardPreferences, BoardSetPreferences,
        BoardRunStart, BoardRunEnd,
    ];

    /// <summary>api.AllMethods: every callable method name.</summary>
    public static IReadOnlyList<string> AllMethods { get; } = [.. Methods.Select(m => m.Name)];

    /// <summary>The notification names (api.Notify*).</summary>
    public static class Notify
    {
        /// <summary>api.NotifyNewMessage.</summary>
        public const string NewMessage = "notify.newMessage";

        /// <summary>api.NotifySyncState.</summary>
        public const string SyncState = "notify.syncState";

        /// <summary>api.NotifyAuthRequired.</summary>
        public const string AuthRequired = "notify.authRequired";

        /// <summary>api.NotifyAccountsChanged.</summary>
        public const string AccountsChanged = "notify.accountsChanged";

        /// <summary>api.NotifyMessagesChanged.</summary>
        public const string MessagesChanged = "notify.messagesChanged";

        /// <summary>api.NotifyBoardChanged.</summary>
        public const string BoardChanged = "notify.boardChanged";
    }

    /// <summary>api.AllNotifications: every server-initiated notification name.</summary>
    public static IReadOnlyList<string> AllNotifications { get; } =
    [
        Notify.NewMessage, Notify.SyncState, Notify.AuthRequired, Notify.AccountsChanged, Notify.MessagesChanged,
        Notify.BoardChanged,
    ];

    /// <summary>
    /// api.MailCapabilities: what an IMAP or Graph account can do, and every
    /// account of a daemon that sends no <c>capabilities</c>.
    /// </summary>
    public static IReadOnlyList<Capability> MailCapabilities { get; } =
        [Capability.Compose, Capability.Reply, Capability.ReplyAll, Capability.Forward, Capability.Move, Capability.Delete];

    /// <summary>The limits the daemon enforces (types.go constants), for pre-checks.</summary>
    public static class Limits
    {
        /// <summary>api.DefaultPageLimit.</summary>
        public const int DefaultPageLimit = 50;

        /// <summary>api.MaxPageLimit.</summary>
        public const int MaxPageLimit = 500;

        /// <summary>The built RFC 5322 message (<c>message.send</c>).</summary>
        public const int MaxOutgoingMessageBytes = 36 << 20;

        /// <summary><c>textBody</c> and <c>htmlBody</c>, each.</summary>
        public const int MaxDraftBodyBytes = 1 << 20;

        /// <summary>api.MaxDraftSubjectBytes.</summary>
        public const int MaxDraftSubjectBytes = 1024;

        /// <summary>to + cc + bcc.</summary>
        public const int MaxDraftRecipients = 500;

        /// <summary>api.MaxDraftAttachments.</summary>
        public const int MaxDraftAttachments = 100;

        /// <summary>One file (<c>attachment.import</c>).</summary>
        public const int MaxAttachmentBytes = 25 << 20;

        /// <summary>The sum over a draft (<c>draft.save</c>).</summary>
        public const int MaxDraftAttachmentBytes = 25 << 20;

        /// <summary>Inline base64 payloads (<c>message.part</c>, <c>attachment.get</c>, <c>attachment.import</c> data).</summary>
        public const int MaxAttachmentDataBytes = 16 << 20;

        /// <summary>api.MaxThreadMessages.</summary>
        public const int MaxThreadMessages = 500;

        /// <summary>api.MaxThreadParticipants.</summary>
        public const int MaxThreadParticipants = 8;

        /// <summary>api.MaxDraftAttributionBytes.</summary>
        public const int MaxDraftAttributionBytes = 2048;

        /// <summary>api.MaxDraftAttributionLines.</summary>
        public const int MaxDraftAttributionLines = 16;

        /// <summary>The smallest non-zero <see cref="Preferences.SyncIntervalSeconds"/>.</summary>
        public const int SyncIntervalMin = 60;

        /// <summary>api.OfflineDaysMax.</summary>
        public const int OfflineDaysMax = 3650;

        /// <summary>The largest <see cref="Preferences.AttachmentOfflineDays"/> (api.AttachmentOfflineDaysMax).</summary>
        public const int AttachmentOfflineDaysMax = 3650;

        /// <summary>
        /// <see cref="Preferences.AttachmentOfflineDays"/> that keeps no large
        /// attachment locally: Small Attachments Only (api.AttachmentOfflineNone).
        /// </summary>
        public const int AttachmentOfflineNone = -1;

        /// <summary><c>messageIds</c> in message.flag, message.move and message.delete.</summary>
        public const int MaxMessageIdsPerCall = 1000;

        /// <summary>api.DefaultContactLimit.</summary>
        public const int DefaultContactLimit = 10;

        /// <summary>api.MaxContactLimit.</summary>
        public const int MaxContactLimit = 50;

        /// <summary>api.MaxContactQueryBytes.</summary>
        public const int MaxContactQueryBytes = 256;

        /// <summary>
        /// search.query: the query's length, its terms and filters, and the
        /// matches <c>page.total</c> counts exactly (-1 beyond).
        /// </summary>
        public const int MaxSearchQueryBytes = 1024;

        /// <summary>api.MaxSearchTerms.</summary>
        public const int MaxSearchTerms = 32;

        /// <summary>api.MaxSearchTotal.</summary>
        public const int MaxSearchTotal = 1000;

        /// <summary>JiraConfig.Spaces.</summary>
        public const int MaxJiraSpaces = 200;

        /// <summary>JiraConfig.ClosedStatuses.</summary>
        public const int MaxJiraStatuses = 64;

        /// <summary>NotificationSenders, BotNames, MetadataFilters, AuthorPrefixes, each.</summary>
        public const int MaxJiraListEntries = 32;

        /// <summary>One entry of those lists, in bytes.</summary>
        public const int MaxJiraPatternBytes = 512;

        /// <summary>JiraConfig.OfflineDays.</summary>
        public const int MaxJiraOfflineDays = 365;

        /// <summary>What JiraConfig.OfflineDays 0 means.</summary>
        public const int DefaultJiraOfflineDays = 30;

        /// <summary>The transitions <c>issue.transitions</c> returns at most.</summary>
        public const int MaxIssueTransitions = 100;
    }
}
