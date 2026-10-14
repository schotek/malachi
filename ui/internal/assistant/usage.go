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
//
// LowerBound says whether Total is less than the run used: true unless the
// result's usage was taken, or the run's final report came (Finished) and
// every message's usage counted was final (Event.UsageFinal, a provider
// that reports each message's usage at its end).
type UsageTally struct {
	result   *Usage
	messages map[string]Usage
	// partial: some message's usage counted was not final; finished: the
	// run's final report came.
	partial  map[string]bool
	finished bool
}

// Finished says the run's final report came (the request answered): a
// provider without a usage in its result (ChatGPT) then has its whole
// usage in its final messages.
func (t *UsageTally) Finished() { t.finished = true }

// LowerBound says whether Total is a lower bound of what the run used (see
// the type's comment); false when Total has nothing.
func (t *UsageTally) LowerBound() bool {
	if t.resultTaken() || len(t.messages) == 0 {
		return false
	}
	return !t.finished || len(t.partial) > 0
}

// resultTaken says whether Total is the result's usage.
func (t *UsageTally) resultTaken() bool {
	return t.result != nil && (*t.result != Usage{} || t.sum() == Usage{})
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
	if t.partial == nil {
		t.partial = map[string]bool{}
	}
	if e.UsageFinal {
		delete(t.partial, e.MessageID)
	} else {
		t.partial[e.MessageID] = true
	}
}

// sum is the usage of the messages counted.
func (t *UsageTally) sum() Usage {
	var sum Usage
	for _, u := range t.messages {
		sum = Usage{
			InputTokens:              addTokens(sum.InputTokens, u.InputTokens),
			OutputTokens:             addTokens(sum.OutputTokens, u.OutputTokens),
			CacheCreationInputTokens: addTokens(sum.CacheCreationInputTokens, u.CacheCreationInputTokens),
			CacheReadInputTokens:     addTokens(sum.CacheReadInputTokens, u.CacheReadInputTokens),
		}
	}
	return sum
}

// Total is the run's usage, each counter at most MaxUsageTokens; false
// when nothing reported any.
func (t *UsageTally) Total() (Usage, bool) {
	sum := t.sum()
	switch {
	case t.resultTaken():
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
