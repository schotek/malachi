package core

import (
	"context"
	"net/mail"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

type senderService struct{ b *Backend }

func (s *senderService) List(ctx context.Context, _ api.SenderListParams) (*api.SenderListResult, error) {
	rows, err := s.b.store.ListKnownSenders(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := make([]api.KnownSender, 0, len(rows))
	for _, r := range rows {
		out = append(out, api.KnownSender{Address: r.Address, Source: r.Source, AddedAt: r.AddedAt})
	}
	return &api.SenderListResult{Senders: out}, nil
}

// Add records an explicit user decision. The address may be given as
// "Name <addr>"; only the address part is stored.
func (s *senderService) Add(ctx context.Context, p api.SenderAddParams) (*api.SenderAddResult, error) {
	addr, err := parseAddress(p.Address)
	if err != nil {
		return nil, err
	}
	if err := s.b.store.AddKnownSender(ctx, addr, api.KnownSenderSourceUser); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.SenderAddResult{}, nil
}

func (s *senderService) Remove(ctx context.Context, p api.SenderRemoveParams) (*api.SenderRemoveResult, error) {
	addr, err := parseAddress(p.Address)
	if err != nil {
		return nil, err
	}
	if err := s.b.store.RemoveKnownSender(ctx, addr); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.SenderRemoveResult{}, nil
}

// parseAddress accepts a bare address or an RFC 5322 mailbox and returns the
// normalised bare address.
func parseAddress(s string) (string, error) {
	a, err := mail.ParseAddress(s)
	if err != nil {
		return "", api.NewError(api.CodeInvalidArgument, "invalid address %q", s)
	}
	return store.NormalizeAddress(a.Address), nil
}
