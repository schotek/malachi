// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// keyringFileEnv names the file the helper keeps its secrets in.
const keyringFileEnv = "MALACHI_DEVMAIL_KEYRING"

// keyring is the daemon's keyring helper (internal/auth/helper): one
// JSON line on stdin, the answer on stdout, the outcome in the exit
// status (0 ok, 2 no such item, 3 malformed request).
func keyring(op string, stdin io.Reader, stdout, stderr io.Writer) int {
	path := os.Getenv(keyringFileEnv)
	if path == "" {
		fmt.Fprintf(stderr, "malachi-devmail %s: %s is not set\n", op, keyringFileEnv)
		return 1
	}
	var req struct {
		Account string  `json:"account"`
		Key     string  `json:"key"`
		Value   *string `json:"value"`
	}
	b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil || json.Unmarshal(b, &req) != nil || req.Account == "" || req.Key == "" || (op == "set" && req.Value == nil) {
		return 3
	}
	items, err := readKeyring(path)
	if err != nil {
		fmt.Fprintf(stderr, "malachi-devmail %s: %v\n", op, err)
		return 1
	}
	id := req.Account + "\x00" + req.Key
	switch op {
	case "get":
		v, ok := items[id]
		if !ok {
			return 2
		}
		out, _ := json.Marshal(map[string]string{"value": v})
		fmt.Fprintf(stdout, "%s\n", out)
		return 0
	case "set":
		items[id] = *req.Value
	case "delete":
		if _, ok := items[id]; !ok {
			return 2
		}
		delete(items, id)
	}
	if err := writeKeyring(path, items); err != nil {
		fmt.Fprintf(stderr, "malachi-devmail %s: %v\n", op, err)
		return 1
	}
	return 0
}

func readKeyring(path string) (map[string]string, error) {
	items := map[string]string{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return items, nil
	}
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return items, nil
}

func writeKeyring(path string, items map[string]string) error {
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
