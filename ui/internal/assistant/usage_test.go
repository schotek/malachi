// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"strconv"
	"testing"
)

// tally adds up the events of the lines, in order.
func tally(t *testing.T, lines ...string) UsageTally {
	t.Helper()
	var u UsageTally
	for _, line := range lines {
		events, err := ParseEvents([]byte(line))
		if err != nil {
			t.Fatalf("ParseEvents(%s): %v", line, err)
		}
		for _, e := range events {
			u.Add(e)
		}
	}
	return u
}

func TestUsageTally(t *testing.T) {
	msg := func(id string, in, out, created, read int64) string {
		return `{"type":"assistant","message":{"id":"` + id + `","content":[{"type":"text","text":"t"}],"usage":{"input_tokens":` +
			itoa(in) + `,"output_tokens":` + itoa(out) + `,"cache_creation_input_tokens":` + itoa(created) +
			`,"cache_read_input_tokens":` + itoa(read) + `}},"parent_tool_use_id":null}`
	}
	tests := []struct {
		name  string
		lines []string
		want  Usage
		ok    bool
	}{
		{"nothing", nil, Usage{}, false},
		{"no usage at all", []string{lineInit, lineThinking, lineResultStr, lineAPIError}, Usage{}, false},
		{"the result wins", []string{lineSplitThinking, lineSplitTool, lineResultUsage},
			Usage{InputTokens: 12, OutputTokens: 1500, CacheCreationInputTokens: 2400, CacheReadInputTokens: 180000}, true},
		{"a message split into lines counts once", []string{lineSplitThinking, lineSplitTool},
			Usage{InputTokens: 3, OutputTokens: 8, CacheCreationInputTokens: 1200, CacheReadInputTokens: 45000}, true},
		{"distinct messages add up", []string{lineAssistant, lineSplitThinking, lineSplitTool},
			Usage{InputTokens: 13, OutputTokens: 28, CacheCreationInputTokens: 1200, CacheReadInputTokens: 45000}, true},
		{"the last usage of an id counts", []string{msg("m1", 1, 1, 1, 1), msg("m2", 10, 0, 0, 0), msg("m1", 2, 3, 4, 5)},
			Usage{InputTokens: 12, OutputTokens: 3, CacheCreationInputTokens: 4, CacheReadInputTokens: 5}, true},
		{"a zeroed result gives way to the messages", []string{lineAssistant, lineMaxTurns}, Usage{InputTokens: 10, OutputTokens: 20}, true},
		{"a zeroed result alone", []string{lineMaxTurns}, Usage{}, true},
		{"a result after zero messages", []string{msg("m1", 0, 0, 0, 0), lineMaxTurns}, Usage{}, true},
		{"a result alone", []string{lineSuccess}, Usage{InputTokens: 100, OutputTokens: 50}, true},
		{"a bad result keeps the messages", []string{lineAssistant,
			`{"type":"result","subtype":"success","usage":{"input_tokens":-1}}`}, Usage{InputTokens: 10, OutputTokens: 20}, true},
		{"a subagent's message is left out", []string{lineAssistant,
			`{"type":"assistant","message":{"id":"m9","content":[],"usage":{"input_tokens":500}},"parent_tool_use_id":"toolu_01"}`},
			Usage{InputTokens: 10, OutputTokens: 20}, true},
		{"a failure is left out", []string{lineAuthFailed}, Usage{}, false},
		{"counters stop at the maximum", []string{msg("m1", MaxUsageTokens, 9223372036854775807, 1, 0), msg("m2", 1, 1, 0, 0)},
			Usage{InputTokens: MaxUsageTokens, OutputTokens: MaxUsageTokens, CacheCreationInputTokens: 1, CacheReadInputTokens: 0}, true},
		{"a result beyond the maximum", []string{`{"type":"result","usage":{"cache_read_input_tokens":9223372036854775807}}`},
			Usage{CacheReadInputTokens: MaxUsageTokens}, true},
	}
	for _, tt := range tests {
		u := tally(t, tt.lines...)
		got, ok := u.Total()
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: Total = %+v, %v; want %+v, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestUsageTallyLowerBound(t *testing.T) {
	final := func(id string, in int64) Event {
		return Event{Kind: EventOther, MessageID: id, Usage: &Usage{InputTokens: in, OutputTokens: 1}, UsageFinal: true}
	}
	tests := []struct {
		name     string
		lines    []string
		events   []Event
		finished bool
		want     bool
	}{
		{"nothing", nil, nil, false, false},
		{"the result's usage is whole", []string{lineSplitThinking, lineSplitTool, lineResultUsage}, nil, false, false},
		{"stopped before the result", []string{lineAssistant, lineSplitThinking}, nil, false, true},
		{"answered without a result usage: placeholders", []string{lineAssistant}, nil, true, true},
		{"a zeroed result gives way to placeholders", []string{lineAssistant, lineMaxTurns}, nil, true, true},
		{"final messages of a finished run", nil, []Event{final("r1", 5), final("r2", 7)}, true, false},
		{"final messages of a run that never finished", nil, []Event{final("r1", 5)}, false, true},
		{"one message not final", nil, []Event{final("r1", 5), {Kind: EventOther, MessageID: "r2", Usage: &Usage{InputTokens: 1}}}, true, true},
		{"a placeholder later made final", nil, []Event{{Kind: EventOther, MessageID: "r1", Usage: &Usage{InputTokens: 1}}, final("r1", 5)}, true, false},
	}
	for _, tt := range tests {
		u := tally(t, tt.lines...)
		for _, e := range tt.events {
			u.Add(e)
		}
		if tt.finished {
			u.Finished()
		}
		if got := u.LowerBound(); got != tt.want {
			t.Errorf("%s: LowerBound = %v, want %v", tt.name, got, tt.want)
		}
	}
}
