package rpc

import (
	"context"
	"os"

	"github.com/schotek/malachi/backend/pkg/api"
)

// StubBackend implements api.Backend with every data method returning
// api.ErrNotImplemented. Only system.info works. internal/core embeds it and
// overrides the services it has implemented so far; malachid serves that.
//
// TODO(phase-1): as internal/core grows (account, imap, …) the stub shrinks
// and eventually disappears. The RPC package depends on core only through
// api.Backend.
type StubBackend struct {
	Version   string
	StorePath string
}

var _ api.Backend = (*StubBackend)(nil)

func (b *StubBackend) System() api.SystemService          { return stubSystem{b} }
func (b *StubBackend) Accounts() api.AccountService       { return stubAccounts{} }
func (b *StubBackend) Folders() api.FolderService         { return stubFolders{} }
func (b *StubBackend) Messages() api.MessageService       { return stubMessages{} }
func (b *StubBackend) Threads() api.ThreadService         { return stubThreads{} }
func (b *StubBackend) Drafts() api.DraftService           { return stubDrafts{} }
func (b *StubBackend) Attachments() api.AttachmentService { return stubAttachments{} }
func (b *StubBackend) Search() api.SearchService          { return stubSearch{} }
func (b *StubBackend) Sync() api.SyncService              { return stubSync{} }
func (b *StubBackend) Config() api.ConfigService          { return stubConfig{} }
func (b *StubBackend) Senders() api.SenderService         { return stubSenders{} }

type stubSystem struct{ b *StubBackend }

func (s stubSystem) Info(context.Context, api.SystemInfoParams) (*api.SystemInfoResult, error) {
	return &api.SystemInfoResult{
		Version:         s.b.Version,
		ProtocolVersion: api.ProtocolVersion,
		PID:             os.Getpid(),
		StorePath:       s.b.StorePath,
	}, nil
}

type stubAccounts struct{}

func (stubAccounts) List(context.Context, api.AccountListParams) (*api.AccountListResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubAccounts) Add(context.Context, api.AccountAddParams) (*api.AccountAddResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubAccounts) Remove(context.Context, api.AccountRemoveParams) (*api.AccountRemoveResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubAccounts) Test(context.Context, api.AccountTestParams) (*api.AccountTestResult, error) {
	return nil, api.ErrNotImplemented
}

type stubFolders struct{}

func (stubFolders) List(context.Context, api.FolderListParams) (*api.FolderListResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubFolders) Subscribe(context.Context, api.FolderSubscribeParams) (*api.FolderSubscribeResult, error) {
	return nil, api.ErrNotImplemented
}

type stubMessages struct{}

func (stubMessages) List(context.Context, api.MessageListParams) (*api.MessageListResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubMessages) Get(context.Context, api.MessageGetParams) (*api.MessageGetResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubMessages) Body(context.Context, api.MessageBodyParams) (*api.MessageBodyResult, error) {
	// When implemented, this MUST route through internal/sanitize and never
	// return the stored HTML directly. See docs/security.md.
	return nil, api.ErrNotImplemented
}
func (stubMessages) Flag(context.Context, api.MessageFlagParams) (*api.MessageFlagResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubMessages) Move(context.Context, api.MessageMoveParams) (*api.MessageMoveResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubMessages) Delete(context.Context, api.MessageDeleteParams) (*api.MessageDeleteResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubMessages) Send(context.Context, api.MessageSendParams) (*api.MessageSendResult, error) {
	return nil, api.ErrNotImplemented
}

type stubThreads struct{}

func (stubThreads) List(context.Context, api.ThreadListParams) (*api.ThreadListResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubThreads) Get(context.Context, api.ThreadGetParams) (*api.ThreadGetResult, error) {
	return nil, api.ErrNotImplemented
}

type stubDrafts struct{}

func (stubDrafts) Save(context.Context, api.DraftSaveParams) (*api.DraftSaveResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubDrafts) List(context.Context, api.DraftListParams) (*api.DraftListResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubDrafts) Delete(context.Context, api.DraftDeleteParams) (*api.DraftDeleteResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubDrafts) Create(context.Context, api.DraftCreateParams) (*api.DraftCreateResult, error) {
	return nil, api.ErrNotImplemented
}

type stubAttachments struct{}

func (stubAttachments) Import(context.Context, api.AttachmentImportParams) (*api.AttachmentImportResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubAttachments) Remove(context.Context, api.AttachmentRemoveParams) (*api.AttachmentRemoveResult, error) {
	return nil, api.ErrNotImplemented
}

type stubSearch struct{}

func (stubSearch) Query(context.Context, api.SearchQueryParams) (*api.SearchQueryResult, error) {
	return nil, api.ErrNotImplemented
}

type stubSync struct{}

func (stubSync) Status(context.Context, api.SyncStatusParams) (*api.SyncStatusResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubSync) Trigger(context.Context, api.SyncTriggerParams) (*api.SyncTriggerResult, error) {
	return nil, api.ErrNotImplemented
}

type stubConfig struct{}

func (stubConfig) Get(context.Context, api.ConfigGetParams) (*api.ConfigGetResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubConfig) Set(context.Context, api.ConfigSetParams) (*api.ConfigSetResult, error) {
	return nil, api.ErrNotImplemented
}

type stubSenders struct{}

func (stubSenders) List(context.Context, api.SenderListParams) (*api.SenderListResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubSenders) Add(context.Context, api.SenderAddParams) (*api.SenderAddResult, error) {
	return nil, api.ErrNotImplemented
}
func (stubSenders) Remove(context.Context, api.SenderRemoveParams) (*api.SenderRemoveResult, error) {
	return nil, api.ErrNotImplemented
}
