// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

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

// registerTransport binds the handshake methods, which belong to the
// transport and not to api.Backend (docs/api.md §1.4). Before a connection
// is authenticated the handshake answers them (handshake.step) and nothing
// reaches this table; afterwards they answer that it already is, and the
// connection stays usable.
func (s *Server) registerTransport() {
	already := func(context.Context, json.RawMessage) (any, error) {
		return nil, api.NewError(api.CodeInvalidRequest, "already authenticated")
	}
	s.handlers[api.MethodSystemHello] = already
	s.handlers[api.MethodSystemAuthenticate] = already
}

// registerBackend binds every other method name from pkg/api to the
// corresponding service method. Adding a method to the contract without
// adding it here (or to registerTransport) is caught by
// TestAllMethodsRegistered.
func (s *Server) registerBackend(b api.Backend) {
	sys, acc, fol := b.System(), b.Accounts(), b.Folders()
	msg, thr, drf := b.Messages(), b.Threads(), b.Drafts()
	srch, sync := b.Search(), b.Sync()
	cfg, snd, att := b.Config(), b.Senders(), b.Attachments()
	con, iss, brd := b.Contacts(), b.Issues(), b.Board()

	s.handlers[api.MethodSystemInfo] = wrap(sys.Info)
	s.handlers[api.MethodSystemStorage] = wrap(sys.Storage)

	s.handlers[api.MethodAccountList] = wrap(acc.List)
	s.handlers[api.MethodAccountAdd] = wrap(acc.Add)
	s.handlers[api.MethodAccountRemove] = wrap(acc.Remove)
	s.handlers[api.MethodAccountSetEnabled] = wrap(acc.SetEnabled)
	s.handlers[api.MethodAccountUpdate] = wrap(acc.Update)
	s.handlers[api.MethodAccountDiscover] = wrap(acc.Discover)
	s.handlers[api.MethodAccountTest] = wrap(acc.Test)
	s.handlers[api.MethodAccountLinked] = wrap(acc.Linked)
	s.handlers[api.MethodAccountReorder] = wrap(acc.Reorder)
	s.handlers[api.MethodAccountOAuthStart] = wrap(acc.OAuthStart)
	s.handlers[api.MethodAccountOAuthWait] = wrap(acc.OAuthWait)
	s.handlers[api.MethodAccountOAuthCancel] = wrap(acc.OAuthCancel)
	s.handlers[api.MethodAccountDetectSite] = wrap(acc.DetectSite)
	s.handlers[api.MethodAccountListSpaces] = wrap(acc.ListSpaces)

	s.handlers[api.MethodFolderList] = wrap(fol.List)
	s.handlers[api.MethodFolderSubscribe] = wrap(fol.Subscribe)
	s.handlers[api.MethodFolderMarkRead] = wrap(fol.MarkRead)
	s.handlers[api.MethodFolderEmpty] = wrap(fol.Empty)

	s.handlers[api.MethodMessageList] = wrap(msg.List)
	s.handlers[api.MethodMessageGet] = wrap(msg.Get)
	s.handlers[api.MethodMessageBody] = wrap(msg.Body)
	s.handlers[api.MethodMessagePart] = wrap(msg.Part)
	s.handlers[api.MethodMessageEmbedded] = wrap(msg.Embedded)
	s.handlers[api.MethodMessageDownload] = wrap(msg.Download)
	s.handlers[api.MethodMessageUnsubscribe] = wrap(msg.Unsubscribe)
	s.handlers[api.MethodMessageFlag] = wrap(msg.Flag)
	s.handlers[api.MethodMessageMove] = wrap(msg.Move)
	s.handlers[api.MethodMessageDelete] = wrap(msg.Delete)
	s.handlers[api.MethodMessageSend] = wrap(msg.Send)
	s.handlers[api.MethodOutboxRetry] = wrap(b.Outbox().Retry)

	s.handlers[api.MethodThreadList] = wrap(thr.List)
	s.handlers[api.MethodThreadGet] = wrap(thr.Get)

	s.handlers[api.MethodDraftSave] = wrap(drf.Save)
	s.handlers[api.MethodDraftList] = wrap(drf.List)
	s.handlers[api.MethodDraftDelete] = wrap(drf.Delete)
	s.handlers[api.MethodDraftCreate] = wrap(drf.Create)
	s.handlers[api.MethodDraftOpen] = wrap(drf.Open)
	s.handlers[api.MethodDraftMarkdown] = wrap(drf.Markdown)
	s.handlers[api.MethodDraftGet] = wrap(drf.Get)

	s.handlers[api.MethodAttachmentImport] = wrap(att.Import)
	s.handlers[api.MethodAttachmentRemove] = wrap(att.Remove)
	s.handlers[api.MethodAttachmentGet] = wrap(att.Get)

	s.handlers[api.MethodSearchQuery] = wrap(srch.Query)

	s.handlers[api.MethodSyncStatus] = wrap(sync.Status)
	s.handlers[api.MethodSyncTrigger] = wrap(sync.Trigger)

	s.handlers[api.MethodConfigGet] = wrap(cfg.Get)
	s.handlers[api.MethodConfigSet] = wrap(cfg.Set)

	s.handlers[api.MethodSenderList] = wrap(snd.List)
	s.handlers[api.MethodSenderAdd] = wrap(snd.Add)
	s.handlers[api.MethodSenderRemove] = wrap(snd.Remove)

	s.handlers[api.MethodContactSearch] = wrap(con.Search)

	s.handlers[api.MethodIssueTransitions] = wrap(iss.Transitions)
	s.handlers[api.MethodIssueTransition] = wrap(iss.Transition)

	s.handlers[api.MethodBoardList] = wrap(brd.List)
	s.handlers[api.MethodBoardGet] = wrap(brd.Get)
	s.handlers[api.MethodBoardSetState] = wrap(brd.SetState)
	s.handlers[api.MethodBoardSetDone] = wrap(brd.SetDone)
	s.handlers[api.MethodBoardRemind] = wrap(brd.Remind)
	s.handlers[api.MethodBoardArchive] = wrap(brd.Archive)
	s.handlers[api.MethodBoardTrash] = wrap(brd.Trash)
	s.handlers[api.MethodBoardUnflag] = wrap(brd.Unflag)
	s.handlers[api.MethodBoardDiscardDraft] = wrap(brd.DiscardDraft)
	s.handlers[api.MethodBoardSetDraft] = wrap(brd.SetDraft)
	s.handlers[api.MethodBoardQueue] = wrap(brd.Queue)
	s.handlers[api.MethodBoardAnnotate] = wrap(brd.Annotate)
	s.handlers[api.MethodBoardCommit] = wrap(brd.Commit)
	s.handlers[api.MethodBoardSetCommitment] = wrap(brd.SetCommitment)
	s.handlers[api.MethodBoardPreferences] = wrap(brd.Preferences)
	s.handlers[api.MethodBoardSetPreferences] = wrap(brd.SetPreferences)
	s.handlers[api.MethodBoardRunStart] = wrap(brd.RunStart)
	s.handlers[api.MethodBoardRunEnd] = wrap(brd.RunEnd)

	s.registerCalendar(b)
	s.registerCrm(b)
	s.registerAssistant(b)
}

