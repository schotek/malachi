// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "context"

// The interfaces below group the RPC methods by service. The backend's RPC
// server dispatches wire calls to an implementation of Backend; a UI client
// library may implement the same interfaces on top of the socket so that
// code can be written against Go types instead of method strings.
//
// Every method returns *Error on failure (wrapped in the error interface).
// Implementations that are not ready return ErrNotImplemented.

type SystemService interface {
	Info(ctx context.Context, p SystemInfoParams) (*SystemInfoResult, error)
	Storage(ctx context.Context, p SystemStorageParams) (*SystemStorageResult, error)
}

type AccountService interface {
	List(ctx context.Context, p AccountListParams) (*AccountListResult, error)
	Add(ctx context.Context, p AccountAddParams) (*AccountAddResult, error)
	Remove(ctx context.Context, p AccountRemoveParams) (*AccountRemoveResult, error)
	SetEnabled(ctx context.Context, p AccountSetEnabledParams) (*AccountSetEnabledResult, error)
	Update(ctx context.Context, p AccountUpdateParams) (*AccountUpdateResult, error)
	Discover(ctx context.Context, p AccountDiscoverParams) (*AccountDiscoverResult, error)
	Test(ctx context.Context, p AccountTestParams) (*AccountTestResult, error)
	Linked(ctx context.Context, p AccountLinkedParams) (*AccountLinkedResult, error)
	Reorder(ctx context.Context, p AccountReorderParams) (*AccountReorderResult, error)
	OAuthStart(ctx context.Context, p AccountOAuthStartParams) (*AccountOAuthStartResult, error)
	OAuthWait(ctx context.Context, p AccountOAuthWaitParams) (*AccountOAuthWaitResult, error)
	OAuthCancel(ctx context.Context, p AccountOAuthCancelParams) (*AccountOAuthCancelResult, error)
	DetectSite(ctx context.Context, p AccountDetectSiteParams) (*AccountDetectSiteResult, error)
	ListSpaces(ctx context.Context, p AccountListSpacesParams) (*AccountListSpacesResult, error)
	DiscoverCalDAV(ctx context.Context, p AccountDiscoverCalDAVParams) (*AccountDiscoverCalDAVResult, error)
	ListCalDAVCalendars(ctx context.Context, p AccountListCalDAVCalendarsParams) (*AccountListCalDAVCalendarsResult, error)
	DetectEway(ctx context.Context, p AccountDetectEwayParams) (*AccountDetectEwayResult, error)
}

type FolderService interface {
	List(ctx context.Context, p FolderListParams) (*FolderListResult, error)
	Subscribe(ctx context.Context, p FolderSubscribeParams) (*FolderSubscribeResult, error)
	MarkRead(ctx context.Context, p FolderMarkReadParams) (*FolderMarkReadResult, error)
	Empty(ctx context.Context, p FolderEmptyParams) (*FolderEmptyResult, error)
}

type MessageService interface {
	List(ctx context.Context, p MessageListParams) (*MessageListResult, error)
	Get(ctx context.Context, p MessageGetParams) (*MessageGetResult, error)
	Body(ctx context.Context, p MessageBodyParams) (*MessageBodyResult, error)
	Part(ctx context.Context, p MessagePartParams) (*MessagePartResult, error)
	Embedded(ctx context.Context, p MessageEmbeddedParams) (*MessageEmbeddedResult, error)
	Download(ctx context.Context, p MessageDownloadParams) (*MessageDownloadResult, error)
	Unsubscribe(ctx context.Context, p MessageUnsubscribeParams) (*MessageUnsubscribeResult, error)
	Flag(ctx context.Context, p MessageFlagParams) (*MessageFlagResult, error)
	Move(ctx context.Context, p MessageMoveParams) (*MessageMoveResult, error)
	Delete(ctx context.Context, p MessageDeleteParams) (*MessageDeleteResult, error)
	Send(ctx context.Context, p MessageSendParams) (*MessageSendResult, error)
}

type OutboxService interface {
	Retry(ctx context.Context, p OutboxRetryParams) (*OutboxRetryResult, error)
}

type ThreadService interface {
	List(ctx context.Context, p ThreadListParams) (*ThreadListResult, error)
	Get(ctx context.Context, p ThreadGetParams) (*ThreadGetResult, error)
}

