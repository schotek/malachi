// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
)

// Native compatibility canaries use synthetic HTTP and a fake MCP only. No
// browser, OpenAI account, actual inference, or mailbox participates.
func TestNativeCodexCanary(t *testing.T) {
	exe := os.Getenv("MALACHI_TEST_CODEX")
	if exe == "" {
		exe = "/usr/lib/chatgpt/resources/codex"
	}
	if !IsExecutable(exe) {
		t.Skip("native Codex absent")
	}
	bridge := os.Getenv("MALACHI_TEST_MCP")
	if bridge == "" {
		bridge = "/tmp/malachi-codex-canary-mcp"
	}
	for _, model := range []string{"gpt-6.1", "gpt-6.1-sol", "gpt-6-sol", "gpt-5.6-sol", ""} {
		label := model
		if label == "" {
			label = "default"
		}
		for _, mode := range []string{"text", "mail-tool", "failure-after-draft"} {
			t.Run(label+"/"+mode, func(t *testing.T) {
				withTools := mode != "text"
				failure := mode == "failure-after-draft"
				if withTools {
					if _, err := os.Stat(bridge); err != nil {
						t.Skip("build synthetic MCP fixture")
					}
				}
				calls := 0
				var body []byte
				var mu sync.Mutex
				client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
					mu.Lock()
					defer mu.Unlock()
					calls++
					body, _ = io.ReadAll(r.Body)
					if r.URL.String() != Resource+"/responses" || r.Header.Get("Authorization") != "Bearer fixture-token-never-real" {
						t.Error("token boundary violated")
					}
					if failure && calls > 1 {
						return jsonResponse(map[string]any{"error": "synthetic failure"}, 503), nil
					}
					item := `{"type":"message","id":"msg_canary","role":"assistant","status":"completed","content":[{"type":"output_text","text":"safe answer","annotations":[]}]}`
					toolCall := withTools && calls == 1
					if toolCall {
						name := "read_message"
						if failure {
							name = "create_draft"
						}
						item = `{"type":"function_call","id":"fc_canary","call_id":"call_canary","namespace":"malachi","name":"` + name + `","arguments":"{}","status":"completed"}`
					}
					response := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_canary\",\"status\":\"in_progress\",\"output\":[]}}\n\n" + "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":" + item + "}\n\n"
					if !toolCall {
						response += "data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_canary\",\"output_index\":0,\"content_index\":0,\"delta\":\"safe answer\"}\n\n"
					}
					response += "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + item + "}\n\n" + "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_canary\",\"status\":\"completed\",\"output\":[" + item + "],\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n"
					header := http.Header{}
					if model == "gpt-6.1" {
						header.Set("Content-Type", "Text/Event-Stream; charset=utf-8")
					}
					return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(response))}, nil
				})}
				root := filepath.Join(t.TempDir(), "runtime")
				p := NewProvider(Options{Executable: func() string { return exe }, Directory: root, Env: []string{"OPENAI_API_KEY=must-not-inherit", "MALACHI_MCP_ALLOW_SEND=1"}, Model: func() string { return model }, HasConsent: func() bool { return true }, HTTPClient: client}, fixtureTokens{})
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				spec := assistantpanel.SessionSpec{SystemPrompt: "Answer the fixture"}
				if withTools {
					spec.Tools = &assistantpanel.Tools{Bridge: bridge, Allowed: assistant.AllowedTools}
				}
				session, err := p.Open(ctx, spec)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					session.Terminate()
					select {
					case <-session.Completion():
					case <-ctx.Done():
						t.Error("cleanup timed out")
					}
				}()
				result := make(chan assistant.Event, 1)
				tools := []string{}
				session.SetHandlers(func(events []assistant.Event) {
					for _, e := range events {
						if e.Kind == assistant.EventToolUse {
							tools = append(tools, e.Tool)
						}
						if e.Kind == assistant.EventResult {
							result <- e
						}
					}
				}, func(e assistantpanel.Exit) {
					select {
					case result <- assistant.Event{ResultText: e.Reason}:
					default:
					}
				})
				if err = session.Submit(ctx, "CANARY INPUT"); err != nil {
					t.Fatal(err)
				}
				var answer assistant.Event
				select {
				case answer = <-result:
				case <-ctx.Done():
					t.Fatal("no native result")
				}
				if answer.Success == failure {
					t.Fatalf("unexpected result %#v", answer)
				}
				if failure {
					if answer.ResultText != "chatgpt_inference_refused" {
						t.Fatal(answer.ResultText)
					}
				} else if answer.ResultText != "safe answer" {
					t.Fatal(answer.ResultText)
				}
				if withTools && len(tools) != 1 {
					t.Fatalf("tool count %v", tools)
				}
				mu.Lock()
				count := calls
				lastBody := append([]byte{}, body...)
				mu.Unlock()
				if withTools && count != 2 {
					t.Fatalf("inference replay: %d", count)
				}
				var request map[string]any
				_ = json.Unmarshal(lastBody, &request)
				if request["store"] != false {
					t.Fatal("retention enabled")
				}
				_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.IsDir() {
						return nil
					}
					b, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					for _, canary := range []string{"CANARY INPUT", "safe answer", "SYNTHETIC TOOL RESULT", "fixture-token-never-real"} {
						if strings.Contains(string(b), canary) {
							t.Errorf("sensitive data retained in %s", path)
						}
					}
					return nil
				})
				session.Terminate()
				select {
				case <-session.Completion():
				case <-ctx.Done():
					t.Fatal("native cleanup timed out")
				}
				entries, _ := os.ReadDir(root)
				if len(entries) != 0 {
					t.Fatalf("profile retained %v", entries)
				}
			})
		}
	}
}

