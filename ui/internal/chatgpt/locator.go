// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"bytes"
	"context"
	"debug/elf"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

const InstallURL = "https://developers.openai.com/codex/cli/"

// IsExecutable accepts native ELF images of this architecture, never shell shims.
func IsExecutable(path string) bool {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		return false
	}
	f, err := elf.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	expected := elf.Machine(0)
	switch runtime.GOARCH {
	case "amd64":
		expected = elf.EM_X86_64
	case "arm64":
		expected = elf.EM_AARCH64
	}
	return expected != 0 && f.Machine == expected && (f.Type == elf.ET_EXEC || f.Type == elf.ET_DYN)
}

// Locate treats an explicit selection as authoritative, even if it is invalid.
func Locate(saved string) string {
	if saved != "" {
		if IsExecutable(saved) {
			return saved
		}
		return ""
	}
	home, _ := os.UserHomeDir()
	paths := []string{filepath.Join(home, ".local", "bin", "codex"), "/usr/lib/chatgpt/resources/codex"}
	for _, part := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(part) {
			paths = append(paths, filepath.Join(part, "codex"))
		}
	}
	for _, p := range paths {
		if IsExecutable(p) {
			return p
		}
	}
	return ""
}

var safeVersion = regexp.MustCompile(`^codex(?:-cli)? [0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

func Version(ctx context.Context, path string) (string, error) {
	if !IsExecutable(path) {
		return "", authError("codex_not_found")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = assistant.ChildEnv(os.Environ(), path)
	capture := &versionCapture{}
	cmd.Stdout = capture
	cmd.Stderr = io.Discard
	err := cmd.Run()
	b := capture.Bytes()
	if err != nil || len(b) > 256 {
		return "", authError("codex_version_unavailable")
	}
	line := strings.TrimSpace(string(b))
	if !safeVersion.MatchString(line) {
		return "", authError("codex_version_unavailable")
	}
	return line, nil
}

// versionCapture retains at most 257 bytes while draining arbitrary child output.
type versionCapture struct{ bytes.Buffer }

func (b *versionCapture) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 257 - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