type DraftService interface {
	Save(ctx context.Context, p DraftSaveParams) (*DraftSaveResult, error)
	List(ctx context.Context, p DraftListParams) (*DraftListResult, error)
	Delete(ctx context.Context, p DraftDeleteParams) (*DraftDeleteResult, error)
	Create(ctx context.Context, p DraftCreateParams) (*DraftCreateResult, error)
	Open(ctx context.Context, p DraftOpenParams) (*DraftOpenResult, error)
	Markdown(ctx context.Context, p DraftMarkdownParams) (*DraftMarkdownResult, error)
	Get(ctx context.Context, p DraftGetParams) (*DraftGetResult, error)
}

type AttachmentService interface {
	Import(ctx context.Context, p AttachmentImportParams) (*AttachmentImportResult, error)
	Remove(ctx context.Context, p AttachmentRemoveParams) (*AttachmentRemoveResult, error)
	Get(ctx context.Context, p AttachmentGetParams) (*AttachmentGetResult, error)
}

type SearchService interface {
	Query(ctx context.Context, p SearchQueryParams) (*SearchQueryResult, error)
}

type SyncService interface {
	Status(ctx context.Context, p SyncStatusParams) (*SyncStatusResult, error)
	Trigger(ctx context.Context, p SyncTriggerParams) (*SyncTriggerResult, error)
}

type ConfigService interface {
	Get(ctx context.Context, p ConfigGetParams) (*ConfigGetResult, error)
	Set(ctx context.Context, p ConfigSetParams) (*ConfigSetResult, error)
}

type SenderService interface {
	List(ctx context.Context, p SenderListParams) (*SenderListResult, error)
	Add(ctx context.Context, p SenderAddParams) (*SenderAddResult, error)
	Remove(ctx context.Context, p SenderRemoveParams) (*SenderRemoveResult, error)
}

type ContactService interface {
	Search(ctx context.Context, p ContactSearchParams) (*ContactSearchResult, error)
}

// IssueService acts on the issues of an issue-tracker account (an account
// with CapabilityTransition).
type IssueService interface {
	Transitions(ctx context.Context, p IssueTransitionsParams) (*IssueTransitionsResult, error)
	Transition(ctx context.Context, p IssueTransitionParams) (*IssueTransitionResult, error)
}

// BoardService is the board (docs/api.md §4.13): cases, the user's
// decisions on them, triage by an assistant and its runs.
type BoardService interface {
	List(ctx context.Context, p BoardListParams) (*BoardListResult, error)
	Get(ctx context.Context, p BoardGetParams) (*BoardGetResult, error)
	SetState(ctx context.Context, p BoardSetStateParams) (*BoardSetStateResult, error)
	SetDone(ctx context.Context, p BoardSetDoneParams) (*BoardSetDoneResult, error)
	Remind(ctx context.Context, p BoardRemindParams) (*BoardRemindResult, error)
	Archive(ctx context.Context, p BoardArchiveParams) (*BoardArchiveResult, error)
	Trash(ctx context.Context, p BoardTrashParams) (*BoardTrashResult, error)
	Unflag(ctx context.Context, p BoardUnflagParams) (*BoardUnflagResult, error)
	DiscardDraft(ctx context.Context, p BoardDiscardDraftParams) (*BoardDiscardDraftResult, error)
	SetDraft(ctx context.Context, p BoardSetDraftParams) (*BoardSetDraftResult, error)
	Queue(ctx context.Context, p BoardQueueParams) (*BoardQueueResult, error)
	Annotate(ctx context.Context, p BoardAnnotateParams) (*BoardAnnotateResult, error)
	Commit(ctx context.Context, p BoardCommitParams) (*BoardCommitResult, error)
	SetCommitment(ctx context.Context, p BoardSetCommitmentParams) (*BoardSetCommitmentResult, error)
	Preferences(ctx context.Context, p BoardPreferencesParams) (*BoardPreferencesResult, error)
	SetPreferences(ctx context.Context, p BoardSetPreferencesParams) (*BoardSetPreferencesResult, error)
	RunStart(ctx context.Context, p BoardRunStartParams) (*BoardRunStartResult, error)
	RunEnd(ctx context.Context, p BoardRunEndParams) (*BoardRunEndResult, error)
}

// CalendarService is the calendars of accounts with CapabilityCalendar,
// their state and the calendar preferences (docs/api.md §4.14).
type CalendarService interface {
	List(ctx context.Context, p CalendarListParams) (*CalendarListResult, error)
	Update(ctx context.Context, p CalendarUpdateParams) (*CalendarUpdateResult, error)
	Sync(ctx context.Context, p CalendarSyncParams) (*CalendarSyncResult, error)
	Preferences(ctx context.Context, p CalendarPreferencesParams) (*CalendarPreferencesResult, error)
	SetPreferences(ctx context.Context, p CalendarSetPreferencesParams) (*CalendarSetPreferencesResult, error)
	FreeBusy(ctx context.Context, p CalendarFreeBusyParams) (*CalendarFreeBusyResult, error)
	FindFreeTime(ctx context.Context, p CalendarFindFreeTimeParams) (*CalendarFindFreeTimeResult, error)
}

