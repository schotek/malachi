// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows addition to the ported API tests: every record of the contract
// decodes from the JSON docs/api.md shows for it and encodes back to the
// same JSON (with the members Swift's encoding adds: empty lists, a zero
// failedOutbox), every null-as-empty list reads null and absence as empty,
// and every wire value of a newer daemon decodes as itself. The cases are
// found by reflection over Malachi.Core.Api, so a record added without a
// sample fails RecordsHaveSamples.

using System;
using System.Collections;
using System.Collections.Generic;
using System.Linq;
using System.Reflection;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.Json.Serialization;
using System.Text.Json.Serialization.Metadata;
using Malachi.Core.Api;
using Xunit;

namespace Malachi.Core.Tests.Api;

public sealed class ApiRoundTripTests
{
    private const string Pin = "abababababababababababababababababababababababababababababababab";

    private const string Server =
        """{"host":"imap.example.org","port":993,"security":"tls","username":"me@example.org","authMethod":"password","certificateSha256":"@pin@"}""";

    private const string OAuth2 =
        """{"source":"daemon","goaAccountId":"account_1","provider":"office365","clientId":"c","tenantId":"common","authUrl":"https://a","tokenUrl":"https://t","scopes":["s1","s2"]}""";

    private const string Config =
        """{"name":"Work","email":"me@example.org","displayName":"Me","kind":"imap","imap":@server@,"smtp":@server@,"oauth2":@oauth2@,"graph":{"source":"goa","goaAccountId":"account_1"},"jira":@jira@,"syncIntervalSeconds":300}""";

    private const string Error = """{"code":1302,"message":"550 no","data":{"limit":1,"size":2}}""";

    private const string State =
        """{"accountId":"acc_1","status":"syncing","folderId":"f_inbox","progress":42,"lastSync":"2026-09-02T10:00:00Z","error":@error@,"pendingOutbox":1,"failedOutbox":2}""";

    private const string Address = """{"name":"Alice","address":"alice@example.org"}""";

    private const string Outbox = """{"state":"failed","attempts":3,"nextAttemptAt":"2026-09-02T12:00:00.5Z","error":@error@}""";

    private const string Summary =
        """{"id":"m_1","accountId":"acc_1","folderId":"f_outbox","threadId":"t_9","from":[@address@],"to":[{"address":"me@example.org"}],"subject":"Lunch","date":"2026-09-02T10:00:00Z","snippet":"plain","flags":["seen","pinned"],"hasAttachments":true,"size":4321,"outbox":@outbox@,"issue":@itemissue@,"bulk":@bulk@}""";

    private const string Bulk = """{"kind":"list","listId":"golang-nuts.googlegroups.com","domain":"googlegroups.com"}""";

    private const string Unsubscribe = """{"method":"url","target":"shop.example","url":"https://shop.example/u?x=1","unsubscribedAt":"2026-09-30T12:00:00Z"}""";

    private const string Attachment =
        """{"partId":"2.1","filename":"image001.png","contentType":"image/png","size":100,"inline":true,"contentId":"image001@example.org","remote":true}""";

    private const string Message =
        """{"id":"m_1","accountId":"acc_1","folderId":"f_inbox","threadId":"t_9","from":[@address@],"to":[@address@],"subject":"Lunch","date":"2026-09-02T10:00:00Z","snippet":"plain","flags":["seen"],"hasAttachments":true,"size":4321,"outbox":@outbox@,"issue":@itemissue@,"cc":[@address@],"bcc":[@address@],"replyTo":[@address@],"rfcMessageId":"<x@example.org>","inReplyTo":"<w@example.org>","references":["<v@example.org>","<w@example.org>"],"attachments":[@attachment@],"headers":{"Auto-Submitted":"no"},"bulk":@bulk@,"unsubscribe":@unsubscribe@}""";

    private const string Blocked =
        """{"remoteImages":3,"remoteStyles":1,"remoteFonts":0,"scripts":1,"forms":0,"eventHandlers":2,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":1}""";

    private const string Body =
        """{"messageId":"m_1","bodyState":"fetched","hasHtml":true,"html":"<p>é & 'x' <b>\u2028</b></p>","htmlWithheld":false,"text":"plain","blocked":@blocked@,"links":[{"text":"Click here","href":"https://real.destination/x"}],"inlineParts":{"image001@example.org":"2.1"},"remotePictures":1,"remoteContent":"block","sanitizerVersion":"1","quotedTrimmed":true}""";

    private const string DraftAttachment =
        """{"id":"att_1","filename":"a.png","contentType":"image/png","size":100,"inline":true,"contentId":"abc@malachi.local"}""";

    private const string Draft =
        """{"id":"d_1","accountId":"acc_1","version":3,"to":[@address@],"cc":[@address@],"bcc":[@address@],"subject":"Re: Lunch","textBody":"> hi","htmlBody":"<p>hi</p>","inReplyTo":"m_1","forwarding":"m_2","attachments":[@draftattachment@],"replaces":"m_9","comment":{"issue":@issue@,"visibility":"internal"},"updatedAt":"2026-09-02T10:00:00.1234567Z"}""";

    private const string Thread =
        """{"id":"t_9","accountId":"acc_1","subject":"Lunch","participants":[@address@],"messageCount":3,"unreadCount":1,"latestDate":"2026-09-02T10:00:00Z","latest":@summary@,"snippet":"plain","flags":["flagged","seen"],"hasAttachments":true,"folderIds":["f_inbox","f_sent"],"issue":@issue@,"sentCount":2}""";

    private const string Certificate =
        """{"sha256":"@pin@","subject":"127.0.0.1","issuer":"CA","dnsNames":["mail.example.org"],"ipAddresses":["127.0.0.1"],"notBefore":"2024-01-02T03:04:05Z","notAfter":"2044-01-02T03:04:05Z","selfSigned":true}""";

    private const string Jira =
        """{"siteUrl":"https://acme.atlassian.net","deployment":"cloud","cloudId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","login":"jana@acme.example","spaces":[{"id":"10001","key":"ITSD","name":"IT Service Desk"},{"id":"10002","key":"WEB"}],"offlineDays":90,"onlyMine":true,"hideEvents":false,"disabledFolders":["watching"],"closedStatuses":[{"id":"6","name":"Closed"},{"id":"10005"}],"notificationMail":"hide","notificationSenders":["@acme.example"],"botNames":["Relay Bot"],"metadataFilters":["^Sent from .*$"],"authorPrefixes":["[EXT]"]}""";

