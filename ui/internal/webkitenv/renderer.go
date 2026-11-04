// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package webkitenv configures the WebKitGTK process environment before the
// first WebView exists. Every package that creates a WebView imports it for
// its side effect (the compose editor, the message viewer), so the setting
// below is in place whichever of them comes first.
package webkitenv

import "os"

// dmabufEnv is WebKitGTK's switch for its DMA-BUF based accelerated
// compositing path; dmabufOptIn is ours to turn that path back on.
const (
	dmabufEnv   = "WEBKIT_DISABLE_DMABUF_RENDERER"
	dmabufOptIn = "MALACHI_WEBKIT_DMABUF"
)

// WebKitGTK 2.5x's DMA-BUF renderer shows an uninitialised GPU buffer for
// the first frame(s) of a fresh view on some drivers (seen on Apple
// Silicon / Asahi: a red flash before the compose editor paints). Hiding
// the widget does not help because the buffer bypasses GSK compositing.
// WebKit only reads the variable when it launches its web process, so it
// must be set before the first WebView exists; package init is early
// enough. The fallback path (GL textures through GSK) is fast enough for
// an editor and for message rendering.
//
// Set MALACHI_WEBKIT_DMABUF=1 to keep WebKit's default renderer (e.g. to
// re-test a driver), or set WEBKIT_DISABLE_DMABUF_RENDERER yourself.
func init() {
	if os.Getenv(dmabufOptIn) != "" || os.Getenv(dmabufEnv) != "" {
		return
	}
	os.Setenv(dmabufEnv, "1")
}
