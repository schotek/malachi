package core

import (
	"context"

	"github.com/schotek/malachi/backend/pkg/api"
)

// KnownSenderLookup answers whether an address is on the allow-list.
type KnownSenderLookup func(ctx context.Context, address string) (bool, error)

// ResolveRemoteContent turns the stored policy, an optional per-call
// override and the message's sender list into the two-state decision the
// sanitiser understands. It never returns RemoteKnownSenders.
//
// Rules, in order:
//   - decrypted content is always blocked (docs/security.md §5);
//   - an override of "block" or "allow" wins; any other non-empty override is
//     invalidArgument;
//   - stored "allow" / "block" apply as such;
//   - stored "knownSenders" allows only when the message has at least one
//     sender and every sender address is known; anything else blocks.
func ResolveRemoteContent(ctx context.Context, stored, override api.RemoteContentPolicy,
	senders []api.Address, decrypted bool, known KnownSenderLookup) (api.RemoteContentPolicy, error) {

	switch override {
	case "", api.RemoteBlock, api.RemoteAllow:
	default:
		return api.RemoteBlock, api.NewError(api.CodeInvalidArgument,
			"remoteContent override must be block or allow")
	}
	if decrypted {
		return api.RemoteBlock, nil
	}
	if override != "" {
		return override, nil
	}

	switch stored {
	case api.RemoteAllow:
		return api.RemoteAllow, nil
	case api.RemoteKnownSenders:
		if len(senders) == 0 || known == nil {
			return api.RemoteBlock, nil
		}
		for _, a := range senders {
			ok, err := known(ctx, a.Address)
			if err != nil {
				return api.RemoteBlock, api.NewError(api.CodeStorageError, "%v", err)
			}
			if !ok {
				return api.RemoteBlock, nil
			}
		}
		return api.RemoteAllow, nil
	default:
		return api.RemoteBlock, nil
	}
}

// RemoteContentFor is ResolveRemoteContent wired to this backend's stored
// policy and allow-list; message.body will call it once it exists.
func (b *Backend) RemoteContentFor(ctx context.Context, override api.RemoteContentPolicy,
	senders []api.Address, decrypted bool) (api.RemoteContentPolicy, error) {
	p, err := b.preferences(ctx)
	if err != nil {
		return api.RemoteBlock, err
	}
	return ResolveRemoteContent(ctx, p.RemoteContent, override, senders, decrypted, b.store.IsKnownSender)
}