// registerAssistant binds the assistant profile methods (docs/api.md
// §4.18).
func (s *Server) registerAssistant(b api.Backend) {
	as := b.Assistant()
	s.handlers[api.MethodAssistantProfileGet] = wrap(as.ProfileGet)
	s.handlers[api.MethodAssistantProfileSet] = wrap(as.ProfileSet)
	s.handlers[api.MethodAssistantProfilePropose] = wrap(as.ProfilePropose)
	s.handlers[api.MethodAssistantProfileResolve] = wrap(as.ProfileResolve)
}

// registerCrm binds the CRM methods (docs/api.md §4.1, §4.17).
func (s *Server) registerCrm(b api.Backend) {
	acc, crm := b.Accounts(), b.Crm()

	s.handlers[api.MethodAccountDetectEway] = wrap(acc.DetectEway)

	s.handlers[api.MethodCrmLookup] = wrap(crm.Lookup)
	s.handlers[api.MethodCrmSearch] = wrap(crm.Search)
	s.handlers[api.MethodCrmSave] = wrap(crm.Save)
	s.handlers[api.MethodCrmRemove] = wrap(crm.Remove)
}

// registerCalendar binds the calendar methods (docs/api.md §4.1, §4.14–
// §4.16) to the account, calendar, event and invitation services.
func (s *Server) registerCalendar(b api.Backend) {
	acc, cal, ev, inv := b.Accounts(), b.Calendars(), b.Events(), b.Invitations()

	s.handlers[api.MethodAccountDiscoverCalDAV] = wrap(acc.DiscoverCalDAV)
	s.handlers[api.MethodAccountListCalDAVCalendars] = wrap(acc.ListCalDAVCalendars)

	s.handlers[api.MethodCalendarList] = wrap(cal.List)
	s.handlers[api.MethodCalendarUpdate] = wrap(cal.Update)
	s.handlers[api.MethodCalendarSync] = wrap(cal.Sync)
	s.handlers[api.MethodCalendarPreferences] = wrap(cal.Preferences)
	s.handlers[api.MethodCalendarSetPreferences] = wrap(cal.SetPreferences)
	s.handlers[api.MethodCalendarFreeBusy] = wrap(cal.FreeBusy)
	s.handlers[api.MethodCalendarFindFreeTime] = wrap(cal.FindFreeTime)

	s.handlers[api.MethodEventList] = wrap(ev.List)
	s.handlers[api.MethodEventGet] = wrap(ev.Get)
	s.handlers[api.MethodEventSearch] = wrap(ev.Search)
	s.handlers[api.MethodEventCreate] = wrap(ev.Create)
	s.handlers[api.MethodEventUpdate] = wrap(ev.Update)
	s.handlers[api.MethodEventDelete] = wrap(ev.Delete)
	s.handlers[api.MethodEventRespond] = wrap(ev.Respond)
	s.handlers[api.MethodEventSnoozeAlert] = wrap(ev.SnoozeAlert)
	s.handlers[api.MethodEventDismissAlert] = wrap(ev.DismissAlert)

	s.handlers[api.MethodInvitationList] = wrap(inv.List)
	s.handlers[api.MethodInvitationRespond] = wrap(inv.Respond)
	s.handlers[api.MethodInvitationApplyCancel] = wrap(inv.ApplyCancel)
}

// Server also implements the optional api.CalendarNotifier: like every
// notification, these reach only connections that completed the
// handshake (broadcast).
var _ api.CalendarNotifier = (*Server)(nil)

func (s *Server) CalendarChanged(n api.CalendarChangedNotification) {
	s.broadcast(api.NotifyCalendarChanged, n)
}

func (s *Server) CalendarState(n api.CalendarStateNotification) {
	s.broadcast(api.NotifyCalendarState, n)
}

func (s *Server) EventAlert(n api.EventAlertNotification) {
	s.broadcast(api.NotifyEventAlert, n)
}

func (s *Server) InvitationsChanged(n api.InvitationsChangedNotification) {
	s.broadcast(api.NotifyInvitationsChanged, n)
}