    private const string Issue =
        """{"key":"ITSD-42","url":"https://acme.atlassian.net/browse/ITSD-42","summary":"Printer jams","status":"In Progress","statusCategory":"inProgress","type":"Service Request","priority":"High","assignee":"Jana","reporter":"Petr","assignedToMe":true,"watching":false,"commentVisibilities":["public","internal"]}""";

    // MessageIssue: the issue's members and the item's, in one object (Go embeds IssueInfo).
    private const string ItemIssue =
        """{"key":"ITSD-42","url":"https://acme.atlassian.net/browse/ITSD-42","summary":"Printer jams","status":"In Progress","statusCategory":"inProgress","type":"Service Request","priority":"High","assignee":"Jana","reporter":"Petr","assignedToMe":true,"watching":false,"commentVisibilities":["public","internal"],"item":"event","visibility":"internal","changes":[{"field":"status","from":"To Do","to":"In Progress"},{"field":"assignee","to":"Jana"}],"via":"Relay Bot","edited":true,"mine":false}""";

    private const string Page = """{"cursor":"opaque","limit":50}""";

    private const string PageInfo = """{"nextCursor":"opaque","total":1234}""";

    // Every record: a JSON document and, when the encoding adds members, what it encodes to.
    private static readonly Dictionary<string, Sample> Samples = new(StringComparer.Ordinal)
    {
        // API.cs, Auth.cs
        [nameof(EmptyParams)] = Case<EmptyParams>("{}"),
        [nameof(EmptyResult)] = Case<EmptyResult>("{}"),
        [nameof(SystemHelloParams)] = Case<SystemHelloParams>("""{"clientNonce":"@pin@"}"""),
        [nameof(SystemHelloResult)] = Case<SystemHelloResult>("""{"protocolVersion":2,"daemonNonce":"@pin@","daemonProof":"@pin@"}"""),
        [nameof(SystemAuthenticateParams)] = Case<SystemAuthenticateParams>("""{"clientProof":"@pin@"}"""),
        // Accounts.cs
        [nameof(Malachi.Core.Api.Page)] = Case<Page>(Page),
        [nameof(Malachi.Core.Api.PageInfo)] = Case<PageInfo>(PageInfo),
        [nameof(SystemInfoResult)] = Case<SystemInfoResult>("""{"version":"0.1.0","protocolVersion":2,"pid":4242,"storePath":"/home/u/.local/share/malachi/store.db"}"""),
        [nameof(ServerConfig)] = Case<ServerConfig>(Server),
        [nameof(OAuth2Config)] = Case<OAuth2Config>(OAuth2),
        [nameof(GraphConfig)] = Case<GraphConfig>("""{"source":"goa","goaAccountId":"account_1788512854_0"}"""),
        [nameof(AccountConfig)] = Case<AccountConfig>(Config),
        [nameof(Credentials)] = Case<Credentials>("""{"password":"secret","oauthSession":"s_1"}"""),
        [nameof(Account)] = Case<Account>("""{"id":"acc_1","config":@config@,"enabled":true,"state":@state@,"capabilities":["comment","forward","teleport"]}"""),
        [nameof(AccountListResult)] = Case<AccountListResult>("""{"accounts":[{"id":"acc_1","config":@config@,"enabled":false,"state":@state@}]}"""),
        [nameof(AccountAddParams)] = Case<AccountAddParams>("""{"config":@config@,"credentials":{}}"""),
        [nameof(AccountAddResult)] = Case<AccountAddResult>("""{"accountId":"acc_2"}"""),
        [nameof(AccountRemoveParams)] = Case<AccountRemoveParams>("""{"accountId":"acc_1","deleteLocalData":false}"""),
        [nameof(AccountSetEnabledParams)] = Case<AccountSetEnabledParams>("""{"accountId":"acc_1","enabled":true}"""),
        [nameof(AccountReorderParams)] = Case<AccountReorderParams>("""{"accountIds":["acc_2","acc_1"]}"""),
        [nameof(AccountDiscoverParams)] = Case<AccountDiscoverParams>("""{"email":"me@example.org"}"""),
        [nameof(AccountDiscoverResult)] = Case<AccountDiscoverResult>(
            """{"config":@config@,"source":"ispdb","providerName":"Example","alternatives":[@config@]}"""),
        [nameof(LinkedAccount)] = Case<LinkedAccount>(
            """{"provider":"microsoft365","email":"me@contoso.com","name":"Me","goaAccountId":"account_1","configured":false,"attentionNeeded":true,"config":@config@}"""),
        [nameof(AccountLinkedResult)] = Case<AccountLinkedResult>(
            """{"accounts":[{"provider":"google","email":"me@gmail.com","goaAccountId":"account_2","configured":true,"attentionNeeded":false}]}"""),
        [nameof(OAuthBrowserPage)] = Case<OAuthBrowserPage>(
            """{"successTitle":"Signed in","successText":"You can close this tab.","failureTitle":"Sign-in failed","failureText":"Return to Malachi Mail."}"""),
        [nameof(AccountOAuthStartParams)] = Case<AccountOAuthStartParams>("""{"accountId":"acc_1","config":@config@,"browserPage":{"successTitle":"Signed in"}}"""),
        [nameof(AccountOAuthStartResult)] = Case<AccountOAuthStartResult>(
            """{"sessionId":"s_1","authUrl":"https://accounts.google.com/o/oauth2/v2/auth?x=1&y=2","expiresAt":"2026-09-25T10:10:00Z"}"""),
        [nameof(AccountOAuthWaitParams)] = Case<AccountOAuthWaitParams>("""{"sessionId":"s_1"}"""),
        [nameof(AccountOAuthWaitResult)] = Case<AccountOAuthWaitResult>("""{"status":"complete","config":@config@}"""),
        [nameof(AccountOAuthCancelParams)] = Case<AccountOAuthCancelParams>("""{"sessionId":"s_1"}"""),
        [nameof(AccountUpdateParams)] = Case<AccountUpdateParams>("""{"accountId":"acc_1","config":@config@,"credentials":{"password":"p"}}"""),
        [nameof(AccountTestParams)] = Case<AccountTestParams>("""{"accountId":"acc_1","config":@config@,"credentials":{"oauthSession":"s_1"}}"""),
        [nameof(EndpointTestResult)] = Case<EndpointTestResult>("""{"ok":false,"error":@error@,"capabilities":["IDLE","CONDSTORE"],"latencyMs":120}"""),
        [nameof(AccountTestResult)] = Case<AccountTestResult>(
            """{"imap":{"ok":true,"latencyMs":1},"smtp":{"ok":false,"error":{"code":1201,"message":"535"},"latencyMs":2},"graph":{"ok":true,"capabilities":["graph"],"latencyMs":3},"jira":{"ok":true,"capabilities":["cloud","gateway"],"latencyMs":4}}"""),
        // Attachments.cs
        [nameof(AttachmentImportParams)] = Case<AttachmentImportParams>("""{"accountId":"acc_1","path":"C:\\Users\\u\\a.pdf","data":"AQID","filename":"a.bin","inline":true}"""),
        [nameof(AttachmentImportResult)] = Case<AttachmentImportResult>("""{"attachment":@draftattachment@}"""),
        [nameof(AttachmentRemoveParams)] = Case<AttachmentRemoveParams>("""{"accountId":"acc_1","attachmentId":"att_1"}"""),
        [nameof(AttachmentGetParams)] = Case<AttachmentGetParams>("""{"accountId":"acc_1","attachmentId":"att_1"}"""),
        [nameof(AttachmentGetResult)] = Case<AttachmentGetResult>("""{"attachmentId":"att_1","filename":"a.png","contentType":"image/png","size":3,"data":"AQID"}"""),
        // Config.cs
        [nameof(Preferences)] = Case<Preferences>("""{"syncIntervalSeconds":300,"remoteContent":"knownSenders","offlineDays":30,"compressStore":false,"attachmentOfflineDays":-1,"neverStoreAttachments":true}"""),
        [nameof(ConfigGetResult)] = Case<ConfigGetResult>("""{"preferences":{"syncIntervalSeconds":0,"remoteContent":"block","offlineDays":0}}"""),
        [nameof(ConfigSetParams)] = Case<ConfigSetParams>("""{"preferences":{"syncIntervalSeconds":60,"remoteContent":"allow","offlineDays":3650}}"""),
        [nameof(ConfigSetResult)] = Case<ConfigSetResult>("""{"preferences":{"syncIntervalSeconds":300,"remoteContent":"block","offlineDays":30}}"""),
        // Contacts.cs
        [nameof(Contact)] = Case<Contact>("""{"name":"Alice Example","address":"alice@example.org","source":"addressBook","book":"Contacts"}"""),
        [nameof(ContactSearchParams)] = Case<ContactSearchParams>("""{"accountId":"acc_1","query":"ali","limit":10}"""),
        [nameof(ContactSearchResult)] = Case<ContactSearchResult>("""{"contacts":[{"address":"bob@example.org","source":"sent"}]}"""),
        // Drafts.cs
        [nameof(Malachi.Core.Api.DraftAttachment)] = Case<DraftAttachment>(DraftAttachment),
        [nameof(Malachi.Core.Api.Draft)] = Case<Draft>(Draft),
        [nameof(DraftSaveParams)] = Case<DraftSaveParams>("""{"draft":@draft@}"""),
        [nameof(DraftSaveResult)] = Case<DraftSaveResult>(
            """{"draftId":"d_1","version":2,"textBody":"hi","htmlBody":"<p>hi</p>","blocked":@blocked@,"attachments":[@draftattachment@]}"""),
        [nameof(DraftListParams)] = Case<DraftListParams>("""{"accountId":"acc_1","page":@page@}"""),
        [nameof(DraftListResult)] = Case<DraftListResult>("""{"drafts":[@draft@],"page":@pageinfo@}"""),
        [nameof(DraftDeleteParams)] = Case<DraftDeleteParams>("""{"accountId":"acc_1","draftId":"d_1"}"""),
        [nameof(DraftCreateParams)] = Case<DraftCreateParams>(
            """{"accountId":"acc_1","mode":"replyAll","messageId":"m_1","mailto":"mailto:a@example.org","attribution":"On Tue, Alice wrote:","messageAccountId":"acc_j"}"""),
        [nameof(DraftCreateResult)] = Case<DraftCreateResult>("""{"draft":@draft@,"quoted":"html","blocked":@blocked@,"skipped":[@attachment@]}"""),
        [nameof(DraftOpenParams)] = Case<DraftOpenParams>("""{"accountId":"acc_1","messageId":"m_1"}"""),
        [nameof(DraftOpenResult)] = Case<DraftOpenResult>("""{"draft":@draft@,"blocked":@blocked@,"skipped":[@attachment@]}"""),
        [nameof(DraftMarkdownParams)] = Case<DraftMarkdownParams>("""{"text":"# Plan\n\n- **one**\n- two"}"""),
        [nameof(DraftMarkdownResult)] = Case<DraftMarkdownResult>("""{"markdown":true,"html":"<h1>Plan</h1><ul><li><b>one</b></li><li>two</li></ul>"}"""),
        [nameof(MessageSendParams)] = Case<MessageSendParams>("""{"accountId":"acc_1","draftId":"d_1","version":3}"""),
        [nameof(MessageSendResult)] = Case<MessageSendResult>("""{"outboxId":"m_7"}"""),
        [nameof(OutboxRetryParams)] = Case<OutboxRetryParams>("""{"accountId":"acc_1","messageId":"m_7"}"""),
        // Folders.cs
        [nameof(Folder)] = Case<Folder>(
            """{"id":"f_2","accountId":"acc_1","parentId":"f_1","name":"Sub","path":"Inbox/Sub","role":"none","subscribed":false,"selectable":true,"synced":true,"unread":0,"total":1,"virtual":"assignedToMe"}"""),
        [nameof(FolderListParams)] = Case<FolderListParams>("""{"accountId":"acc_1","includeUnsubscribed":true}"""),
        [nameof(FolderListResult)] = Case<FolderListResult>(
            """{"folders":[{"id":"f_1","accountId":"acc_1","name":"Inbox","path":"Inbox","role":"inbox","subscribed":true,"selectable":true,"synced":true,"unread":3,"total":120}]}"""),
        [nameof(FolderSubscribeParams)] = Case<FolderSubscribeParams>("""{"accountId":"acc_1","folderId":"f_1","subscribed":false}"""),
        // Jira.cs
        [nameof(SpaceRef)] = Case<SpaceRef>("""{"id":"10001","key":"ITSD","name":"IT Service Desk"}"""),
        [nameof(StatusRef)] = Case<StatusRef>("""{"id":"6","name":"Closed"}"""),
        [nameof(JiraConfig)] = Case<JiraConfig>(Jira),
        [nameof(IssueInfo)] = Case<IssueInfo>(Issue),
        [nameof(IssueChange)] = Case<IssueChange>("""{"field":"assignee","from":"Petr","to":"Jana"}"""),
        [nameof(MessageIssue)] = Case<MessageIssue>(ItemIssue),
        [nameof(AccountDetectSiteParams)] = Case<AccountDetectSiteParams>("""{"url":"acme.atlassian.net"}"""),
        [nameof(AccountDetectSiteResult)] = Case<AccountDetectSiteResult>(
            """{"kind":"jira","siteUrl":"https://acme.atlassian.net","deployment":"cloud","cloudId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","title":"Acme","version":"1001.0.0-SNAPSHOT"}"""),
        [nameof(AccountListSpacesParams)] = Case<AccountListSpacesParams>("""{"accountId":"acc_j","config":@config@,"credentials":{"password":"token"},"counts":true}"""),
        [nameof(Space)] = Case<Space>("""{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":120}"""),
        [nameof(IssueStatus)] = Case<IssueStatus>("""{"id":"6","name":"Closed","category":"done"}"""),
        [nameof(SiteUser)] = Case<SiteUser>("""{"name":"Jana Dvořáková","email":"jana@acme.example"}"""),
        [nameof(AccountListSpacesResult)] = Case<AccountListSpacesResult>(
            """{"user":{"name":"Jana"},"spaces":[{"id":"10002","key":"WEB","name":"Website","issues":-1}],"statuses":[{"id":"1","name":"To Do","category":"todo"}]}"""),
        [nameof(DraftComment)] = Case<DraftComment>("""{"issue":@issue@,"visibility":""}"""),
        [nameof(IssueTransitionsParams)] = Case<IssueTransitionsParams>("""{"accountId":"acc_j","messageId":"m_j1"}"""),
        [nameof(IssueTransition)] = Case<IssueTransition>("""{"id":"21","name":"Done","to":"Done","toCategory":"done","needsInput":true}"""),
        [nameof(IssueTransitionsResult)] = Case<IssueTransitionsResult>("""{"issue":@issue@,"transitions":[{"id":"11","name":"Start Progress","to":"In Progress"}]}"""),
        [nameof(IssueTransitionParams)] = Case<IssueTransitionParams>("""{"accountId":"acc_j","messageId":"m_j1","transitionId":"11"}"""),
        [nameof(IssueTransitionResult)] = Case<IssueTransitionResult>("""{"issue":@issue@}"""),
        [nameof(MessagesChangedNotification)] = Case<MessagesChangedNotification>("""{"accountId":"acc_1","folderIds":["f_inbox"]}"""),
        // Messages.cs
        [nameof(Malachi.Core.Api.Address)] = Case<Address>(Address),
        [nameof(OutboxInfo)] = Case<OutboxInfo>(Outbox),
        [nameof(MessageSummary)] = Case<MessageSummary>(Summary),
        [nameof(Malachi.Core.Api.Attachment)] = Case<Attachment>(Attachment),
        [nameof(Malachi.Core.Api.Message)] = Case<Message>(Message),
        [nameof(MessageListParams)] = Case<MessageListParams>(
            """{"accountId":"acc_1","folderId":"f_inbox","page":@page@,"sort":"dateAsc","filter":"unread","unreadOnly":true}"""),
        [nameof(MessageListResult)] = Case<MessageListResult>("""{"messages":[@summary@],"page":@pageinfo@}"""),
        [nameof(MessageGetParams)] = Case<MessageGetParams>("""{"accountId":"acc_1","messageId":"m_1"}"""),
        [nameof(MessageGetResult)] = Case<MessageGetResult>("""{"message":@message@}"""),
        [nameof(MessageBodyParams)] = Case<MessageBodyParams>("""{"accountId":"acc_1","messageId":"m_1","remoteContent":"allow","trimQuoted":true}"""),
        [nameof(BlockedContent)] = Case<BlockedContent>(Blocked),
        [nameof(Link)] = Case<Link>("""{"text":"Click here","href":"https://real.destination/x"}"""),
        [nameof(MessageBodyResult)] = Case<MessageBodyResult>(Body),
        [nameof(MessagePartParams)] = Case<MessagePartParams>("""{"accountId":"acc_1","messageId":"m_1","partId":"2.1"}"""),
        [nameof(MessagePartResult)] = Case<MessagePartResult>("""{"partId":"2.1","contentType":"image/png","filename":"a.png","size":3,"data":"AQID"}"""),
        [nameof(MessageEmbeddedParams)] = Case<MessageEmbeddedParams>("""{"accountId":"acc_1","messageId":"m_1","partId":"3","remoteContent":"block"}"""),
        [nameof(MessageEmbeddedResult)] = Case<MessageEmbeddedResult>("""{"partId":"3","message":@message@,"body":@body@}"""),
        [nameof(MessageDownloadParams)] = Case<MessageDownloadParams>("""{"accountId":"acc_1","messageId":"m_1"}"""),
        [nameof(MessageDownloadResult)] = Case<MessageDownloadResult>("""{"message":@message@}"""),
        [nameof(BulkInfo)] = Case<BulkInfo>(Bulk),
        [nameof(UnsubscribeOffer)] = Case<UnsubscribeOffer>(Unsubscribe),
        [nameof(MessageUnsubscribeParams)] = Case<MessageUnsubscribeParams>("""{"accountId":"acc_1","messageId":"m_1","method":"mailto"}"""),
        [nameof(MessageUnsubscribeResult)] = Case<MessageUnsubscribeResult>(
            """{"outcome":"unverified","url":"https://shop.example/u","mailto":"u@shop.example","unsubscribedAt":"2026-09-30T12:00:00Z"}"""),
        [nameof(MessageFlagParams)] = Case<MessageFlagParams>("""{"accountId":"acc_1","messageIds":["m_1","m_2"],"set":["seen"],"clear":["flagged"]}"""),
        [nameof(MessageMoveParams)] = Case<MessageMoveParams>("""{"accountId":"acc_1","messageIds":["m_1"],"targetFolderId":"f_archive"}"""),
        [nameof(MessageDeleteParams)] = Case<MessageDeleteParams>("""{"accountId":"acc_1","messageIds":["m_1"],"permanent":true}"""),
        // Notifications.cs
        [nameof(NewMessageNotification)] = Case<NewMessageNotification>("""{"accountId":"acc_1","folderId":"f_inbox","message":@summary@}"""),
        [nameof(SyncStateNotification)] = Case<SyncStateNotification>("""{"state":@state@}"""),
        [nameof(AuthRequiredNotification)] = Case<AuthRequiredNotification>(
            """{"accountId":"acc_1","reason":1200,"message":"token expired","authUrl":"https://login.example/x"}"""),
        [nameof(AccountsChangedNotification)] = Case<AccountsChangedNotification>("{}"),
        // RpcError.cs
        [nameof(RpcError)] = Case<RpcError>(Error),
        // Search.cs
        [nameof(SearchQueryParams)] = Case<SearchQueryParams>("""{"accountId":"acc_1","folderId":"f_inbox","query":"from:alice přílohy","page":@page@}"""),
        [nameof(MatchRange)] = Case<MatchRange>("""{"start":13,"end":22}"""),
        [nameof(SearchResult)] = Case<SearchResult>("""{"message":@summary@,"snippet":"…posílám přílohy k faktuře…","ranges":[{"start":13,"end":22}],"score":0}"""),
        [nameof(SearchQueryResult)] = Case<SearchQueryResult>("""{"results":[{"message":@summary@,"snippet":"x","score":0.5}],"page":@pageinfo@}"""),
        // Senders.cs
        [nameof(KnownSender)] = Case<KnownSender>("""{"address":"alice@example.org","source":"user","addedAt":"2026-09-02T10:00:00Z"}"""),
        [nameof(SenderListResult)] = Case<SenderListResult>("""{"senders":[{"address":"alice@example.org","source":"sent","addedAt":"2026-09-02T10:00:00Z"}]}"""),
        [nameof(SenderAddParams)] = Case<SenderAddParams>("""{"address":"Alice <alice@example.org>"}"""),
        [nameof(SenderRemoveParams)] = Case<SenderRemoveParams>("""{"address":"alice@example.org"}"""),
        // Storage.cs
        [nameof(SystemStorageResult)] = Case<SystemStorageResult>(
            """{"totalBytes":734003200,"databaseBytes":44470272,"messageBytes":546700000,"messageUncompressedBytes":909800000,"savedBytes":363100000,"attachmentBytes":250000,"remoteAttachmentBytes":312000000,"messages":3725,"compressedMessages":3725,"partialMessages":410,"conversion":"running"}"""),
        // Sync.cs
        [nameof(SyncState)] = Case<SyncState>(State),
        [nameof(SyncStatusParams)] = Case<SyncStatusParams>("""{"accountId":"acc_1"}"""),
        [nameof(SyncStatusResult)] = Case<SyncStatusResult>("""{"accounts":[@state@]}"""),
        [nameof(SyncTriggerParams)] = Case<SyncTriggerParams>("""{"accountId":"acc_1","folderId":"f_inbox","full":true}"""),
        // Threads.cs
        [nameof(ThreadSummary)] = Case<ThreadSummary>(Thread),
        [nameof(ThreadListParams)] = Case<ThreadListParams>("""{"accountId":"acc_1","folderId":"f_inbox","page":@page@,"sort":"dateDesc","filter":"flagged"}"""),
        [nameof(ThreadListResult)] = Case<ThreadListResult>("""{"threads":[@thread@],"page":@pageinfo@}"""),
        [nameof(ThreadGetParams)] = Case<ThreadGetParams>("""{"accountId":"acc_1","threadId":"t_9","folderId":"f_inbox","withSent":true}"""),
        [nameof(ThreadGetResult)] = Case<ThreadGetResult>("""{"thread":@thread@,"messages":[@summary@,@summary@],"sent":[@summary@]}"""),
        // Tls.cs
        [nameof(CertificateInfo)] = Case<CertificateInfo>(Certificate),
        [nameof(TlsErrorData)] = Case<TlsErrorData>("""{"reason":"pinMismatch","certificate":@certificate@,"expectedSha256":"@pin@"}"""),
    };

