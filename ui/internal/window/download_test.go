// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"
	"fmt"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/widget"
)

// withDownload: a remote part downloads first, a local one only when the
// daemon says it is not on this computer, and never more than one download
// and one retry; after a download the part is asked for as the downloaded
// message lists it, and not at all when it lists no such part.
func TestWithDownload(t *testing.T) {
	notHere := api.NewError(api.CodePartNotDownloaded, "remote")
	offline := api.NewError(api.CodeOffline, "no network")
	pdf := api.Attachment{PartID: "2", Filename: "report.pdf", ContentType: "application/pdf"}
	moved := &api.Message{Attachments: []api.Attachment{
		{PartID: "2", Filename: "logo.png", ContentType: "image/png"},
		{PartID: "3", Filename: "report.pdf", ContentType: "application/pdf"},
	}}
	gone := &api.Message{Attachments: []api.Attachment{{PartID: "2", Filename: "logo.png", ContentType: "image/png"}}}
	cases := []struct {
		name       string
		remote     bool
		fetches    []error      // what each fetch answers, in order
		downloaded *api.Message // what the download answers with
		download   error
		want       error
		parts      []string // the part of each fetch, in order
		downloads  int
	}{
		{"local part", false, []error{nil}, nil, nil, nil, []string{"2"}, 0},
		{"local part, other error", false, []error{offline}, nil, nil, offline, []string{"2"}, 0},
		{"moved to the server since", false, []error{notHere, nil}, nil, nil, nil, []string{"2", "2"}, 1},
		{"moved, download fails", false, []error{notHere}, nil, offline, offline, []string{"2"}, 1},
		{"moved, still not here: no loop", false, []error{notHere, notHere}, nil, nil, notHere, []string{"2", "2"}, 1},
		{"remote part", true, []error{nil}, nil, nil, nil, []string{"2"}, 1},
		{"remote part, download fails", true, nil, nil, offline, offline, nil, 1},
		{"remote part, still not here: no loop", true, []error{notHere}, nil, nil, notHere, []string{"2"}, 1},
		{"remote part renumbered", true, []error{nil}, moved, nil, nil, []string{"3"}, 1},
		{"local part renumbered on the retry", false, []error{notHere, nil}, moved, nil, nil, []string{"2", "3"}, 1},
		{"remote part gone: not fetched", true, nil, gone, nil, errPartNotFound, nil, 1},
		{"local part gone on the retry: not fetched again", false, []error{notHere}, gone, nil, errPartNotFound, []string{"2"}, 1},
	}
	for _, c := range cases {
		var parts []string
		downloads := 0
		got, err := withDownload(pdf, c.remote, func(a api.Attachment) (string, error) {
			if len(parts) >= len(c.fetches) {
				t.Fatalf("%s: fetch %d not expected", c.name, len(parts)+1)
			}
			err := c.fetches[len(parts)]
			parts = append(parts, a.PartID)
			if err != nil {
				return "", err
			}
			return "data of " + a.PartID, nil
		}, func() (*api.Message, error) {
			downloads++
			if c.download != nil {
				return nil, c.download
			}
			return c.downloaded, nil
		})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err %v, want %v", c.name, err, c.want)
		}
		if err == nil && got != "data of "+parts[len(parts)-1] {
			t.Errorf("%s: got %q from the fetches of %v", c.name, got, parts)
		}
		if err != nil && got != "" {
			t.Errorf("%s: a value with an error: %q", c.name, got)
		}
		if fmt.Sprint(parts) != fmt.Sprint(c.parts) || downloads != c.downloads {
			t.Errorf("%s: fetched %v with %d downloads; want %v, %d", c.name, parts, downloads, c.parts, c.downloads)
		}
	}
}

func TestPartNotDownloaded(t *testing.T) {
	if !partNotDownloaded(api.NewError(api.CodePartNotDownloaded, "x")) {
		t.Error("1504")
	}
	if !partNotDownloaded(fmt.Errorf("wrapped: %w", api.NewError(api.CodePartNotDownloaded, "x"))) {
		t.Error("wrapped 1504")
	}
	for _, err := range []error{nil, errors.New("x"), api.NewError(api.CodePartNotFound, "x"), errPartNotFound, client.ErrDisconnected} {
		if partNotDownloaded(err) {
			t.Errorf("%v taken for 1504", err)
		}
	}
}

// A part the downloaded message no longer lists is said like one the
// daemon does not have, through the action's usual toast.
func TestErrPartNotFoundText(t *testing.T) {
	for _, what := range []string{"Opening the attachment", "Saving the attachment", "Opening the attached message"} {
		if got := widget.RPCErrorText(what, errPartNotFound); got != "The attachment no longer exists" {
			t.Errorf("%s: %q", what, got)
		}
	}
}

func TestMethodUnsupported(t *testing.T) {
	for _, err := range []error{api.NewError(api.CodeMethodNotFound, "x"), api.ErrNotImplemented} {
		if !methodUnsupported(err) {
			t.Errorf("%v: should be unsupported", err)
		}
	}
	for _, err := range []error{nil, errors.New("x"), api.NewError(api.CodeUnavailable, "x"), client.ErrDisconnected} {
		if methodUnsupported(err) {
			t.Errorf("%v: taken for unsupported", err)
		}
	}
}

// After a download the part is found again by number while it is the
// same file, else by name and type (Microsoft 365 rebuilds the message);
// otherwise it is not found, and its old number, which may name another
// file by now, is never used.
func TestPartAfterDownload(t *testing.T) {
	pdf := api.Attachment{PartID: "2", Filename: "report.pdf", ContentType: "application/pdf", Size: 300 << 10, Remote: true}
	cases := []struct {
		name string
		m    *api.Message
		want string // "" = not found
	}{
		{"no message", nil, "2"},
		{"same number, same file", &api.Message{Attachments: []api.Attachment{
			{PartID: "2", Filename: "report.pdf", ContentType: "application/pdf"},
			{PartID: "3", Filename: "report.pdf", ContentType: "application/pdf"},
		}}, "2"},
		{"renumbered", &api.Message{Attachments: []api.Attachment{
			{PartID: "2", Filename: "logo.png", ContentType: "image/png"},
			{PartID: "3", Filename: "report.pdf", ContentType: "application/pdf"},
		}}, "3"},
		{"renumbered, two candidates", &api.Message{Attachments: []api.Attachment{
			{PartID: "3", Filename: "report.pdf", ContentType: "application/pdf"},
			{PartID: "4", Filename: "report.pdf", ContentType: "application/pdf"},
		}}, ""},
		{"gone, another file under the number", &api.Message{Attachments: []api.Attachment{
			{PartID: "2", Filename: "other.pdf", ContentType: "application/pdf"},
		}}, ""},
		{"another type under the name", &api.Message{Attachments: []api.Attachment{
			{PartID: "3", Filename: "report.pdf", ContentType: "application/octet-stream"},
		}}, ""},
		{"no attachments left", &api.Message{}, ""},
	}
	for _, c := range cases {
		got, ok := partAfterDownload(pdf, c.m)
		switch {
		case c.want == "" && ok:
			t.Errorf("%s: found part %q, want none", c.name, got.PartID)
		case c.want != "" && (!ok || got.PartID != c.want):
			t.Errorf("%s: part %q (found %v), want %q", c.name, got.PartID, ok, c.want)
		case c.m != nil && ok && got.Remote:
			t.Errorf("%s: the downloaded entry should be used", c.name)
		}
	}
}
