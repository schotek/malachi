// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package editor

import (
	"context"
	"strings"
	"testing"
)

// The registry resolves what was registered, by either route, and
// nothing else; forgetting an id makes it unknown again.
func TestCIDRegistry(t *testing.T) {
	RegisterCID("file@x", "/tmp/pic.png", "image/png")
	RegisterCIDFetcher("fetched@x", func(context.Context) ([]byte, string, error) { return []byte{1}, "image/png", nil })
	t.Cleanup(func() {
		UnregisterCID("file@x")
		UnregisterCID("fetched@x")
	})
	if f, ok := lookupCID("file@x"); !ok || f.path != "/tmp/pic.png" || f.contentType != "image/png" || f.fetch != nil {
		t.Errorf("file entry = %+v %v", f, ok)
	}
	if f, ok := lookupCID("fetched@x"); !ok || f.fetch == nil || f.path != "" {
		t.Errorf("fetcher entry = %+v %v", f, ok)
	}
	for _, id := range []string{"", "other@x", "../file@x", "FILE@x"} {
		if CIDRegistered(id) {
			t.Errorf("%q resolves", id)
		}
	}
	UnregisterCID("fetched@x")
	if CIDRegistered("fetched@x") {
		t.Error("unregistered id still resolves")
	}
}

// Only a non-empty picture within the cap is served; SVG never.
func TestCheckInline(t *testing.T) {
	png := []byte("\x89PNG")
	if err := checkInline(png, "image/png"); err != nil {
		t.Errorf("png: %v", err)
	}
	if err := checkInline(png, " Image/JPEG; charset=binary "); err != nil {
		t.Errorf("parameters and case: %v", err)
	}
	bad := map[string]struct {
		data []byte
		ct   string
	}{
		"empty":     {nil, "image/png"},
		"svg":       {[]byte("<svg/>"), "image/svg+xml"},
		"html":      {[]byte("<p>"), "text/html"},
		"no type":   {png, ""},
		"over cap":  {make([]byte, maxCIDBytes+1), "image/png"},
		"imageless": {png, "imagex/png"},
	}
	for name, c := range bad {
		if err := checkInline(c.data, c.ct); err == nil || !strings.Contains(err.Error(), "inline image") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
