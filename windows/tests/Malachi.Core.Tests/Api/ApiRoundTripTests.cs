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
        """{"name":"Work","email":"me@example.org","displayName":"Me","kind":"imap","imap":@server@,"smtp":@server@,"oauth2":@oauth2@,"graph":{"source":"goa","goaAccountId":"account_1"},"jira":@jira@,"caldav":@calcaldavconfig@,"calendar":@calconfig@,"syncIntervalSeconds":300}""";

    private const string Error = """{"code":1302,"message":"550 no","data":{"limit":1,"size":2}}""";

    private const string State =
        """{"accountId":"acc_1","status":"syncing","folderId":"f_inbox","progress":42,"lastSync":"2026-09-02T10:00:00Z","error":@error@,"pendingOutbox":1,"failedOutbox":2}""";

    private const string Address = """{"name":"Alice","address":"alice@example.org"}""";

    private const string Outbox = """{"state":"failed","attempts":3,"nextAttemptAt":"2026-09-02T12:00:00.5Z","error":@error@}""";

    private const string Summary =
        """{"id":"m_1","accountId":"acc_1","folderId":"f_outbox","threadId":"t_9","from":[@address@],"to":[{"address":"me@example.org"}],"subject":"Lunch","date":"2026-09-02T10:00:00Z","snippet":"plain","flags":["seen","pinned"],"hasAttachments":true,"size":4321,"outbox":@outbox@,"issue":@itemissue@,"bulk":@bulk@,"invitation":@calinvitationsummary@}""";

    private const string Bulk = """{"kind":"list","listId":"golang-nuts.googlegroups.com","domain":"googlegroups.com"}""";

    private const string Unsubscribe = """{"method":"url","target":"shop.example","url":"https://shop.example/u?x=1","unsubscribedAt":"2026-09-30T12:00:00Z"}""";

    private const string Attachment =
        """{"partId":"2.1","filename":"image001.png","contentType":"image/png","size":100,"inline":true,"contentId":"image001@example.org","remote":true}""";

    private const string Message =
        """{"id":"m_1","accountId":"acc_1","folderId":"f_inbox","threadId":"t_9","from":[@address@],"to":[@address@],"subject":"Lunch","date":"2026-09-02T10:00:00Z","snippet":"plain","flags":["seen"],"hasAttachments":true,"size":4321,"outbox":@outbox@,"issue":@itemissue@,"cc":[@address@],"bcc":[@address@],"replyTo":[@address@],"rfcMessageId":"<x@example.org>","inReplyTo":"<w@example.org>","references":["<v@example.org>","<w@example.org>"],"attachments":[@attachment@],"headers":{"Auto-Submitted":"no"},"bulk":@bulk@,"unsubscribe":@unsubscribe@,"invitation":@calinvitationcard@}""";

    private const string Blocked =
        """{"remoteImages":3,"remoteStyles":1,"remoteFonts":0,"scripts":1,"forms":0,"eventHandlers":2,"dangerousUrls":0,"embeddedFrames":0,"trackingPixels":1}""";

    private const string Body =
        """{"messageId":"m_1","bodyState":"fetched","hasHtml":true,"html":"<p>é & 'x' <b>\u2028</b></p>","htmlWithheld":false,"text":"plain","blocked":@blocked@,"links":[{"text":"Click here","href":"https://real.destination/x"}],"inlineParts":{"image001@example.org":"2.1"},"remotePictures":1,"remoteContent":"block","sanitizerVersion":"1","quotedTrimmed":true}""";

    private const string DraftAttachment =
        """{"id":"att_1","filename":"a.png","contentType":"image/png","size":100,"inline":true,"contentId":"abc@malachi.local"}""";

    private const string Draft =
        """{"id":"d_1","accountId":"acc_1","version":3,"to":[@address@],"cc":[@address@],"bcc":[@address@],"subject":"Re: Lunch","textBody":"> hi","htmlBody":"<p>hi</p>","inReplyTo":"m_1","forwarding":"m_2","attachments":[@draftattachment@],"replaces":"m_9","comment":{"issue":@issue@,"visibility":"internal"},"local":true,"updatedAt":"2026-09-02T10:00:00.1234567Z"}""";

    private const string Thread =
        """{"id":"t_9","accountId":"acc_1","subject":"Lunch","participants":[@address@],"messageCount":3,"unreadCount":1,"latestDate":"2026-09-02T10:00:00Z","latest":@summary@,"snippet":"plain","flags":["flagged","seen"],"hasAttachments":true,"folderIds":["f_inbox","f_sent"],"issue":@issue@,"sentCount":2,"draftCount":1}""";

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

    private const string CaseId = "c_0123456789abcdef0123456789abcdef";

    private const string BoardIssue = """{"key":"ITSD-42","status":"In Progress","statusCategory":"inProgress"}""";

    private const string BoardDue = """{"at":"2026-10-02T15:00:00Z","quote":"Please send the signed contract by Friday 3 pm.","messageId":"m_1"}""";

    private const string BoardAnnotation =
        """{"state":"you","title":"Signed contract for Alice","summary":"Alice needs the signed contract.\nShe asks for Friday.","why":"Alice asked you directly.","tasks":["Sign the contract","Send it to Alice"],"due":@boarddue@,"source":"claude-opus","at":"2026-09-30T08:30:00Z","stale":true}""";

    private const string BoardCase =
        """{"id":"@caseid@","accountId":"acc_1","threadId":"t_9","ruleState":"you","ruleReason":"you.addressed","userState":"hot","annotation":@boardannotation@,"visibility":"snoozed","doneAt":"2026-09-29T10:00:00Z","remindAt":"2026-10-02T07:00:00Z","remindedAt":"2026-10-02T07:00:05Z","subject":"Contract","person":@address@,"date":"2026-09-30T08:00:00Z","snippet":"Could you sign","unread":true,"hasAttachments":true,"messageCount":3,"replyMessageId":"m_1","replyFolderId":"f_inbox","latestMessageId":"m_2","issue":@boardissue@,"canArchive":true,"draft":{"draftId":"d_1","text":"Signed, attached.","updated":"2026-09-30T09:00:00Z"},"version":4294967296}""";

    private const string BoardCommitment =
        """{"id":"k_1","caseId":"@caseid@","accountId":"acc_1","messageId":"m_3","text":"Send the report","quote":"I will send the report on Monday.","due":"2026-10-05T09:00:00Z","state":"closed","closedReason":"replied","at":"2026-09-30T08:31:00Z"}""";

    private const string BoardMessage =
        """{"id":"m_1","folderId":"f_inbox","from":@address@,"date":"2026-09-30T08:00:00Z","mine":false,"text":"Could you sign the contract?","trimmed":true}""";

    private const string BoardRun =
        """{"at":"2026-09-30T08:00:00Z","endedAt":"2026-09-30T08:02:00Z","trigger":"auto","source":"claude-opus","annotated":3,"error":"timeout"}""";

    private const string BoardMoved = """{"messageId":"m_1","fromFolderId":"f_inbox"}""";

    private const string BoardUsage =
        """{"inputTokens":1200,"outputTokens":340,"cacheCreationInputTokens":5,"cacheReadInputTokens":1000000000000,"lowerBound":true}""";

    private const string BoardUsageTotal =
        """{"inputTokens":2400,"outputTokens":680,"cacheCreationInputTokens":10,"cacheReadInputTokens":2000000000000,"runs":2,"lowerBound":true}""";

    private const string BoardTriage = """{"lastRun":@boardrun@,"annotatedTodayAuto":3,"queue":5,"usage24h":@boardusagetotal@}""";

    private const string BoardWindows = """{"hot":90,"you":30,"them":30,"info":14}""";

    private const string BoardPreferences =
        """{"enabled":true,"assistant":true,"windows":@boardwindows@,"triageAccounts":["acc_1","acc_j"],"autoTriage":true,"autoTriageMinutes":30,"autoTriageDailyCases":60}""";

    private const string BoardQueueMessage =
        """{"messageId":"m_1","from":@address@,"to":[{"address":"me@example.org"}],"cc":[@address@],"date":"2026-09-30T08:00:00Z","mine":false,"text":"Could you sign?","truncated":true}""";

    private const string BoardQueueItem =
        """{"caseId":"@caseid@","accountId":"acc_1","inputKey":"0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f","ruleState":"you","ruleReason":"you.addressed","userState":"them","subject":"Contract","replyMessageId":"m_1","issue":@boardissue@,"own":["me@example.org"],"messages":[@boardqueuemessage@],"hasDraft":true,"commitments":[@boardcommitment@]}""";

    // The calendar (Calendar.cs): every member named, optional ones present.

    private const string CalCalDavConfig =
        """{"serverUrl":"https://cloud.example.org/remote.php/dav","principalUrl":"https://cloud.example.org/remote.php/dav/principals/users/jana/","homeSetUrl":"https://cloud.example.org/remote.php/dav/calendars/jana/","username":"jana","certificateSha256":"@pin@","mailAccountId":"acc_2","scheduling":"client"}""";

    private const string CalConfig =
        """{"sync":true,"disabledCalendars":["/remote.php/dav/calendars/jana/old/"],"defaultCalendar":"/remote.php/dav/calendars/jana/personal/","keepDays":365,"showUnanswered":false,"invitationCard":true,"afterAnswer":"archive"}""";

    private const string CalDavCalendar =
        """{"href":"/remote.php/dav/calendars/jana/personal/","name":"Personal","color":"#0082c9","access":"owner","isDefault":true}""";

    private const string CalCalendar =
        """{"id":"cal_1","accountId":"acc_1","remoteId":"AAMk-1","name":"Calendar","color":"#1f6feb","serverColor":"#1f6feb","userColor":"#e5534b","visible":true,"access":"owner","kind":"events","owner":@address@,"isDefault":true,"timeZone":"Europe/Prague","onlineMeetingProviders":["teamsForBusiness"],"disabled":true}""";

    private const string CalFeatures =
        """{"freeBusy":true,"proposeNewTime":true,"invitations":"mail","mailAccountId":"acc_2","onlineMeetings":true}""";

    private const string CalSyncState =
        """{"accountId":"acc_1","status":"error","progress":-1,"lastSync":"2026-10-14T09:00:00Z","error":@error@}""";

    private const string CalWindow =
        """{"from":"2025-10-14","to":"2027-11-18"}""";

    private const string CalAccount =
        """{"accountId":"acc_1","source":"graph","sync":true,"state":@calsyncstate@,"features":@calfeatures@,"window":@calwindow@,"calendars":[@calcalendar@]}""";

    private const string CalRecurrence =
        """{"frequency":"monthly","interval":2,"byDay":["2TU"],"byMonthDay":[14,-1],"byMonth":[10],"until":"2027-10-14","count":10,"weekStart":"MO","complex":true,"exceptions":3}""";

    private const string CalAttendee =
        """{"name":"Jana Nováková","address":"jana@example.org","role":"required","response":"accepted","type":"person","me":true,"organizer":true}""";

    private const string CalVideoCall =
        """{"url":"https://teams.microsoft.com/l/meetup-join/x","provider":"teams","host":"teams.microsoft.com"}""";

    private const string CalMessageRef =
        """{"accountId":"acc_1","messageId":"m_1"}""";

    private const string CalOccurrenceMembers =
        """"eventId":"ev_1","recurrenceId":"2026-10-14T08:00:00Z","seriesId":"ev_0","calendarId":"cal_1","accountId":"acc_1","title":"Standup","start":"2026-10-14T08:00:00Z","end":"2026-10-14T08:30:00Z","startDate":"2026-10-14","endDate":"2026-10-15","allDay":false,"floating":true,"timeZone":"Europe/Prague","location":"Room B","status":"confirmed","showAs":"busy","myResponse":"needsAction","organizer":true,"attendeeCount":4,"recurring":true,"videoCall":true,"private":true,"alert":true,"readOnly":true,"invitation":@calmessageref@"""";

    private const string CalOccurrence =
        """{@caloccurrencemembers@}""";

    private const string CalEvent =
        """{@caloccurrencemembers@,"etag":"v1-abc","notes":"Agenda","links":["https://example.org/a"],"videoCallLink":@calvideocall@,"organizerAddress":@address@,"attendees":[@calattendee@],"attendeesTruncated":true,"recurrence":@calrecurrence@,"alerts":[15,0],"responseRequested":true,"canEdit":true,"canDelete":true,"canRespond":true,"canProposeNewTime":true,"removeIsDelete":true,"invitationMessages":[@calmessageref@],"created":"2026-10-01T10:00:00Z","updated":"2026-10-02T10:00:00Z"}""";

    private const string CalInputAttendee =
        """{"name":"Jana","address":"jana@example.org","role":"optional"}""";

    private const string CalVideoCallInput =
        """{"mode":"create","provider":"teamsForBusiness","url":"https://example.org/meet"}""";

    private const string CalEventInput =
        """{"title":"Standup","allDay":false,"start":"2026-10-14T10:00:00","end":"2026-10-14T10:30:00","timeZone":"Europe/Prague","startDate":"2026-10-14","endDate":"2026-10-15","location":"Room B","notes":"Agenda","recurrence":@calrecurrence@,"keepRecurrence":true,"alerts":[15],"showAs":"busy","private":true,"attendees":[@calinputattendee@],"videoCall":@calvideocallinput@,"attachments":["att_1"]}""";

    private const string CalMailText =
        """{"subject":"Invitation: Standup","text":"Jana invites you."}""";

    private const string CalBusy =
        """{"start":"2026-10-14T08:00:00Z","end":"2026-10-14T08:30:00Z","showAs":"oof"}""";

    private const string CalAvailability =
        """{"address":"jana@example.org","known":true,"busy":[@calbusy@]}""";

    private const string CalSlot =
        """{"start":"2026-10-14T08:00:00Z","end":"2026-10-14T08:30:00Z"}""";

    private const string CalAlert =
        """{"alertId":"al_1","state":"due","eventId":"ev_1","recurrenceId":"2026-10-14","accountId":"acc_1","calendarId":"cal_1","title":"Standup","start":"2026-10-14T08:00:00Z","end":"2026-10-14T08:30:00Z","startDate":"2026-10-14","allDay":false,"location":"Room B","videoCall":true,"minutesBefore":15}""";

    private const string CalWorkingHours =
        """{"days":["MO","TU"],"start":"08:00","end":"17:00"}""";

    private const string CalPreferences =
        """{"weekStart":"monday","defaultDurationMinutes":60,"defaultAlertMinutes":15,"showDeclined":true,"workingHours":@calworkinghours@}""";

    private const string CalInvitationsSent =
        """{"via":"mail","count":3}""";

    private const string CalInvitationSummaryMembers =
        """"method":"request","kind":"update","state":"unanswered","title":"Workshop","start":"2026-10-14T08:00:00Z","end":"2026-10-14T08:30:00Z","startDate":"2026-10-14","allDay":true,"organizer":@address@,"replyFrom":@address@,"ownEvent":true"""";

    private const string CalInvitationSummary =
        """{@calinvitationsummarymembers@}""";

    private const string CalInvitationMe =
        """{"address":"me@example.org","response":"needsAction"}""";

    private const string CalInvitationPrevious =
        """{"start":"2026-10-13T08:00:00Z","end":"2026-10-13T08:30:00Z","location":"Room A","recurrence":@calrecurrence@}""";

    private const string CalInvitationConflict =
        """{"title":"Lunch","start":"2026-10-14T08:00:00Z","end":"2026-10-14T08:30:00Z","calendarId":"cal_1","eventId":"ev_2"}""";

    private const string CalDayBusy =
        """{"start":"2026-10-14T08:00:00Z","end":"2026-10-14T08:30:00Z","calendarId":"cal_1"}""";

    private const string CalInvitationDay =
        """{"from":"2026-10-14T00:00:00Z","to":"2026-10-15T00:00:00Z","busy":[@caldaybusy@]}""";

    private const string CalInvitationTarget =
        """{"calendarId":"cal_1","isDefault":true}""";

    private const string CalInvitationCard =
        """{@calinvitationsummarymembers@,"partId":"2","uid":"abc@example.org","recurrenceId":"2026-10-14T08:00:00Z","sequence":2,"endDate":"2026-10-15","timeZone":"Europe/Prague","recurrence":@calrecurrence@,"location":"Room B","videoCall":@calvideocall@,"notes":"Agenda","organizerMismatch":true,"attendeeCount":4,"attendees":[@calattendee@],"me":@calinvitationme@,"response":"needsAction","respondedAt":"2026-10-14T09:00:00Z","eventId":"ev_1","calendarId":"cal_1","previous":@calinvitationprevious@,"conflicts":[@calinvitationconflict@],"day":@calinvitationday@,"targets":[@calinvitationtarget@],"fixedTarget":true,"canRespond":true,"canProposeNewTime":true,"replyVia":"mail","replyAccountId":"acc_2","replyTo":@address@,"replyApplied":true}""";

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
        [nameof(AccountOAuthStartParams)] = Case<AccountOAuthStartParams>("""{"accountId":"acc_1","config":@config@,"browserPage":{"successTitle":"Signed in"},"calendar":true}"""),
        [nameof(AccountOAuthStartResult)] = Case<AccountOAuthStartResult>(
            """{"sessionId":"s_1","authUrl":"https://accounts.google.com/o/oauth2/v2/auth?x=1&y=2","expiresAt":"2026-09-25T10:10:00Z"}"""),
        [nameof(AccountOAuthWaitParams)] = Case<AccountOAuthWaitParams>("""{"sessionId":"s_1"}"""),
        [nameof(AccountOAuthWaitResult)] = Case<AccountOAuthWaitResult>("""{"status":"complete","config":@config@}"""),
        [nameof(AccountOAuthCancelParams)] = Case<AccountOAuthCancelParams>("""{"sessionId":"s_1"}"""),
        [nameof(AccountUpdateParams)] = Case<AccountUpdateParams>("""{"accountId":"acc_1","config":@config@,"credentials":{"password":"p"}}"""),
        [nameof(AccountTestParams)] = Case<AccountTestParams>("""{"accountId":"acc_1","config":@config@,"credentials":{"oauthSession":"s_1"}}"""),
        [nameof(EndpointTestResult)] = Case<EndpointTestResult>("""{"ok":false,"error":@error@,"capabilities":["IDLE","CONDSTORE"],"latencyMs":120}"""),
        [nameof(AccountTestResult)] = Case<AccountTestResult>(
            """{"imap":{"ok":true,"latencyMs":1},"smtp":{"ok":false,"error":{"code":1201,"message":"535"},"latencyMs":2},"graph":{"ok":true,"capabilities":["graph"],"latencyMs":3},"jira":{"ok":true,"capabilities":["cloud","gateway"],"latencyMs":4},"caldav":{"ok":true,"capabilities":["calendar-access","sync-collection"],"latencyMs":5}}"""),
        // Attachments.cs
        [nameof(AttachmentImportParams)] = Case<AttachmentImportParams>("""{"accountId":"acc_1","path":"C:\\Users\\u\\a.pdf","data":"AQID","filename":"a.bin","inline":true}"""),
        [nameof(AttachmentImportResult)] = Case<AttachmentImportResult>("""{"attachment":@draftattachment@}"""),
        [nameof(AttachmentRemoveParams)] = Case<AttachmentRemoveParams>("""{"accountId":"acc_1","attachmentId":"att_1"}"""),
        [nameof(AttachmentGetParams)] = Case<AttachmentGetParams>("""{"accountId":"acc_1","attachmentId":"att_1"}"""),
        [nameof(AttachmentGetResult)] = Case<AttachmentGetResult>("""{"attachmentId":"att_1","filename":"a.png","contentType":"image/png","size":3,"data":"AQID"}"""),
        // Board.cs
        [nameof(Malachi.Core.Api.BoardCase)] = Case<BoardCase>(BoardCase),
        [nameof(Malachi.Core.Api.BoardIssue)] = Case<BoardIssue>(BoardIssue),
        [nameof(BoardDraft)] = Case<BoardDraft>("""{"draftId":"d_1","text":"Signed, attached.","updated":"2026-09-30T09:00:00.5Z"}"""),
        [nameof(Malachi.Core.Api.BoardAnnotation)] = Case<BoardAnnotation>(BoardAnnotation),
        [nameof(Malachi.Core.Api.BoardDue)] = Case<BoardDue>(BoardDue),
        [nameof(Malachi.Core.Api.BoardCommitment)] = Case<BoardCommitment>(BoardCommitment),
        [nameof(Malachi.Core.Api.BoardMessage)] = Case<BoardMessage>(BoardMessage),
        [nameof(Malachi.Core.Api.BoardRun)] = Case<BoardRun>(BoardRun),
        [nameof(Malachi.Core.Api.BoardTriage)] = Case<BoardTriage>(BoardTriage),
        [nameof(Malachi.Core.Api.BoardUsage)] = Case<BoardUsage>(BoardUsage),
        [nameof(Malachi.Core.Api.BoardUsageTotal)] = Case<BoardUsageTotal>(BoardUsageTotal),
        [nameof(Malachi.Core.Api.BoardWindows)] = Case<BoardWindows>(BoardWindows),
        [nameof(Malachi.Core.Api.BoardPreferences)] = Case<BoardPreferences>(BoardPreferences),
        [nameof(QuoteNotFoundData)] = Case<QuoteNotFoundData>("""{"field":"due"}"""),
        [nameof(BoardListParams)] = Case<BoardListParams>("""{"accountIds":["acc_1","acc_j"]}"""),
        [nameof(BoardListResult)] = Case<BoardListResult>(
            """{"cases":[@boardcase@],"commitments":[@boardcommitment@],"enabled":true,"assistant":false,"triage":@boardtriage@,"ready":true,"truncated":true}"""),
        [nameof(BoardGetParams)] = Case<BoardGetParams>("""{"caseId":"@caseid@"}"""),
        [nameof(BoardGetResult)] = Case<BoardGetResult>("""{"case":@boardcase@,"messages":[@boardmessage@,@boardmessage@]}"""),
        [nameof(BoardSetStateParams)] = Case<BoardSetStateParams>("""{"caseId":"@caseid@","state":"them"}"""),
        [nameof(BoardSetStateResult)] = Case<BoardSetStateResult>("""{"case":@boardcase@}"""),
        [nameof(BoardSetDoneParams)] = Case<BoardSetDoneParams>("""{"caseId":"@caseid@","done":false}"""),
        [nameof(BoardSetDoneResult)] = Case<BoardSetDoneResult>("""{"case":@boardcase@}"""),
        [nameof(BoardRemindParams)] = Case<BoardRemindParams>("""{"caseId":"@caseid@","until":"2026-10-02T07:00:00Z"}"""),
        [nameof(BoardRemindResult)] = Case<BoardRemindResult>("""{"case":@boardcase@}"""),
        [nameof(BoardArchiveParams)] = Case<BoardArchiveParams>("""{"caseId":"@caseid@"}"""),
        [nameof(BoardArchiveResult)] = Case<BoardArchiveResult>("""{"archived":0,"noArchive":true,"case":@boardcase@,"moved":[@boardmoved@]}"""),
        [nameof(BoardTrashParams)] = Case<BoardTrashParams>("""{"caseId":"@caseid@"}"""),
        [nameof(BoardTrashResult)] = Case<BoardTrashResult>("""{"trashed":2,"case":@boardcase@,"moved":[@boardmoved@]}"""),
        [nameof(Malachi.Core.Api.BoardMoved)] = Case<BoardMoved>(BoardMoved),
        [nameof(BoardUnflagParams)] = Case<BoardUnflagParams>("""{"caseId":"@caseid@"}"""),
        [nameof(BoardUnflagResult)] = Case<BoardUnflagResult>("""{"case":@boardcase@,"unflagged":2}"""),
        [nameof(BoardDiscardDraftParams)] = Case<BoardDiscardDraftParams>("""{"caseId":"@caseid@"}"""),
        [nameof(BoardDiscardDraftResult)] = Case<BoardDiscardDraftResult>("""{"case":@boardcase@}"""),
        [nameof(BoardSetDraftParams)] = Case<BoardSetDraftParams>("""{"caseId":"@caseid@","draftId":"d_1"}"""),
        [nameof(BoardSetDraftResult)] = Case<BoardSetDraftResult>("""{"case":@boardcase@}"""),
        [nameof(BoardQueueParams)] = Case<BoardQueueParams>("""{"accountIds":["acc_1"],"caseIds":["@caseid@"],"limit":5}"""),
        [nameof(BoardQueueResult)] = Case<BoardQueueResult>("""{"items":[@boardqueueitem@],"remaining":7}"""),
        [nameof(Malachi.Core.Api.BoardQueueItem)] = Case<BoardQueueItem>(BoardQueueItem),
        [nameof(Malachi.Core.Api.BoardQueueMessage)] = Case<BoardQueueMessage>(BoardQueueMessage),
        [nameof(BoardAnnotateParams)] = Case<BoardAnnotateParams>(
            """{"caseId":"@caseid@","inputKey":"0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f","runId":"r_1","state":"you","title":"Signed contract","summary":"Alice needs it.","why":"Alice asked you.","tasks":["Sign"],"due":@boarddue@,"draftId":"d_1","source":"claude-opus"}"""),
        [nameof(BoardAnnotateResult)] = Case<BoardAnnotateResult>("""{"case":@boardcase@,"draftNotLinked":true}"""),
        [nameof(BoardCommitParams)] = Case<BoardCommitParams>(
            """{"caseId":"@caseid@","inputKey":"0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f","runId":"r_1","messageId":"m_3","text":"Send the report","quote":"I will send the report on Monday.","due":"2026-10-05T09:00:00Z","source":"claude-opus"}"""),
        [nameof(BoardCommitResult)] = Case<BoardCommitResult>("""{"commitment":@boardcommitment@,"existing":true}"""),
        [nameof(BoardSetCommitmentParams)] = Case<BoardSetCommitmentParams>("""{"commitmentId":"k_1","done":true}"""),
        [nameof(BoardSetCommitmentResult)] = Case<BoardSetCommitmentResult>("""{"commitment":@boardcommitment@}"""),
        [nameof(BoardPreferencesResult)] = Case<BoardPreferencesResult>("""{"preferences":@boardpreferences@}"""),
        [nameof(BoardSetPreferencesParams)] = Case<BoardSetPreferencesParams>("""{"preferences":@boardpreferences@}"""),
        [nameof(BoardSetPreferencesResult)] = Case<BoardSetPreferencesResult>("""{"preferences":@boardpreferences@}"""),
        [nameof(BoardRunStartParams)] = Case<BoardRunStartParams>("""{"trigger":"manual","source":"claude-opus"}"""),
        [nameof(BoardRunStartResult)] = Case<BoardRunStartResult>("""{"runId":"r_1"}"""),
        [nameof(BoardRunEndParams)] = Case<BoardRunEndParams>("""{"runId":"r_1","error":"cancelled","usage":@boardusage@}"""),
        [nameof(BoardChangedNotification)] = Case<BoardChangedNotification>("""{"accountIds":["acc_1"]}"""),
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
        [nameof(DraftGetParams)] = Case<DraftGetParams>("""{"accountId":"acc_1","draftId":"d_1"}"""),
        [nameof(DraftGetResult)] = Case<DraftGetResult>("""{"draft":@draft@}"""),
        [nameof(DraftCreateParams)] = Case<DraftCreateParams>(
            """{"accountId":"acc_1","mode":"replyAll","messageId":"m_1","mailto":"mailto:a@example.org","attribution":"On Tue, Alice wrote:","messageAccountId":"acc_j","messageIds":["m_2","m_3"]}"""),
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
        [nameof(FolderMarkReadParams)] = Case<FolderMarkReadParams>("""{"accountId":"acc_1","folderId":"f_1"}"""),
        [nameof(FolderMarkReadResult)] = Case<FolderMarkReadResult>("""{"marked":3}"""),
        [nameof(FolderEmptyParams)] = Case<FolderEmptyParams>("""{"accountId":"acc_1","folderId":"f_trash"}"""),
        [nameof(FolderEmptyResult)] = Case<FolderEmptyResult>("""{"deleted":12}"""),
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
        [nameof(DraftsChangedNotification)] = Case<DraftsChangedNotification>("""{"accountId":"acc_1","threadIds":["t_9"]}"""),
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
        [nameof(ThreadGetParams)] = Case<ThreadGetParams>("""{"accountId":"acc_1","threadId":"t_9","folderId":"f_inbox","withSent":true,"withDrafts":true}"""),
        [nameof(ThreadGetResult)] = Case<ThreadGetResult>("""{"thread":@thread@,"messages":[@summary@,@summary@],"sent":[@summary@],"drafts":[@draft@]}"""),
        // Tls.cs
        [nameof(CertificateInfo)] = Case<CertificateInfo>(Certificate),
        [nameof(TlsErrorData)] = Case<TlsErrorData>("""{"reason":"pinMismatch","certificate":@certificate@,"expectedSha256":"@pin@"}"""),
        // Calendar.cs
        [nameof(CalDAVConfig)] = Case<CalDAVConfig>("""@calcaldavconfig@"""),
        [nameof(CalendarConfig)] = Case<CalendarConfig>("""@calconfig@"""),
        [nameof(CalDAVCalendar)] = Case<CalDAVCalendar>("""@caldavcalendar@"""),
        [nameof(AccountDiscoverCalDAVParams)] = Case<AccountDiscoverCalDAVParams>("""{"url":"cloud.example.org","certificateSha256":"@pin@"}"""),
        [nameof(AccountDiscoverCalDAVResult)] = Case<AccountDiscoverCalDAVResult>("""{"serverUrl":"https://cloud.example.org/remote.php/dav","foundVia":"wellKnown","needsCredentials":true,"certificate":@certificate@}"""),
        [nameof(AccountListCalDAVCalendarsParams)] = Case<AccountListCalDAVCalendarsParams>("""{"accountId":"acc_1","config":@config@,"credentials":{"password":"p"}}"""),
        [nameof(AccountListCalDAVCalendarsResult)] = Case<AccountListCalDAVCalendarsResult>("""{"principalUrl":"https://cloud.example.org/remote.php/dav/principals/users/jana/","homeSetUrl":"https://cloud.example.org/remote.php/dav/calendars/jana/","displayName":"Jana","addresses":["jana@example.org"],"scheduling":"server","calendars":[@caldavcalendar@]}"""),
        [nameof(Calendar)] = Case<Calendar>("""@calcalendar@"""),
        [nameof(CalendarFeatures)] = Case<CalendarFeatures>("""@calfeatures@"""),
        [nameof(CalendarSyncState)] = Case<CalendarSyncState>("""@calsyncstate@"""),
        [nameof(CalendarWindow)] = Case<CalendarWindow>("""@calwindow@"""),
        [nameof(CalendarAccount)] = Case<CalendarAccount>("""@calaccount@"""),
        [nameof(Recurrence)] = Case<Recurrence>("""@calrecurrence@"""),
        [nameof(Attendee)] = Case<Attendee>("""@calattendee@"""),
        [nameof(VideoCall)] = Case<VideoCall>("""@calvideocall@"""),
        [nameof(MessageRef)] = Case<MessageRef>("""@calmessageref@"""),
        [nameof(Occurrence)] = Case<Occurrence>("""@caloccurrence@"""),
        [nameof(CalendarEvent)] = Case<CalendarEvent>("""@calevent@"""),
        [nameof(EventInputAttendee)] = Case<EventInputAttendee>("""@calinputattendee@"""),
        [nameof(VideoCallInput)] = Case<VideoCallInput>("""@calvideocallinput@"""),
        [nameof(EventInput)] = Case<EventInput>("""@caleventinput@"""),
        [nameof(MailText)] = Case<MailText>("""@calmailtext@"""),
        [nameof(BusyInterval)] = Case<BusyInterval>("""@calbusy@"""),
        [nameof(Availability)] = Case<Availability>("""@calavailability@"""),
        [nameof(TimeSlot)] = Case<TimeSlot>("""@calslot@"""),
        [nameof(EventAlert)] = Case<EventAlert>("""@calalert@"""),
        [nameof(WorkingHours)] = Case<WorkingHours>("""@calworkinghours@"""),
        [nameof(CalendarPreferences)] = Case<CalendarPreferences>("""@calpreferences@"""),
        [nameof(CalendarPreferencesUpdate)] = Case<CalendarPreferencesUpdate>("""@calpreferences@"""),
        [nameof(CalendarConsentRequiredData)] = Case<CalendarConsentRequiredData>("""{"accountId":"acc_1","signIn":true}"""),
        [nameof(ConflictEventData)] = Case<ConflictEventData>("""{"event":@calevent@}"""),
        [nameof(CalendarListParams)] = Case<CalendarListParams>("""{"accountId":"acc_1"}"""),
        [nameof(CalendarListResult)] = Case<CalendarListResult>("""{"accounts":[@calaccount@],"unanswered":3}"""),
        [nameof(CalendarUpdateParams)] = Case<CalendarUpdateParams>("""{"calendarId":"cal_1","visible":false,"userColor":"#e5534b"}"""),
        [nameof(CalendarUpdateResult)] = Case<CalendarUpdateResult>("""{"calendar":@calcalendar@}"""),
        [nameof(CalendarSyncParams)] = Case<CalendarSyncParams>("""{"accountId":"acc_1","calendarId":"cal_1"}"""),
        [nameof(CalendarSetPreferencesParams)] = Case<CalendarSetPreferencesParams>("""{"preferences":@calpreferences@}"""),
        [nameof(CalendarPreferencesResult)] = Case<CalendarPreferencesResult>("""{"preferences":@calpreferences@}"""),
        [nameof(CalendarSetPreferencesResult)] = Case<CalendarSetPreferencesResult>("""{"preferences":@calpreferences@}"""),
        [nameof(CalendarFreeBusyParams)] = Case<CalendarFreeBusyParams>("""{"accountId":"acc_1","attendees":["jana@example.org"],"from":"2026-10-14T08:00:00Z","to":"2026-10-15T08:00:00Z","intervalMinutes":15}"""),
        [nameof(CalendarFreeBusyResult)] = Case<CalendarFreeBusyResult>("""{"me":@calavailability@,"attendees":[@calavailability@]}"""),
        [nameof(CalendarFindFreeTimeParams)] = Case<CalendarFindFreeTimeParams>("""{"accountId":"acc_1","attendees":["jana@example.org"],"durationMinutes":30,"from":"2026-10-14T08:00:00Z","to":"2026-10-15T08:00:00Z","workingHoursOnly":false,"limit":5}"""),
        [nameof(CalendarFindFreeTimeResult)] = Case<CalendarFindFreeTimeResult>("""{"slots":[@calslot@],"unknown":["x@example.org"]}"""),
        [nameof(EventListParams)] = Case<EventListParams>("""{"from":"2026-10-14T08:00:00Z","to":"2026-10-21T08:00:00Z","timeZone":"Europe/Prague","calendarIds":["cal_1"],"accountIds":["acc_1"],"includeHidden":true,"includeDeclined":false,"includeInvitations":true,"limit":2000}"""),
        [nameof(EventListResult)] = Case<EventListResult>("""{"occurrences":[@caloccurrence@],"truncated":true,"complete":false}"""),
        [nameof(EventGetParams)] = Case<EventGetParams>("""{"eventId":"ev_1","recurrenceId":"2026-10-14","timeZone":"Europe/Prague"}"""),
        [nameof(EventGetResult)] = Case<EventGetResult>("""{"event":@calevent@}"""),
        [nameof(EventSearchParams)] = Case<EventSearchParams>("""{"query":"supplier","from":"2026-10-14T08:00:00Z","to":"2026-12-14T08:00:00Z","calendarIds":["cal_1"],"limit":50}"""),
        [nameof(EventSearchResult)] = Case<EventSearchResult>("""{"occurrences":[@caloccurrence@],"total":12}"""),
        [nameof(InvitationsSent)] = Case<InvitationsSent>("""@calinvitationssent@"""),
        [nameof(EventCreateParams)] = Case<EventCreateParams>("""{"calendarId":"cal_1","event":@caleventinput@,"sendInvitations":true,"mail":@calmailtext@}"""),
        [nameof(EventCreateResult)] = Case<EventCreateResult>("""{"event":@calevent@,"invitations":@calinvitationssent@}"""),
        [nameof(EventUpdateParams)] = Case<EventUpdateParams>("""{"eventId":"ev_1","recurrenceId":"2026-10-14T08:00:00Z","scope":"thisAndFuture","etag":"v1-abc","event":@caleventinput@,"sendUpdates":false,"mail":@calmailtext@}"""),
        [nameof(EventUpdateResult)] = Case<EventUpdateResult>("""{"event":@calevent@,"droppedExceptions":1,"invitations":@calinvitationssent@}"""),
        [nameof(EventDeleteParams)] = Case<EventDeleteParams>("""{"eventId":"ev_1","recurrenceId":"2026-10-14","scope":"this","etag":"v1-abc","notify":false,"comment":"Sorry","mail":@calmailtext@}"""),
        [nameof(EventDeleteResult)] = Case<EventDeleteResult>("""{"invitations":@calinvitationssent@}"""),
        [nameof(EventRespondParams)] = Case<EventRespondParams>("""{"eventId":"ev_1","recurrenceId":"2026-10-14T08:00:00Z","scope":"all","etag":"v1-abc","response":"tentative","sendResponse":true,"comment":"Maybe","proposedStart":"2026-10-14T08:00:00Z","proposedEnd":"2026-10-14T08:30:00Z","mail":@calmailtext@}"""),
        [nameof(EventRespondResult)] = Case<EventRespondResult>("""{"event":@calevent@,"via":"server"}"""),
        [nameof(EventSnoozeAlertParams)] = Case<EventSnoozeAlertParams>("""{"alertId":"al_1","minutes":5}"""),
        [nameof(EventDismissAlertParams)] = Case<EventDismissAlertParams>("""{"alertId":"al_1"}"""),
        [nameof(InvitationSummary)] = Case<InvitationSummary>("""@calinvitationsummary@"""),
        [nameof(InvitationMe)] = Case<InvitationMe>("""@calinvitationme@"""),
        [nameof(InvitationPrevious)] = Case<InvitationPrevious>("""@calinvitationprevious@"""),
        [nameof(InvitationConflict)] = Case<InvitationConflict>("""@calinvitationconflict@"""),
        [nameof(DayBusy)] = Case<DayBusy>("""@caldaybusy@"""),
        [nameof(InvitationDay)] = Case<InvitationDay>("""@calinvitationday@"""),
        [nameof(InvitationTarget)] = Case<InvitationTarget>("""@calinvitationtarget@"""),
        [nameof(InvitationCard)] = Case<InvitationCard>("""@calinvitationcard@"""),
        [nameof(InvitationListParams)] = Case<InvitationListParams>("""{"accountIds":["acc_1"],"state":"unanswered","limit":200}"""),
        [nameof(InvitationListItem)] = Case<InvitationListItem>("""{"accountId":"acc_1","messageId":"m_1","summary":@calinvitationsummary@}"""),
        [nameof(InvitationListResult)] = Case<InvitationListResult>("""{"invitations":[{"accountId":"acc_1","messageId":"m_1","summary":@calinvitationsummary@}]}"""),
        [nameof(InvitationRespondParams)] = Case<InvitationRespondParams>("""{"accountId":"acc_1","messageId":"m_1","response":"accepted","calendarId":"cal_1","sendResponse":false,"comment":"Yes","proposedStart":"2026-10-14T08:00:00Z","proposedEnd":"2026-10-14T08:30:00Z","mail":@calmailtext@}"""),
        [nameof(InvitationRespondResult)] = Case<InvitationRespondResult>("""{"invitation":@calinvitationcard@,"eventId":"ev_1","movedTo":"f_archive"}"""),
        [nameof(InvitationApplyCancelParams)] = Case<InvitationApplyCancelParams>("""{"accountId":"acc_1","messageId":"m_1"}"""),
        [nameof(InvitationApplyCancelResult)] = Case<InvitationApplyCancelResult>("""{"removed":true}"""),
        [nameof(CalendarChangedNotification)] = Case<CalendarChangedNotification>("""{"accountIds":["acc_1"],"calendarIds":["cal_1"],"from":"2026-10-14T08:00:00Z","to":"2026-11-14T08:00:00Z"}"""),
        [nameof(CalendarStateNotification)] = Case<CalendarStateNotification>("""{"state":@calsyncstate@}"""),
        [nameof(EventAlertNotification)] = Case<EventAlertNotification>("""{"alert":@calalert@}"""),
        [nameof(InvitationsChangedNotification)] = Case<InvitationsChangedNotification>("""{"accountIds":["acc_1"]}"""),
    };

    // Minimal documents: what the encoding adds to them.
    private static readonly Dictionary<string, Sample> Minimal = new(StringComparer.Ordinal)
    {
        // The calendar: members left out stay out, the lists Go never sends as null are written as arrays.
        ["Calendar without its optional members"] = Case<Calendar>(
            """{"id":"cal_1","accountId":"acc_1","remoteId":"AAMk-1","name":"Calendar","color":"#1f6feb","visible":true,"access":"read","kind":"somethingNewer"}""",
            """{"id":"cal_1","accountId":"acc_1","remoteId":"AAMk-1","name":"Calendar","color":"#1f6feb","visible":true,"access":"read","kind":"somethingNewer"}"""),
        ["CalendarAccount of a Graph account with sync off"] = Case<CalendarAccount>(
            """{"accountId":"acc_1","source":"graph","sync":false,"state":{"accountId":"acc_1","status":"disabled","progress":0},"features":{"freeBusy":false,"proposeNewTime":false,"invitations":"none","onlineMeetings":false},"window":{"from":"2025-10-14","to":"2027-11-18"},"calendars":null}""",
            """{"accountId":"acc_1","source":"graph","sync":false,"state":{"accountId":"acc_1","status":"disabled","progress":0},"features":{"freeBusy":false,"proposeNewTime":false,"invitations":"none","onlineMeetings":false},"window":{"from":"2025-10-14","to":"2027-11-18"},"calendars":[]}"""),
        ["Occurrence of an all-day event"] = Case<Occurrence>(
            """{"eventId":"ev_1","calendarId":"cal_1","accountId":"acc_1","title":"Holiday","startDate":"2026-10-14","endDate":"2026-10-15","allDay":true,"status":"confirmed","showAs":"free"}""",
            """{"eventId":"ev_1","calendarId":"cal_1","accountId":"acc_1","title":"Holiday","startDate":"2026-10-14","endDate":"2026-10-15","allDay":true,"status":"confirmed","showAs":"free"}"""),
        ["Event without its optional members"] = Case<CalendarEvent>(
            """{"accountId":"acc_1","title":"","allDay":false,"status":"confirmed","showAs":"busy","etag":"v1","canEdit":false,"canDelete":false,"canRespond":false,"canProposeNewTime":false,"removeIsDelete":false}""",
            """{"accountId":"acc_1","title":"","allDay":false,"status":"confirmed","showAs":"busy","etag":"v1","canEdit":false,"canDelete":false,"canRespond":false,"canProposeNewTime":false,"removeIsDelete":false}"""),
        ["EventInput that leaves the alerts to the preference"] = Case<EventInput>(
            """{"title":"","allDay":true,"startDate":"2026-10-14","endDate":"2026-10-15","alerts":null}""",
            """{"title":"","allDay":true,"startDate":"2026-10-14","endDate":"2026-10-15"}"""),
        ["EventInput with no alerts"] = Case<EventInput>(
            """{"title":"","allDay":true,"startDate":"2026-10-14","endDate":"2026-10-15","alerts":[]}""",
            """{"title":"","allDay":true,"startDate":"2026-10-14","endDate":"2026-10-15","alerts":[]}"""),
        ["CalendarPreferencesUpdate of one member"] = Case<CalendarPreferencesUpdate>(
            """{"showDeclined":false,"weekStart":null}""",
            """{"showDeclined":false}"""),
        ["CalendarConfig of a Graph account"] = Case<CalendarConfig>(
            """{"sync":false}""",
            """{"sync":false}"""),
        ["CalendarFreeBusyResult without busy spans"] = Case<CalendarFreeBusyResult>(
            """{"me":{"address":"me@example.org","known":true,"busy":null},"attendees":null}""",
            """{"me":{"address":"me@example.org","known":true,"busy":[]},"attendees":[]}"""),
        ["InvitationCard of a reply"] = Case<InvitationCard>(
            """{"method":"reply","kind":"reply","state":"processed","title":"Workshop","partId":"2","uid":"abc@example.org","sequence":0,"attendeeCount":1,"conflicts":null,"day":{"from":"2026-10-14T00:00:00Z","to":"2026-10-15T00:00:00Z","busy":null},"targets":null,"canRespond":false,"canProposeNewTime":false,"replyVia":"none"}""",
            """{"method":"reply","kind":"reply","state":"processed","title":"Workshop","partId":"2","uid":"abc@example.org","sequence":0,"attendeeCount":1,"conflicts":[],"day":{"from":"2026-10-14T00:00:00Z","to":"2026-10-15T00:00:00Z","busy":[]},"targets":[],"canRespond":false,"canProposeNewTime":false,"replyVia":"none"}"""),
        ["CalendarChangedNotification for any account"] = Case<CalendarChangedNotification>(
            """{}""",
            """{}"""),
        ["CalDAVConfig of an auto-scheduling account"] = Case<CalDAVConfig>(
            """{"serverUrl":"https://cloud.example.org/remote.php/dav","username":"jana"}""",
            """{"serverUrl":"https://cloud.example.org/remote.php/dav","username":"jana"}"""),
        ["MessageSummary without an invitation"] = Case<MessageSummary>(
            """{"id":"m","accountId":"a","folderId":"f","from":[],"subject":"","date":"2026-09-02T10:00:00Z","snippet":"","flags":[],"hasAttachments":false,"size":0,"invitation":null}""",
            """{"id":"m","accountId":"a","folderId":"f","from":[],"subject":"","date":"2026-09-02T10:00:00Z","snippet":"","flags":[],"hasAttachments":false,"size":0}"""),
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
        // A daemon from before the user's replies in Sent (2026-10-01) and
        // their drafts (2026-10-15): no sentCount and draftCount (0), no sent
        // and drafts (empty); a request that does not ask for them leaves
        // withSent and withDrafts out.
        ["ThreadSummary without sentCount"] = Case<ThreadSummary>(
            """{"id":"t","accountId":"a","subject":"","messageCount":1,"unreadCount":0,"latestDate":"2026-09-02T10:00:00Z","latest":@summary@,"snippet":"","hasAttachments":false}""",
            """{"id":"t","accountId":"a","subject":"","participants":[],"messageCount":1,"unreadCount":0,"latestDate":"2026-09-02T10:00:00Z","latest":@summary@,"snippet":"","flags":[],"hasAttachments":false,"folderIds":[],"sentCount":0,"draftCount":0}"""),
        ["ThreadGetResult without sent"] = Case<ThreadGetResult>(
            """{"thread":@thread@,"messages":[],"drafts":null}""",
            """{"thread":@thread@,"messages":[],"sent":[],"drafts":[]}"""),
        ["ThreadGetParams without withSent"] = Case<ThreadGetParams>(
            """{"accountId":"a","threadId":"t","withSent":null,"withDrafts":null}""",
            """{"accountId":"a","threadId":"t"}"""),
        ["DraftsChangedNotification for any thread"] = Case<DraftsChangedNotification>(
            """{"accountId":"acc_1"}""",
            """{"accountId":"acc_1","threadIds":[]}"""),
        // trimQuoted is asked for or left out, never false; a daemon that
        // cut nothing, or one from before the parameter, leaves
        // quotedTrimmed out.
        ["MessageBodyParams with trimQuoted false"] = Case<MessageBodyParams>(
            """{"accountId":"a","messageId":"m","trimQuoted":false}""",
            """{"accountId":"a","messageId":"m"}"""),
        // The board: the members Go leaves out (omitempty) stay out, the
        // lists it never sends as null are written as arrays, and a
        // notification without accounts means any account.
        ["BoardCase of a mail thread, automatic and live"] = Case<BoardCase>(
            """{"id":"@caseid@","accountId":"acc_1","threadId":"t_9","ruleState":"info","ruleReason":"somethingNewer","visibility":"live","subject":"","person":{"address":"a@example.org"},"date":"2026-09-30T08:00:00Z","snippet":"","unread":false,"hasAttachments":false,"messageCount":1,"replyMessageId":"m_1","replyFolderId":"f_inbox","latestMessageId":"m_1","canArchive":false,"version":0}""",
            """{"id":"@caseid@","accountId":"acc_1","threadId":"t_9","ruleState":"info","ruleReason":"somethingNewer","visibility":"live","subject":"","person":{"address":"a@example.org"},"date":"2026-09-30T08:00:00Z","snippet":"","unread":false,"hasAttachments":false,"messageCount":1,"replyMessageId":"m_1","replyFolderId":"f_inbox","latestMessageId":"m_1","canArchive":false,"version":0}"""),
        ["BoardAnnotation fresh, without a state, tasks or due"] = Case<BoardAnnotation>(
            """{"title":"","summary":"","why":"","tasks":null,"source":"s","at":"2026-09-30T08:30:00Z","stale":null}""",
            """{"title":"","summary":"","why":"","tasks":[],"source":"s","at":"2026-09-30T08:30:00Z"}"""),
        ["BoardListResult of a disabled board"] = Case<BoardListResult>(
            """{"cases":null,"commitments":null,"enabled":false,"assistant":false,"triage":{"annotatedTodayAuto":0,"queue":0},"ready":false}""",
            """{"cases":[],"commitments":[],"enabled":false,"assistant":false,"triage":{"annotatedTodayAuto":0,"queue":0},"ready":false}"""),
        ["BoardSetStateParams back to automatic"] = Case<BoardSetStateParams>(
            """{"caseId":"@caseid@","state":null}""",
            """{"caseId":"@caseid@"}"""),
        ["BoardRemindParams ending the remind"] = Case<BoardRemindParams>(
            """{"caseId":"@caseid@","until":null}""",
            """{"caseId":"@caseid@"}"""),
        ["BoardRunEndParams of a success"] = Case<BoardRunEndParams>("""{"runId":"r_1"}""", """{"runId":"r_1"}"""),
        ["BoardChangedNotification for any account"] = Case<BoardChangedNotification>("{}", """{"accountIds":[]}"""),
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
            ("@caseid@", CaseId), ("@boardissue@", BoardIssue), ("@boarddue@", BoardDue), ("@boardannotation@", BoardAnnotation),
            ("@boardcase@", BoardCase), ("@boardcommitment@", BoardCommitment), ("@boardmessage@", BoardMessage),
            ("@boardrun@", BoardRun), ("@boardtriage@", BoardTriage), ("@boardwindows@", BoardWindows),
            ("@boardmoved@", BoardMoved), ("@boardusage@", BoardUsage), ("@boardusagetotal@", BoardUsageTotal),
            ("@boardpreferences@", BoardPreferences), ("@boardqueuemessage@", BoardQueueMessage), ("@boardqueueitem@", BoardQueueItem),
            ("@calcaldavconfig@", CalCalDavConfig),
            ("@calconfig@", CalConfig),
            ("@caldavcalendar@", CalDavCalendar),
            ("@calcalendar@", CalCalendar),
            ("@calfeatures@", CalFeatures),
            ("@calsyncstate@", CalSyncState),
            ("@calwindow@", CalWindow),
            ("@calaccount@", CalAccount),
            ("@calrecurrence@", CalRecurrence),
            ("@calattendee@", CalAttendee),
            ("@calvideocall@", CalVideoCall),
            ("@calmessageref@", CalMessageRef),
            ("@caloccurrencemembers@", CalOccurrenceMembers),
            ("@caloccurrence@", CalOccurrence),
            ("@calevent@", CalEvent),
            ("@calinputattendee@", CalInputAttendee),
            ("@calvideocallinput@", CalVideoCallInput),
            ("@caleventinput@", CalEventInput),
            ("@calmailtext@", CalMailText),
            ("@calbusy@", CalBusy),
            ("@calavailability@", CalAvailability),
            ("@calslot@", CalSlot),
            ("@calalert@", CalAlert),
            ("@calworkinghours@", CalWorkingHours),
            ("@calpreferences@", CalPreferences),
            ("@calinvitationssent@", CalInvitationsSent),
            ("@calinvitationsummarymembers@", CalInvitationSummaryMembers),
            ("@calinvitationsummary@", CalInvitationSummary),
            ("@calinvitationme@", CalInvitationMe),
            ("@calinvitationprevious@", CalInvitationPrevious),
            ("@calinvitationconflict@", CalInvitationConflict),
            ("@caldaybusy@", CalDayBusy),
            ("@calinvitationday@", CalInvitationDay),
            ("@calinvitationtarget@", CalInvitationTarget),
            ("@calinvitationcard@", CalInvitationCard),
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