    // Minimal documents: what the encoding adds to them.
    private static readonly Dictionary<string, Sample> Minimal = new(StringComparer.Ordinal)
    {
        ["SyncState without failedOutbox"] = Case<SyncState>(
            """{"accountId":"a","status":"idle","progress":-1,"pendingOutbox":0}""",
            """{"accountId":"a","status":"idle","progress":-1,"pendingOutbox":0,"failedOutbox":0}"""),
        ["Message without attachments"] = Case<Message>(
            """{"id":"m","accountId":"a","folderId":"f","from":null,"subject":"","date":"0001-01-01T00:00:00Z","snippet":"","flags":null,"hasAttachments":false,"size":0,"attachments":null}""",
            """{"id":"m","accountId":"a","folderId":"f","from":[],"subject":"","date":"0001-01-01T00:00:00Z","snippet":"","flags":[],"hasAttachments":false,"size":0,"attachments":[]}"""),
        ["CertificateInfo of Go zero values"] = Case<CertificateInfo>(
            "{}",
            """{"sha256":"","dnsNames":[],"ipAddresses":[],"notBefore":"0001-01-01T00:00:00Z","notAfter":"0001-01-01T00:00:00Z","selfSigned":false}"""),
        ["CertificateInfo with nulls"] = Case<CertificateInfo>(
            """{"sha256":null,"subject":null,"dnsNames":null,"ipAddresses":null,"notBefore":null,"notAfter":null,"selfSigned":null}""",
            """{"sha256":"","dnsNames":[],"ipAddresses":[],"notBefore":"0001-01-01T00:00:00Z","notAfter":"0001-01-01T00:00:00Z","selfSigned":false}"""),
        ["RpcError without a message"] = Case<RpcError>("""{"code":1004}""", """{"code":1004,"message":""}"""),
        ["RpcError with a null message and data"] = Case<RpcError>("""{"code":1004,"message":null,"data":null}""", """{"code":1004,"message":""}"""),
        ["Draft of draft.create"] = Case<Draft>(
            """{"accountId":"a","version":0,"to":null,"subject":"","textBody":"","updatedAt":"0001-01-01T00:00:00Z"}""",
            """{"accountId":"a","version":0,"to":[],"subject":"","textBody":"","updatedAt":"0001-01-01T00:00:00Z"}"""),
        // An older daemon's preferences, or a null of a newer one: absent (unchanged to config.set).
        ["Preferences without the storage members"] = Case<Preferences>(
            """{"syncIntervalSeconds":0,"remoteContent":"block","offlineDays":0,"compressStore":null,"neverStoreAttachments":null}""",
            """{"syncIntervalSeconds":0,"remoteContent":"block","offlineDays":0}"""),
        // A daemon from before the user's replies in Sent (2026-10-01): no
        // sentCount (0), no sent (empty); a request that does not ask for
        // them leaves withSent out.
        ["ThreadSummary without sentCount"] = Case<ThreadSummary>(
            """{"id":"t","accountId":"a","subject":"","messageCount":1,"unreadCount":0,"latestDate":"2026-09-02T10:00:00Z","latest":@summary@,"snippet":"","hasAttachments":false}""",
            """{"id":"t","accountId":"a","subject":"","participants":[],"messageCount":1,"unreadCount":0,"latestDate":"2026-09-02T10:00:00Z","latest":@summary@,"snippet":"","flags":[],"hasAttachments":false,"folderIds":[],"sentCount":0}"""),
        ["ThreadGetResult without sent"] = Case<ThreadGetResult>(
            """{"thread":@thread@,"messages":[]}""",
            """{"thread":@thread@,"messages":[],"sent":[]}"""),
        ["ThreadGetParams without withSent"] = Case<ThreadGetParams>(
            """{"accountId":"a","threadId":"t","withSent":null}""",
            """{"accountId":"a","threadId":"t"}"""),
        // trimQuoted is asked for or left out, never false; a daemon that
        // cut nothing, or one from before the parameter, leaves
        // quotedTrimmed out.
        ["MessageBodyParams with trimQuoted false"] = Case<MessageBodyParams>(
            """{"accountId":"a","messageId":"m","trimQuoted":false}""",
            """{"accountId":"a","messageId":"m"}"""),
        ["MessageBodyResult without links"] = Case<MessageBodyResult>(
            """{"messageId":"m","bodyState":"pending","hasHtml":false,"text":"","blocked":@blocked@,"remoteContent":"block","sanitizerVersion":"1"}""",
            """{"messageId":"m","bodyState":"pending","hasHtml":false,"text":"","blocked":@blocked@,"links":[],"remoteContent":"block","sanitizerVersion":"1"}"""),
    };

