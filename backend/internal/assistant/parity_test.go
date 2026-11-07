// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package assistant

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"os"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The parity with the clients (docs/plans/ai-api.md D6, E0.2): Build
// gives, byte for byte, what the Go reference composed itself for every
// purpose and variant before the prompts moved here (E1.3). The golden
// file was written by the reference's TestPromptGolden then and is the
// one source of truth now; the test skips where the ui tree is not beside
// the backend.
//
//	go test ./internal/assistant -run TestParityWithTheGoReference -update
//
// rewrites the golden's wants from Build (the cases and the fixed inputs
// stay), so that a deliberate change of a prompt is recorded there;
// without -update the test compares.

// goldenPath is the reference's golden file, relative to this package.
const goldenPath = "../../../ui/internal/assistant/testdata/prompts.golden"

var updateGolden = flag.Bool("update", false, "rewrite the golden's wants from Build")

// The file's shape, as golden_test.go writes it.
type goldenFile struct {
	Language string               `json:"language"`
	Today    string               `json:"today"`
	Profile  api.AssistantProfile `json:"profile"`
	Accounts []goldenAccount      `json:"accounts"`
	Cases    []goldenCase         `json:"cases"`
}

type goldenAccount struct {
	ID    api.AccountID `json:"id"`
	Name  string        `json:"name"`
	Email string        `json:"email"`
}

type goldenCase struct {
	Name      string            `json:"name"`
	Purpose   api.PromptPurpose `json:"purpose"`
	AccountID api.AccountID     `json:"accountId,omitempty"`
	Trigger   api.BoardTrigger  `json:"trigger,omitempty"`
	RunID     string            `json:"runId,omitempty"`
	Input     *api.PromptInput  `json:"input"`
	Want      goldenWant        `json:"want"`
}

type goldenWant struct {
	SystemPrompt string   `json:"systemPrompt"`
	Message      string   `json:"message,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	BridgeArgs   []string `json:"bridgeArgs,omitempty"`
	JSONSchema   string   `json:"jsonSchema,omitempty"`
}

func TestParityWithTheGoReference(t *testing.T) {
	raw, err := os.ReadFile(goldenPath)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("%s is not there", goldenPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Cases) == 0 {
		t.Fatal("the golden has no cases")
	}
	accounts := make([]api.Account, len(g.Accounts))
	for i, a := range g.Accounts {
		accounts[i] = api.Account{ID: a.ID, Config: api.AccountConfig{Name: a.Name, Email: a.Email}}
	}
	purposes := map[api.PromptPurpose]int{}
	for i := range g.Cases {
		c := g.Cases[i]
		purposes[c.Purpose]++
		got, err := Build(c.Purpose, c.Input, Options{
			Language: g.Language, Today: g.Today,
			Trigger: c.Trigger, RunID: c.RunID,
			Profile: g.Profile, Accounts: accounts,
			AccountID: c.AccountID,
		})
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if *updateGolden {
			g.Cases[i].Want = goldenWant{SystemPrompt: got.SystemPrompt, Message: got.Message, Tools: got.Tools, BridgeArgs: got.BridgeArgs, JSONSchema: string(got.JSONSchema)}
			continue
		}
		if got.SystemPrompt != c.Want.SystemPrompt {
			t.Errorf("%s: system prompt\n%q\nwant\n%q", c.Name, got.SystemPrompt, c.Want.SystemPrompt)
		}
		if got.Message != c.Want.Message {
			t.Errorf("%s: message\n%q\nwant\n%q", c.Name, got.Message, c.Want.Message)
		}
		if !slices.Equal(got.Tools, c.Want.Tools) {
			t.Errorf("%s: tools %q, want %q", c.Name, got.Tools, c.Want.Tools)
		}
		if !slices.Equal(got.BridgeArgs, c.Want.BridgeArgs) {
			t.Errorf("%s: bridge args %q, want %q", c.Name, got.BridgeArgs, c.Want.BridgeArgs)
		}
		if string(got.JSONSchema) != c.Want.JSONSchema {
			t.Errorf("%s: schema %s, want %s", c.Name, got.JSONSchema, c.Want.JSONSchema)
		}
	}
	// Every purpose is covered.
	for _, p := range []api.PromptPurpose{api.PurposePanel, api.PurposeRewrite, api.PurposeWrite, api.PurposeSearch, api.PurposeTriage, api.PurposeReply} {
		if purposes[p] == 0 {
			t.Errorf("the golden has no %s case", p)
		}
	}
	if *updateGolden {
		// As the reference wrote it: two-space indent, HTML left alone.
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(g); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s with %d cases", goldenPath, len(g.Cases))
	}
}
