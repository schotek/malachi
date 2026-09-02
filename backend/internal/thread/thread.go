// Package thread groups messages into conversations.
//
// Planned algorithm: JWZ threading (References / In-Reply-To) with subject
// fallback, as used by Geary and Thunderbird, with these hardening rules:
//   - Message-ID values are attacker-controlled; never trust them for
//     identity beyond grouping, and cap the number of References considered.
//   - Guard against reference cycles and enormous synthetic threads.
//   - Threads are per account; never merge across accounts.
package thread

import (
	"context"

	"github.com/GITHUB_USER/malachi/backend/pkg/api"
)

// Threader maintains thread membership incrementally as messages arrive.
// TODO(phase-5): implement.
type Threader interface {
	Assign(ctx context.Context, account api.AccountID, msg api.MessageID) (api.ThreadID, error)
}