    public static TheoryData<string> SampleNames => [.. Samples.Keys.Order(StringComparer.Ordinal)];

    public static TheoryData<string> MinimalNames => [.. Minimal.Keys.Order(StringComparer.Ordinal)];

    public static TheoryData<string> WireValueTypes => [.. WireValues().Select(t => t.Name).Order(StringComparer.Ordinal)];

    public static TheoryData<string, string> NullAsEmptyMembers
    {
        get
        {
            var data = new TheoryData<string, string>();
            foreach (var (type, property) in NullAsEmptyProperties())
            {
                data.Add(type.Name, property.GetCustomAttribute<JsonPropertyNameAttribute>()!.Name);
            }
            return data;
        }
    }

    [Theory]
    [MemberData(nameof(SampleNames))]
    public void RecordRoundTrips(string type)
    {
        var sample = Samples[type];
        var encoded = sample.RoundTrip(sample.Json);
        ApiJson.AssertSameJson(sample.Expected ?? sample.Json, encoded);
        // A second trip changes nothing.
        ApiJson.AssertSameJson(encoded, sample.RoundTrip(encoded));
    }

    [Theory]
    [MemberData(nameof(MinimalNames))]
    public void MinimalDocumentRoundTrips(string name)
    {
        var sample = Minimal[name];
        ApiJson.AssertSameJson(sample.Expected!, sample.RoundTrip(sample.Json));
    }

