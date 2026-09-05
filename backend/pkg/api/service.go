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
}

type FolderService interface {
	List(ctx context.Context, p FolderListParams) (*FolderListResult, error)
	Subscribe(ctx context.Context, p FolderSubscribeParams) (*FolderSubscribeResult, error)
}

type MessageService interface {
	List(ctx context.Context, p MessageListParams) (*MessageListResult, error)
	Get(ctx context.Context, p MessageGetParams) (*MessageGetResult, error)
	Body(ctx context.Context, p MessageBodyParams) (*MessageBodyResult, error)
	Part(ctx context.Context, p MessagePartParams) (*MessagePartResult, error)
	Embedded(ctx context.Context, p MessageEmbeddedParams) (*MessageEmbeddedResult, error)
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
}

type AttachmentService interface {
	Import(ctx context.Context, p AttachmentImportParams) (*AttachmentImportResult, error)
	Remove(ctx context.Context, p AttachmentRemoveParams) (*AttachmentRemoveResult, error)
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

// Backend is the complete server-side surface.
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
}

// Notifier is how backend components push events to connected clients.
// The RPC server implements it; internal packages depend on this interface,
// not on the server.
type Notifier interface {
	NewMessage(n NewMessageNotification)
	SyncState(n SyncStateNotification)
	AuthRequired(n AuthRequiredNotification)
	AccountsChanged(n AccountsChangedNotification)
}
