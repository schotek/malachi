package rpc

import (
	"context"
	"encoding/json"

	"github.com/schotek/malachi/backend/pkg/api"
)

// handler is the erased form of a typed service method.
type handler func(ctx context.Context, params json.RawMessage) (any, error)

// wrap adapts a typed method to a handler: it decodes params into P and
// returns the result untyped. Unknown fields are tolerated so that older
// daemons keep working with newer clients that send additional optional
// fields.
func wrap[P any, R any](fn func(context.Context, P) (*R, error)) handler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p P
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, api.NewError(api.CodeInvalidParams, "invalid params: %v", err)
			}
		}
		return fn(ctx, p)
	}
}

// registerBackend binds every method name from pkg/api to the corresponding
// service method. Adding a method to the contract without adding it here is
// caught by TestAllMethodsRegistered.
func (s *Server) registerBackend(b api.Backend) {
	sys, acc, fol := b.System(), b.Accounts(), b.Folders()
	msg, thr, drf := b.Messages(), b.Threads(), b.Drafts()
	srch, sync := b.Search(), b.Sync()
	cfg, snd, att := b.Config(), b.Senders(), b.Attachments()

	s.handlers[api.MethodSystemInfo] = wrap(sys.Info)

	s.handlers[api.MethodAccountList] = wrap(acc.List)
	s.handlers[api.MethodAccountAdd] = wrap(acc.Add)
	s.handlers[api.MethodAccountRemove] = wrap(acc.Remove)
	s.handlers[api.MethodAccountTest] = wrap(acc.Test)

	s.handlers[api.MethodFolderList] = wrap(fol.List)
	s.handlers[api.MethodFolderSubscribe] = wrap(fol.Subscribe)

	s.handlers[api.MethodMessageList] = wrap(msg.List)
	s.handlers[api.MethodMessageGet] = wrap(msg.Get)
	s.handlers[api.MethodMessageBody] = wrap(msg.Body)
	s.handlers[api.MethodMessageFlag] = wrap(msg.Flag)
	s.handlers[api.MethodMessageMove] = wrap(msg.Move)
	s.handlers[api.MethodMessageDelete] = wrap(msg.Delete)
	s.handlers[api.MethodMessageSend] = wrap(msg.Send)

	s.handlers[api.MethodThreadList] = wrap(thr.List)
	s.handlers[api.MethodThreadGet] = wrap(thr.Get)

	s.handlers[api.MethodDraftSave] = wrap(drf.Save)
	s.handlers[api.MethodDraftList] = wrap(drf.List)
	s.handlers[api.MethodDraftDelete] = wrap(drf.Delete)
	s.handlers[api.MethodDraftCreate] = wrap(drf.Create)

	s.handlers[api.MethodAttachmentImport] = wrap(att.Import)
	s.handlers[api.MethodAttachmentRemove] = wrap(att.Remove)

	s.handlers[api.MethodSearchQuery] = wrap(srch.Query)

	s.handlers[api.MethodSyncStatus] = wrap(sync.Status)
	s.handlers[api.MethodSyncTrigger] = wrap(sync.Trigger)

	s.handlers[api.MethodConfigGet] = wrap(cfg.Get)
	s.handlers[api.MethodConfigSet] = wrap(cfg.Set)

	s.handlers[api.MethodSenderList] = wrap(snd.List)
	s.handlers[api.MethodSenderAdd] = wrap(snd.Add)
	s.handlers[api.MethodSenderRemove] = wrap(snd.Remove)
}
