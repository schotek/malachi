// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/pkg/api"
)

// textResult wraps one text block as a successful tool result.
func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// jsonResult renders v as indented JSON in one text block.
func jsonResult(v any) *mcp.CallToolResult {
	s, err := marshalIndent(v)
	if err != nil {
		return toolErrorf("internal error: encode result: %v", err)
	}
	return textResult(s)
}

// toolErrorf is a failure the model should see and can act on. Tool errors
// are never Go errors: a Go error would end the MCP request as a protocol
// failure, which the model cannot reason about.
func toolErrorf(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}
}

// toolError maps a daemon or transport error to a tool error.
func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: errorText(err)}},
	}
}

// errorText is the text the model sees for err. Daemon error messages are
// unstable technical text and may echo an address or a path, so they are
// control-stripped and capped; the stable part is the code name.
func errorText(err error) string {
	var (
		hs     *api.HandshakeError
		apiErr *api.Error
		down   *daemonDownError
	)
	switch {
	case errors.As(err, &hs):
		return handshakeText(hs)
	case errors.As(err, &apiErr):
		msg := fmt.Sprintf("%s (%d): %s", apiErr.Code, int(apiErr.Code),
			truncateBytes(oneLine(apiErr.Message), maxErrorMessageBytes))
		switch apiErr.Code {
		case api.CodeNotImplemented:
			msg += "; this daemon does not implement that method yet"
		case api.CodeConflict:
			msg += "; the draft changed since it was created (edited in Malachi Mail?), create a new one"
		case api.CodeAttachmentTooBig:
			if apiErr.Data != nil {
				msg += fmt.Sprintf("; limits: %v", apiErr.Data)
			}
		}
		return msg
	case errors.As(err, &down):
		return down.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("malachid did not answer within %s; the daemon may be busy syncing, try again", rpcTimeout)
	case errors.Is(err, errDisconnected):
		return "connection to malachid was lost; retry the call"
	default:
		return "internal error: " + err.Error()
	}
}

// handshakeText is the text for a connection whose handshake failed
// (docs/api.md §1.4), with what the user can do about it. The texts of
// api.HandshakeError never contain the key, a nonce or a proof.
func handshakeText(hs *api.HandshakeError) string {
	switch hs.Reason {
	case api.HandshakeProtocolMismatch:
		return fmt.Sprintf("malachid speaks protocol version %d but this bridge expects %d; rebuild both with make build",
			hs.Daemon, api.ProtocolVersion)
	case api.HandshakeKeyUnavailable:
		return hs.Error() + "; the running daemon writes it next to its socket: start or restart malachid as this user"
	case api.HandshakeDaemonUnproven:
		return "refusing to use the socket: " + hs.Error() + "; is MALACHI_SOCKET pointing at another daemon?"
	case api.HandshakeTimedOut:
		return fmt.Sprintf("malachid did not complete the connection handshake within %s; retry the call", api.HandshakeTimeout)
	default: // rejected, malformed
		return hs.Error() + "; retry the call, or restart malachid"
	}
}
