// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Transport/JSONRPC.swift (JSONCoding's
// settings): the one System.Text.Json source-generated context of the
// contract, so that trimming and native AOT stay possible (no reflection).
//
// The generator refuses [JsonSerializable] on more than one partial
// declaration of a context (CS8785, a duplicate hint name, measured with the
// .NET 10.0.400 SDK), so the types are listed here in one place, grouped by
// the files of this folder. Every type that is serialized on its own must
// be public (the generated metadata properties are): the contract has no
// private helper types.

using System.Collections.Generic;
using System.Text.Encodings.Web;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// The metadata of every type of the contract. Use <see cref="Wire"/>, whose
/// options are the contract's: nulls are left out when writing (Go's
/// omitempty; Swift's synthesized encoding), HTML and non-ASCII text stay
/// unescaped (the default escaping inflates an HTML body about sixfold),
/// every date is RFC 3339 (<see cref="Rfc3339Converter"/>) and a null in a
/// member that is not nullable fails the decoding, as it fails Swift's.
/// <see cref="JsonSerializerContext"/>'s own <c>Default</c> has the same
/// settings but the default escaping.
/// </summary>
[JsonSourceGenerationOptions(
    DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    RespectNullableAnnotations = true,
    Converters = new[] { typeof(Rfc3339Converter) })]