    /// <summary>Every record of Malachi.Core.Api, the notification cases aside, has a sample above.</summary>
    [Fact]
    public void RecordsHaveSamples()
    {
        var records = Records().Select(t => t.Name).Order(StringComparer.Ordinal).ToArray();
        Assert.True(records.Length > 100, $"only {records.Length} records found");
        Assert.Equal(records, Samples.Keys.Order(StringComparer.Ordinal));
    }

    /// <summary>
    /// Every sample names every member of its record, so that the round trip
    /// checks each wire name against docs/api.md (a misspelt name would drop
    /// the member on the way through). Message has its own coding
    /// (MessageConverter): its sample holds the summary's members and its own.
    /// </summary>
    [Theory]
    [MemberData(nameof(SampleNames))]
    public void SampleNamesEveryMember(string type)
    {
        var info = TypeInfo(type);
        var sample = ApiJson.Parse(Samples[type].Json);
        var names = type == nameof(Malachi.Core.Api.Message)
            ? [.. WireNames(TypeInfo(nameof(MessageSummary))),
                "cc", "bcc", "replyTo", "rfcMessageId", "inReplyTo", "references", "attachments", "headers", "unsubscribe"]
            : WireNames(info);
        Assert.Equal(names.Order(StringComparer.Ordinal), ApiJson.Keys(sample));
    }