func TestPrivateStorageAndLiveSessionSweep(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	store, err := NewNativeStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	release, err := store.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	if _, err = store.Acquire(deadline); err == nil {
		t.Fatal("refresh lock not exclusive")
	}
	if err = store.WriteRegistration(ctx, Registration{HostID: hostID(), Email: "owner@example.test"}); err != nil {
		t.Fatal(err)
	}
	release()
	if r, err := store.ReadRegistration(ctx); err != nil || r.Email != "owner@example.test" {
		t.Fatal("registration failed")
	}
	if err = os.Chmod(filepath.Join(root, "registration.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadRegistration(ctx); err == nil {
		t.Fatal("world-readable metadata accepted")
	}
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0600)
	link := filepath.Join(root, "linked")
	_ = os.Symlink(outside, link)
	if _, err = NewNativeStore(link); err == nil {
		t.Fatal("symlink root accepted")
	}
	session := filepath.Join(root, "session-"+strings.Repeat("a", 64))
	_ = os.Mkdir(session, 0700)
	native, err := NewNativeStore(session)
	if err != nil {
		t.Fatal(err)
	}
	live, err := native.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	} // use the actual lease filename to test cross-instance cleanup
	lease, err := openPrivate(filepath.Join(session, "lease"), os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err = Sweep(root); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(session); err != nil {
		t.Fatal("live session removed")
	}
	live()
	_ = lease.Close()
	if err = Sweep(root); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(session); !os.IsNotExist(err) {
		t.Fatal("abandoned session retained")
	}
	if _, err = os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("unowned path removed")
	}
}

func TestLocatorRejectsShellShimAndVersionCaptureIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if IsExecutable(path) || Locate(path) != "" {
		t.Fatal("shell shim accepted as native runtime")
	}
	capture := &versionCapture{}
	data := make([]byte, 1024*1024)
	if n, err := capture.Write(data); err != nil || n != len(data) || capture.Len() != 257 {
		t.Fatal("version capture not bounded")
	}
	if safeVersion.MatchString("codex 0.159.0\nmalicious") || !safeVersion.MatchString("codex-cli 0.159.0-alpha.12.1") {
		t.Fatal("version validation")
	}
}