// EventService is the events of the stored calendars and their alerts
// (docs/api.md §4.15). Writes go to the server first.
type EventService interface {
	List(ctx context.Context, p EventListParams) (*EventListResult, error)
	Get(ctx context.Context, p EventGetParams) (*EventGetResult, error)
	Search(ctx context.Context, p EventSearchParams) (*EventSearchResult, error)
	Create(ctx context.Context, p EventCreateParams) (*EventCreateResult, error)
	Update(ctx context.Context, p EventUpdateParams) (*EventUpdateResult, error)
	Delete(ctx context.Context, p EventDeleteParams) (*EventDeleteResult, error)
	Respond(ctx context.Context, p EventRespondParams) (*EventRespondResult, error)
	SnoozeAlert(ctx context.Context, p EventSnoozeAlertParams) (*EventSnoozeAlertResult, error)
	DismissAlert(ctx context.Context, p EventDismissAlertParams) (*EventDismissAlertResult, error)
}

// InvitationService is the invitations received in mail (docs/api.md
// §4.16).
type InvitationService interface {
	List(ctx context.Context, p InvitationListParams) (*InvitationListResult, error)
	Respond(ctx context.Context, p InvitationRespondParams) (*InvitationRespondResult, error)
	ApplyCancel(ctx context.Context, p InvitationApplyCancelParams) (*InvitationApplyCancelResult, error)
}

// CrmService saves messages of mail accounts to a CRM account
// (CapabilityCrm) and reads what they are linked to there (docs/api.md
// §4.17).
type CrmService interface {
	Lookup(ctx context.Context, p CrmLookupParams) (*CrmLookupResult, error)
	Search(ctx context.Context, p CrmSearchParams) (*CrmSearchResult, error)
	Save(ctx context.Context, p CrmSaveParams) (*CrmSaveResult, error)
	Remove(ctx context.Context, p CrmRemoveParams) (*CrmRemoveResult, error)
}

// Backend is the complete server-side surface. The connection handshake
// (system.hello, system.authenticate; docs/api.md §1.4) is not part of it:
// the transport (internal/rpc) answers the handshake before any call of a
// connection reaches the backend.
type Backend interface {
	System() SystemService
	Accounts() AccountService
	Folders() FolderService
	Messages() MessageService
	Outbox() OutboxService
	Threads() ThreadService
	Drafts() DraftService
	Attachments() AttachmentService
	Search() SearchService
	Sync() SyncService
	Config() ConfigService
	Senders() SenderService
	Contacts() ContactService
	Issues() IssueService
	Board() BoardService
	Calendars() CalendarService
	Events() EventService
	Invitations() InvitationService
	Crm() CrmService
}

// Notifier is how backend components push events to connected clients.
// The RPC server implements it; internal packages depend on this interface,
// not on the server.
type Notifier interface {
	NewMessage(n NewMessageNotification)
	SyncState(n SyncStateNotification)
	AuthRequired(n AuthRequiredNotification)
	AccountsChanged(n AccountsChangedNotification)
	MessagesChanged(n MessagesChangedNotification)
}

// BoardNotifier is the optional extension of Notifier for
// notify.boardChanged. The RPC server implements it; a backend component
// type-asserts its Notifier and sends nothing when the assertion fails,
// so Notifier's other implementers (test fakes) need no change.
type BoardNotifier interface {
	BoardChanged(n BoardChangedNotification)
}

// DraftsNotifier is the optional extension of Notifier for
// notify.draftsChanged, asserted like BoardNotifier.
type DraftsNotifier interface {
	DraftsChanged(n DraftsChangedNotification)
}

// CalendarNotifier is the optional extension of Notifier for the calendar
// notifications (notify.calendarChanged, notify.calendarState,
// notify.eventAlert, notify.invitationsChanged), asserted like
// BoardNotifier.
type CalendarNotifier interface {
	CalendarChanged(n CalendarChangedNotification)
	CalendarState(n CalendarStateNotification)
	EventAlert(n EventAlertNotification)
	InvitationsChanged(n InvitationsChangedNotification)
}