    // The members of a record on the wire; a [JsonIgnore]d one (a computed
    // property) is listed without a getter.
    private static string[] WireNames(JsonTypeInfo info) => [.. info.Properties.Where(p => p.Get is not null).Select(p => p.Name)];

    /// <summary>Every record's metadata comes from the source-generated context: nothing falls back to reflection.</summary>
    [Fact]
    public void EveryRecordIsDeclaredInTheContext()
    {
        foreach (var type in Records().Concat(WireValues()).Append(typeof(ErrorCode)))
        {
            var info = ApiJsonContext.Wire.GetTypeInfo(type);
            Assert.True(info is not null, $"{type.Name} is not in ApiJsonContext");
            Assert.Same(ApiJsonContext.Wire.Options, info.Options);
        }
    }

    [Theory]
    [MemberData(nameof(NullAsEmptyMembers))]
    public void NullOrAbsentListReadsAsEmpty(string type, string member)
    {
        var info = TypeInfo(type);
        var property = NullAsEmptyProperties().Single(p => p.Type.Name == type && p.Property.GetCustomAttribute<JsonPropertyNameAttribute>()!.Name == member).Property;
        var document = JsonNode.Parse(Samples[type].Json)!.AsObject();

        document[member] = null;
        var withNull = JsonSerializer.Deserialize(document.ToJsonString(), info)!;
        Assert.Empty((IEnumerable)property.GetValue(withNull)!);

        document.Remove(member);
        var absent = JsonSerializer.Deserialize(document.ToJsonString(), info)!;
        Assert.Empty((IEnumerable)property.GetValue(absent)!);

        // Encoded as an array, never null, never left out.
        var encoded = JsonSerializer.SerializeToElement(absent, info);
        Assert.Equal(JsonValueKind.Array, encoded.GetProperty(member).ValueKind);
        Assert.Equal(0, encoded.GetProperty(member).GetArrayLength());
    }