// API.cs
[JsonSerializable(typeof(EmptyParams))]
[JsonSerializable(typeof(EmptyResult))]
// Auth.cs
[JsonSerializable(typeof(SystemHelloParams))]
[JsonSerializable(typeof(SystemHelloResult))]
[JsonSerializable(typeof(SystemAuthenticateParams))]
// Accounts.cs
[JsonSerializable(typeof(Page))]
[JsonSerializable(typeof(PageInfo))]
[JsonSerializable(typeof(SystemInfoResult))]
[JsonSerializable(typeof(ServerConfig))]
[JsonSerializable(typeof(OAuth2Config))]
[JsonSerializable(typeof(GraphConfig))]
[JsonSerializable(typeof(AccountConfig))]
[JsonSerializable(typeof(Credentials))]
[JsonSerializable(typeof(Account))]
[JsonSerializable(typeof(AccountListResult))]
[JsonSerializable(typeof(AccountAddParams))]
[JsonSerializable(typeof(AccountAddResult))]
[JsonSerializable(typeof(AccountRemoveParams))]
[JsonSerializable(typeof(AccountSetEnabledParams))]
[JsonSerializable(typeof(AccountReorderParams))]
[JsonSerializable(typeof(AccountDiscoverParams))]
[JsonSerializable(typeof(AccountDiscoverResult))]
[JsonSerializable(typeof(LinkedAccount))]
[JsonSerializable(typeof(AccountLinkedResult))]
[JsonSerializable(typeof(OAuthBrowserPage))]
[JsonSerializable(typeof(AccountOAuthStartParams))]
[JsonSerializable(typeof(AccountOAuthStartResult))]
[JsonSerializable(typeof(AccountOAuthWaitParams))]
[JsonSerializable(typeof(AccountOAuthWaitResult))]
[JsonSerializable(typeof(AccountOAuthCancelParams))]
[JsonSerializable(typeof(AccountUpdateParams))]
[JsonSerializable(typeof(AccountTestParams))]
[JsonSerializable(typeof(EndpointTestResult))]
[JsonSerializable(typeof(AccountTestResult))]
// Attachments.cs
[JsonSerializable(typeof(AttachmentImportParams))]
[JsonSerializable(typeof(AttachmentImportResult))]
[JsonSerializable(typeof(AttachmentRemoveParams))]
[JsonSerializable(typeof(AttachmentGetParams))]
[JsonSerializable(typeof(AttachmentGetResult))]
// Board.cs (its identifiers and string types too)
[JsonSerializable(typeof(BoardCaseId))]
[JsonSerializable(typeof(BoardCommitmentId))]
[JsonSerializable(typeof(BoardRunId))]
[JsonSerializable(typeof(BoardState))]
[JsonSerializable(typeof(BoardReason))]
[JsonSerializable(typeof(BoardVisibility))]
[JsonSerializable(typeof(BoardCommitmentState))]
[JsonSerializable(typeof(BoardTrigger))]
[JsonSerializable(typeof(BoardRunError))]
[JsonSerializable(typeof(QuoteField))]
[JsonSerializable(typeof(BoardCase))]
[JsonSerializable(typeof(BoardIssue))]
[JsonSerializable(typeof(BoardDraft))]
[JsonSerializable(typeof(BoardAnnotation))]
[JsonSerializable(typeof(BoardDue))]
[JsonSerializable(typeof(BoardCommitment))]
[JsonSerializable(typeof(BoardMessage))]
[JsonSerializable(typeof(BoardRun))]
[JsonSerializable(typeof(BoardTriage))]
[JsonSerializable(typeof(BoardUsage))]
[JsonSerializable(typeof(BoardUsageTotal))]
[JsonSerializable(typeof(BoardWindows))]
[JsonSerializable(typeof(BoardPreferences))]
[JsonSerializable(typeof(QuoteNotFoundData))]
[JsonSerializable(typeof(BoardListParams))]
[JsonSerializable(typeof(BoardListResult))]
[JsonSerializable(typeof(BoardGetParams))]
[JsonSerializable(typeof(BoardGetResult))]
[JsonSerializable(typeof(BoardSetStateParams))]
[JsonSerializable(typeof(BoardSetStateResult))]
[JsonSerializable(typeof(BoardSetDoneParams))]
[JsonSerializable(typeof(BoardSetDoneResult))]
[JsonSerializable(typeof(BoardRemindParams))]
[JsonSerializable(typeof(BoardRemindResult))]
[JsonSerializable(typeof(BoardArchiveParams))]
[JsonSerializable(typeof(BoardArchiveResult))]
[JsonSerializable(typeof(BoardMoved))]
[JsonSerializable(typeof(BoardUnflagParams))]
[JsonSerializable(typeof(BoardUnflagResult))]
[JsonSerializable(typeof(BoardDiscardDraftParams))]
[JsonSerializable(typeof(BoardDiscardDraftResult))]
[JsonSerializable(typeof(BoardSetDraftParams))]
[JsonSerializable(typeof(BoardSetDraftResult))]
[JsonSerializable(typeof(BoardQueueParams))]
[JsonSerializable(typeof(BoardQueueResult))]
[JsonSerializable(typeof(BoardQueueItem))]
[JsonSerializable(typeof(BoardQueueMessage))]
[JsonSerializable(typeof(BoardAnnotateParams))]
[JsonSerializable(typeof(BoardAnnotateResult))]
[JsonSerializable(typeof(BoardCommitParams))]
[JsonSerializable(typeof(BoardCommitResult))]
[JsonSerializable(typeof(BoardSetCommitmentParams))]
[JsonSerializable(typeof(BoardSetCommitmentResult))]
[JsonSerializable(typeof(BoardPreferencesResult))]
[JsonSerializable(typeof(BoardSetPreferencesParams))]
[JsonSerializable(typeof(BoardSetPreferencesResult))]
[JsonSerializable(typeof(BoardRunStartParams))]
[JsonSerializable(typeof(BoardRunStartResult))]
[JsonSerializable(typeof(BoardRunEndParams))]
[JsonSerializable(typeof(BoardChangedNotification))]
// Config.cs
[JsonSerializable(typeof(Preferences))]
[JsonSerializable(typeof(ConfigGetResult))]
[JsonSerializable(typeof(ConfigSetParams))]
[JsonSerializable(typeof(ConfigSetResult))]
// Contacts.cs
[JsonSerializable(typeof(Contact))]
[JsonSerializable(typeof(ContactSearchParams))]
[JsonSerializable(typeof(ContactSearchResult))]
// Drafts.cs
[JsonSerializable(typeof(DraftAttachment))]
[JsonSerializable(typeof(Draft))]
[JsonSerializable(typeof(DraftSaveParams))]
[JsonSerializable(typeof(DraftSaveResult))]
[JsonSerializable(typeof(DraftListParams))]
[JsonSerializable(typeof(DraftListResult))]
[JsonSerializable(typeof(DraftDeleteParams))]
[JsonSerializable(typeof(DraftGetParams))]
[JsonSerializable(typeof(DraftGetResult))]
[JsonSerializable(typeof(DraftCreateParams))]
[JsonSerializable(typeof(DraftCreateResult))]
[JsonSerializable(typeof(DraftOpenParams))]
[JsonSerializable(typeof(DraftOpenResult))]
[JsonSerializable(typeof(DraftMarkdownParams))]
[JsonSerializable(typeof(DraftMarkdownResult))]
[JsonSerializable(typeof(MessageSendParams))]
[JsonSerializable(typeof(MessageSendResult))]
[JsonSerializable(typeof(OutboxRetryParams))]
// Enums.cs, Identifiers.cs, ErrorCode.cs (list elements are looked up by type)
[JsonSerializable(typeof(Security))]
[JsonSerializable(typeof(AuthMethod))]
[JsonSerializable(typeof(OAuth2Source))]
[JsonSerializable(typeof(OAuth2Provider))]
[JsonSerializable(typeof(AccountKind))]
[JsonSerializable(typeof(GraphSource))]
[JsonSerializable(typeof(DiscoverSource))]
[JsonSerializable(typeof(LinkedProvider))]
[JsonSerializable(typeof(OAuthSessionStatus))]
[JsonSerializable(typeof(FolderRole))]
[JsonSerializable(typeof(Flag))]
[JsonSerializable(typeof(OutboxState))]
[JsonSerializable(typeof(SortOrder))]
[JsonSerializable(typeof(MessageFilter))]
[JsonSerializable(typeof(RemoteContentPolicy))]
[JsonSerializable(typeof(BodyState))]
[JsonSerializable(typeof(ComposeMode))]
[JsonSerializable(typeof(QuoteForm))]
[JsonSerializable(typeof(SyncStatus))]
[JsonSerializable(typeof(KnownSenderSource))]
[JsonSerializable(typeof(ContactSource))]
[JsonSerializable(typeof(Capability))]
[JsonSerializable(typeof(JiraDeployment))]
[JsonSerializable(typeof(VirtualFolder))]
[JsonSerializable(typeof(NotificationMailMode))]
[JsonSerializable(typeof(IssueStatusCategory))]
[JsonSerializable(typeof(IssueItemKind))]
[JsonSerializable(typeof(CommentVisibility))]
[JsonSerializable(typeof(IssueField))]
[JsonSerializable(typeof(BulkKind))]
[JsonSerializable(typeof(UnsubscribeMethod))]
[JsonSerializable(typeof(UnsubscribeOutcome))]
[JsonSerializable(typeof(AccountId))]
[JsonSerializable(typeof(FolderId))]
[JsonSerializable(typeof(MessageId))]
[JsonSerializable(typeof(ThreadId))]
[JsonSerializable(typeof(DraftId))]
[JsonSerializable(typeof(ErrorCode))]
[JsonSerializable(typeof(string))]
// Folders.cs
[JsonSerializable(typeof(Folder))]
[JsonSerializable(typeof(FolderListParams))]
[JsonSerializable(typeof(FolderListResult))]
[JsonSerializable(typeof(FolderSubscribeParams))]
// Jira.cs
[JsonSerializable(typeof(SpaceRef))]
[JsonSerializable(typeof(StatusRef))]
[JsonSerializable(typeof(JiraConfig))]
[JsonSerializable(typeof(IssueInfo))]
[JsonSerializable(typeof(IssueChange))]
[JsonSerializable(typeof(MessageIssue))]
[JsonSerializable(typeof(AccountDetectSiteParams))]
[JsonSerializable(typeof(AccountDetectSiteResult))]
[JsonSerializable(typeof(AccountListSpacesParams))]
[JsonSerializable(typeof(Space))]
[JsonSerializable(typeof(IssueStatus))]
[JsonSerializable(typeof(SiteUser))]
[JsonSerializable(typeof(AccountListSpacesResult))]
[JsonSerializable(typeof(DraftComment))]
[JsonSerializable(typeof(IssueTransitionsParams))]
[JsonSerializable(typeof(IssueTransition))]
[JsonSerializable(typeof(IssueTransitionsResult))]
[JsonSerializable(typeof(IssueTransitionParams))]
[JsonSerializable(typeof(IssueTransitionResult))]
[JsonSerializable(typeof(MessagesChangedNotification))]
// Messages.cs (and the members MessageConverter reads and writes by type)
[JsonSerializable(typeof(Address))]
[JsonSerializable(typeof(OutboxInfo))]
[JsonSerializable(typeof(MessageSummary))]
[JsonSerializable(typeof(Attachment))]
[JsonSerializable(typeof(Message))]
[JsonSerializable(typeof(IReadOnlyList<Address>))]
[JsonSerializable(typeof(IReadOnlyList<string>))]
[JsonSerializable(typeof(IReadOnlyList<Attachment>))]
[JsonSerializable(typeof(IReadOnlyDictionary<string, string>))]
[JsonSerializable(typeof(MessageListParams))]
[JsonSerializable(typeof(MessageListResult))]
[JsonSerializable(typeof(MessageGetParams))]
[JsonSerializable(typeof(MessageGetResult))]
[JsonSerializable(typeof(MessageBodyParams))]
[JsonSerializable(typeof(BlockedContent))]
[JsonSerializable(typeof(Link))]
[JsonSerializable(typeof(MessageBodyResult))]
[JsonSerializable(typeof(MessagePartParams))]
[JsonSerializable(typeof(MessagePartResult))]
[JsonSerializable(typeof(MessageEmbeddedParams))]
[JsonSerializable(typeof(MessageEmbeddedResult))]
[JsonSerializable(typeof(MessageDownloadParams))]
[JsonSerializable(typeof(MessageDownloadResult))]
[JsonSerializable(typeof(BulkInfo))]
[JsonSerializable(typeof(UnsubscribeOffer))]
[JsonSerializable(typeof(MessageUnsubscribeParams))]
[JsonSerializable(typeof(MessageUnsubscribeResult))]
[JsonSerializable(typeof(MessageFlagParams))]
[JsonSerializable(typeof(MessageMoveParams))]
[JsonSerializable(typeof(MessageDeleteParams))]
// Notifications.cs
[JsonSerializable(typeof(NewMessageNotification))]
[JsonSerializable(typeof(SyncStateNotification))]
[JsonSerializable(typeof(AuthRequiredNotification))]
[JsonSerializable(typeof(AccountsChangedNotification))]
// RpcError.cs
[JsonSerializable(typeof(RpcError))]
// Search.cs
[JsonSerializable(typeof(SearchQueryParams))]
[JsonSerializable(typeof(MatchRange))]
[JsonSerializable(typeof(SearchResult))]
[JsonSerializable(typeof(SearchQueryResult))]
// Senders.cs
[JsonSerializable(typeof(KnownSender))]
[JsonSerializable(typeof(SenderListResult))]
[JsonSerializable(typeof(SenderAddParams))]
[JsonSerializable(typeof(SenderRemoveParams))]
// Storage.cs
[JsonSerializable(typeof(StorageConversion))]
[JsonSerializable(typeof(SystemStorageResult))]
// Sync.cs
[JsonSerializable(typeof(SyncState))]
[JsonSerializable(typeof(SyncStatusParams))]
[JsonSerializable(typeof(SyncStatusResult))]
[JsonSerializable(typeof(SyncTriggerParams))]
// Threads.cs
[JsonSerializable(typeof(ThreadSummary))]
[JsonSerializable(typeof(ThreadListParams))]
[JsonSerializable(typeof(ThreadListResult))]
[JsonSerializable(typeof(ThreadGetParams))]
[JsonSerializable(typeof(ThreadGetResult))]
// Tls.cs
[JsonSerializable(typeof(TlsErrorReason))]
[JsonSerializable(typeof(CertificateInfo))]
[JsonSerializable(typeof(TlsErrorData))]
public sealed partial class ApiJsonContext : JsonSerializerContext
{
    /// <summary>The context every wire exchange uses: the contract's options.</summary>
    public static ApiJsonContext Wire { get; } = new(CreateWireOptions());

    private static JsonSerializerOptions CreateWireOptions()
    {
        var options = new JsonSerializerOptions
        {
            DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
            Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
            RespectNullableAnnotations = true,
        };
        options.Converters.Add(new Rfc3339Converter());
        return options;
    }
}
