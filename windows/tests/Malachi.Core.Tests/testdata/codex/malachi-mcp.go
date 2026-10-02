// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Synthetic MCP peer for the native Codex canary (docs/chatgpt-integration.md
// §6). It reads no daemon, mail or credential. Build only as a test fixture.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

func main() {
	tools := []string{"list_accounts", "list_folders", "list_messages", "search_messages", "read_message", "get_attachment", "create_draft"}
	initialized := false
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 16<<20)
	enc := json.NewEncoder(os.Stdout)
	for scan.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &request) != nil {
			return
		}
		if len(request.ID) == 0 {
			continue
		}
		var result any
		switch request.Method {
		case "initialize":
			for _, value := range os.Environ() {
				key, _, _ := strings.Cut(value, "=")
				if strings.Contains(strings.ToUpper(key), "TOKEN") || strings.Contains(strings.ToUpper(key), "CREDENTIAL") || key == "OPENAI_API_KEY" {
					os.Exit(23)
				}
			}
			var p struct {
				ClientInfo struct {
					Name string `json:"name"`
				} `json:"clientInfo"`
			}
			if json.Unmarshal(request.Params, &p) != nil || p.ClientInfo.Name != "malachi-chatgpt" {
				os.Exit(24)
			}
			initialized = true
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "malachi-canary", "version": "1"}}
		case "tools/list":
			if !initialized {
				os.Exit(25)
			}
			catalog := []any{}
			for _, name := range tools {
				catalog = append(catalog, map[string]any{"name": name, "description": "Synthetic canary tool", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}})
			}
			result = map[string]any{"tools": catalog}
		case "tools/call":
			if !initialized {
				os.Exit(26)
			}
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "SYNTHETIC TOOL RESULT"}}, "isError": false}
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "unsupported"}})
			continue
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
}