    /// <summary>A null element of a null-as-empty list is refused, as Swift refuses it.</summary>
    [Fact]
    public void NullElementOfAListIsRefused()
    {
        Assert.Throws<JsonException>(() => ApiJson.Decode<FolderListResult>("""{"folders":[null]}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<FolderListResult>("""{"folders":{}}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<ThreadSummary>(Expand(Thread).Replace("\"f_sent\"", "null", StringComparison.Ordinal)));
    }

    /// <summary>A required member absent, or null where the type has no null, fails the decoding (Swift's synthesized decoding).</summary>
    [Fact]
    public void RequiredMembersAreRequired()
    {
        Assert.Throws<JsonException>(() => ApiJson.Decode<Folder>("""{"id":"f","accountId":"a","name":"n","path":"n","role":"inbox","subscribed":true,"selectable":true,"synced":true,"unread":0}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<Folder>("""{"id":"f","accountId":"a","name":null,"path":"n","role":"inbox","subscribed":true,"selectable":true,"synced":true,"unread":0,"total":0}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<Folder>("""{"id":null,"accountId":"a","name":"n","path":"n","role":"inbox","subscribed":true,"selectable":true,"synced":true,"unread":0,"total":0}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<Folder>("""{"id":"f","accountId":"a","name":"n","path":"n","role":7,"subscribed":true,"selectable":true,"synced":true,"unread":0,"total":0}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<Draft>("""{"accountId":"a","version":0,"subject":"","textBody":""}""")); // updatedAt
        Assert.Throws<JsonException>(() => ApiJson.Decode<BlockedContent>("""{"remoteImages":0}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<AccountAddParams>($$$"""{"config":{{{Expand(Config)}}}}""")); // credentials
        Assert.Throws<JsonException>(() => ApiJson.Decode<MessageGetResult>("""{"message":null}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<Message>("""{"id":"m"}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<Message>("[]"));
        Assert.Throws<JsonException>(() => ApiJson.Decode<MessagePartResult>("""{"partId":"2","contentType":"x","filename":"f","size":1,"data":"not base64!"}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<AuthRequiredNotification>("""{"accountId":"a","reason":1200.5,"message":"m"}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<AuthRequiredNotification>("""{"accountId":"a","reason":"1200","message":"m"}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<SyncState>("null"));
    }

    /// <summary>A value of a newer daemon decodes as itself and goes back unchanged (docs/api.md §6).</summary>
    [Theory]
    [MemberData(nameof(WireValueTypes))]
    public void WireValueOfANewerDaemonDecodesAsItself(string type)
    {
        var info = TypeInfo(type);
        var value = JsonSerializer.Deserialize("\"somethingNewer\"", info)!;
        Assert.Equal("somethingNewer", value.ToString());
        Assert.Equal("somethingNewer", info.Type.GetProperty("Value")!.GetValue(value));
        Assert.Equal("\"somethingNewer\"", JsonSerializer.Serialize(value, info));
        foreach (var known in GoContractTests.ConstValues(info.Type))
        {
            Assert.Equal($"\"{known}\"", JsonSerializer.Serialize(JsonSerializer.Deserialize($"\"{known}\"", info), info));
        }
        foreach (var bad in new[] { "null", "1", "true", "[]", "{}" })
        {
            Assert.Throws<JsonException>(() => JsonSerializer.Deserialize(bad, info));
        }
    }

    /// <summary>
    /// HTML and non-ASCII text go out unescaped, through the context and
    /// through a writer made with JsonCoding.WriterOptions (a request line);
    /// the context's own Default differs in the escaping only.
    /// </summary>
    [Fact]
    public void TextIsWrittenUnescaped()
    {
        var parameters = new SenderAddParams { Address = "Příliš <b>žluťoučký</b> & 'kůň'" };
        Assert.Equal("""{"address":"Příliš <b>žluťoučký</b> & 'kůň'"}""", JsonCoding.EncodeToString(parameters));
        using var buffer = new System.IO.MemoryStream();
        using (var writer = new Utf8JsonWriter(buffer, JsonCoding.WriterOptions))
        {
            writer.WriteStartObject();
            writer.WritePropertyName("params");
            JsonSerializer.Serialize(writer, parameters, API.SenderAdd.ParamsInfo);
            writer.WriteEndObject();
        }
        Assert.Equal("""{"params":{"address":"Příliš <b>žluťoučký</b> & 'kůň'"}}""", System.Text.Encoding.UTF8.GetString(buffer.ToArray()));
        // Line and paragraph separators stay escaped: JavaScript reads them as line ends.
        Assert.Equal("""{"address":"a\u2028b"}""", JsonCoding.EncodeToString(new SenderAddParams { Address = "a\u2028b" }));

        var sender = new KnownSender { Address = "<a>", Source = KnownSenderSource.User, AddedAt = DateTimeOffset.FromUnixTimeSeconds(1_788_343_200) };
        ApiJson.AssertSameJson(JsonCoding.EncodeToString(sender), JsonSerializer.Serialize(sender, ApiJsonContext.Default.KnownSender));
        Assert.Contains("2026-09-02T10:00:00Z", JsonSerializer.Serialize(sender, ApiJsonContext.Default.KnownSender), StringComparison.Ordinal);
        Assert.Contains("\\u003Ca\\u003E", JsonSerializer.Serialize(sender, ApiJsonContext.Default.KnownSender), StringComparison.Ordinal);
    }

    [Fact]
    public void ErrorCodesDecodeFromIntegersOnly()
    {
        Assert.Equal(new ErrorCode(1234), JsonCoding.Decode<ErrorCode>("1234"));
        Assert.Equal("1502", JsonCoding.EncodeToString<ErrorCode>(ErrorCode.AttachmentTooBig));
        foreach (var bad in new[] { "null", "\"1502\"", "1502.5", "1e3", "99999999999", "[]" })
        {
            Assert.Throws<JsonException>(() => JsonCoding.Decode<ErrorCode>(bad));
        }
    }

    /// <summary>
    /// RpcError.Data outlives the document it came from and compares by its
    /// JSON content, as Swift's JSONValue does; AttachmentTooBig follows
    /// JSONValue.intValue.
    /// </summary>
    [Fact]
    public void ErrorDataIsOwnedAndComparedByContent()
    {
        RpcError error;
        using (var document = JsonDocument.Parse("""{"code":1502,"message":"too big","data":{"size":2.0,"limit":1}}"""))
        {
            var root = document.RootElement;
            error = new RpcError { Code = ErrorCode.AttachmentTooBig, Message = "too big", Data = root.GetProperty("data") };
        }
        Assert.Equal(new SizeLimit(1, 2), error.AttachmentTooBig);
        var same = ApiJson.Decode<RpcError>("""{"code":1502,"message":"too big","data":{"limit":1,"size":2.0}}""");
        Assert.Equal(same, error);
        Assert.Equal(same.GetHashCode(), error.GetHashCode());
        Assert.NotEqual(same with { Data = ApiJson.Parse("""{"limit":1,"size":3}""") }, error);
        Assert.NotEqual(same with { Data = null }, error);
        Assert.NotEqual(same with { Message = "other" }, error);
        Assert.Equal("too big (1502)", error.ToString());
        Assert.Null((same with { Data = ApiJson.Parse("""{"limit":1.5}""") }).AttachmentTooBig);
        Assert.Null((same with { Data = ApiJson.Parse("""{"limit":"1"}""") }).AttachmentTooBig);
        Assert.Null((same with { Data = ApiJson.Parse("""{"limit":1e30}""") }).AttachmentTooBig);
        Assert.Null((same with { Data = default(JsonElement) }).Data);
    }

    private static Sample Case<T>(string json, string? expected = null) =>
        new(document => JsonCoding.EncodeToString(JsonCoding.Decode<T>(document)), Expand(json), expected is null ? null : Expand(expected));

    // The documents above name their parts @part@; this puts the parts in.
    private static string Expand(string json)
    {
        var parts = new (string Name, string Json)[]
        {
            ("@certificate@", Certificate), ("@draftattachment@", DraftAttachment), ("@attachment@", Attachment),
            ("@message@", Message), ("@thread@", Thread), ("@summary@", Summary), ("@outbox@", Outbox),
            ("@draft@", Draft), ("@body@", Body), ("@blocked@", Blocked), ("@config@", Config), ("@server@", Server),
            ("@oauth2@", OAuth2), ("@state@", State), ("@error@", Error), ("@address@", Address),
            ("@pageinfo@", PageInfo), ("@page@", Page), ("@pin@", Pin),
            ("@jira@", Jira), ("@itemissue@", ItemIssue), ("@issue@", Issue),
            ("@bulk@", Bulk), ("@unsubscribe@", Unsubscribe),
        };
        string previous;
        do
        {
            previous = json;
            foreach (var (name, part) in parts)
            {
                json = json.Replace(name, part, StringComparison.Ordinal);
            }
        }
        while (json != previous);
        return json;
    }

    private static JsonTypeInfo TypeInfo(string type) =>
        JsonCoding.Options.GetTypeInfo(Records().Concat(WireValues()).Single(t => t.Name == type));

    // The records of the contract: the classes of Malachi.Core.Api with a
    // record's EqualityContract, the cases of DaemonNotification aside.
    private static IEnumerable<Type> Records() =>
        typeof(API).Assembly.GetTypes().Where(t =>
            t.Namespace == "Malachi.Core.Api" && t.IsClass && t.IsPublic && !t.IsAbstract
            && t.GetProperty("EqualityContract", BindingFlags.NonPublic | BindingFlags.Instance) is not null);

    // The wire enums and identifiers: the value types over a wire string.
    private static IEnumerable<Type> WireValues() =>
        typeof(API).Assembly.GetTypes().Where(t =>
            t.IsValueType && t.GetInterfaces().Any(i => i.IsGenericType && i.GetGenericTypeDefinition() == typeof(IStringWireValue<>)));

    private static IEnumerable<(Type Type, PropertyInfo Property)> NullAsEmptyProperties() =>
        from type in Records()
        from property in type.GetProperties()
        let converter = property.GetCustomAttribute<JsonConverterAttribute>()?.ConverterType
        where converter is { IsGenericType: true } && converter.GetGenericTypeDefinition() == typeof(NullAsEmptyListConverter<>)
        select (type, property);

    private sealed record Sample(Func<string, string> RoundTrip, string Json, string? Expected);
}
