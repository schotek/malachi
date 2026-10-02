// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// What a run of Claude Code used, from the usage its events carry. This
// file holds no translatable text.

// MaxUsageTokens is the largest value of each counter of a tally's total,
// api.MaxBoardUsageTokens: what the daemon stores at most.
const MaxUsageTokens int64 = 1_000_000_000_000

// UsageTally adds up the usage of one run's events (Add each event, in
// order). Its Total is the result's usage when the run reported one; a
// run that ended without one (cancelled, timed out, killed, stopped at a
// limit) has the sum over the distinct API messages seen, each counted
// once with the last usage seen for its id: a lower bound, whose output
// tokens are the placeholders of the messages' starts. A result whose
// counters are all 0 while the messages counted some (Claude Code's crash
// result may be zeroed) gives way to that sum. The zero value is empty.
type UsageTally struct {
	result   *Usage
	messages map[string]Usage
}

// Add counts the usage e carries, if any.
func (t *UsageTally) Add(e Event) {
	if e.Usage == nil {
		return
	}
	if e.Kind == EventResult {
		u := *e.Usage
		t.result = &u
		return
	}
	if e.MessageID == "" {
		return
	}
	if t.messages == nil {
		t.messages = map[string]Usage{}
	}
	t.messages[e.MessageID] = *e.Usage
}

// Total is the run's usage, each counter at most MaxUsageTokens; false
// when nothing reported any.
func (t *UsageTally) Total() (Usage, bool) {
	var sum Usage
	for _, u := range t.messages {
		sum = Usage{
			InputTokens:              addTokens(sum.InputTokens, u.InputTokens),
			OutputTokens:             addTokens(sum.OutputTokens, u.OutputTokens),
			CacheCreationInputTokens: addTokens(sum.CacheCreationInputTokens, u.CacheCreationInputTokens),
			CacheReadInputTokens:     addTokens(sum.CacheReadInputTokens, u.CacheReadInputTokens),
		}
	}
	switch {
	case t.result != nil && (*t.result != Usage{} || sum == Usage{}):
		r := *t.result
		return Usage{
			InputTokens:              addTokens(0, r.InputTokens),
			OutputTokens:             addTokens(0, r.OutputTokens),
			CacheCreationInputTokens: addTokens(0, r.CacheCreationInputTokens),
			CacheReadInputTokens:     addTokens(0, r.CacheReadInputTokens),
		}, true
	case len(t.messages) > 0:
		return sum, true
	}
	return Usage{}, false
}

// addTokens is a + b of two counters from 0 up, at most MaxUsageTokens.
func addTokens(a, b int64) int64 {
	a, b = min(max(a, 0), MaxUsageTokens), min(max(b, 0), MaxUsageTokens)
	return min(a+b, MaxUsageTokens)
}
