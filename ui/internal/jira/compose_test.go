// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jira

import (
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func vis(v ...api.CommentVisibility) api.IssueInfo {
	return api.IssueInfo{Key: "ITSD-42", CommentVisibilities: v}
}

func TestVisibilityOptions(t *testing.T) {
	both := []VisibilityOption{
		{Visibility: api.CommentPublic, Label: "Reply to Customer"},
		{Visibility: api.CommentInternal, Label: "Internal Note"},
	}
	tests := []struct {
		name  string
		issue api.IssueInfo
		want  []VisibilityOption
	}{
		{"none", vis(), nil},
		{"public only", vis(api.CommentPublic), nil},
		{"internal only", vis(api.CommentInternal), nil},
		{"both", vis(api.CommentPublic, api.CommentInternal), both},
		{"both, other order", vis(api.CommentInternal, api.CommentPublic), both},
		{"twice public", vis(api.CommentPublic, api.CommentPublic), nil},
		{"unknown value", vis(api.CommentPublic, api.CommentInternal, "partners"), nil},
	}
	for _, tt := range tests {
		if got := VisibilityOptions(tt.issue, tr); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: VisibilityOptions = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestSelectedVisibility(t *testing.T) {
	tests := []struct {
		c    api.DraftComment
		want api.CommentVisibility
	}{
		{api.DraftComment{Issue: vis(api.CommentPublic, api.CommentInternal)}, api.CommentPublic},
		{api.DraftComment{Issue: vis(api.CommentPublic, api.CommentInternal), Visibility: api.CommentInternal}, api.CommentInternal},
		{api.DraftComment{Issue: vis(api.CommentPublic, api.CommentInternal), Visibility: api.CommentPublic}, api.CommentPublic},
		{api.DraftComment{Issue: vis(), Visibility: api.CommentInternal}, api.CommentPublic},
		{api.DraftComment{Issue: vis(api.CommentPublic, api.CommentInternal), Visibility: "secret"}, api.CommentPublic},
	}
	for _, tt := range tests {
		if got := SelectedVisibility(tt.c); got != tt.want {
			t.Errorf("SelectedVisibility(%+v) = %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestCommentCompose(t *testing.T) {
	if _, ok := CommentCompose(api.Draft{Subject: "Re: hello"}, tr); ok {
		t.Error("a mail draft has no comment mode")
	}
	d := api.Draft{Comment: &api.DraftComment{
		Issue:      api.IssueInfo{Key: "ITSD-42" + rlo, CommentVisibilities: []api.CommentVisibility{api.CommentPublic, api.CommentInternal}},
		Visibility: api.CommentInternal,
	}}
	w, ok := CommentCompose(d, tr)
	if !ok || w.Title != "Comment on ITSD-42" || len(w.Visibilities) != 2 || w.Visibility != api.CommentInternal {
		t.Errorf("CommentCompose = %+v, %v", w, ok)
	}
	if !reflect.DeepEqual(w.Formats, CommentFormats) {
		t.Errorf("Formats = %q", w.Formats)
	}
	w.Formats[0] = FormatImage
	if CommentFormats[0] != FormatBold {
		t.Error("changing the window's formats changed CommentFormats")
	}
	plain, _ := CommentCompose(api.Draft{Comment: &api.DraftComment{Issue: api.IssueInfo{Key: "WEB-7"}}}, tr)
	if plain.Visibilities != nil || plain.Visibility != api.CommentPublic || plain.Title != "Comment on WEB-7" {
		t.Errorf("plain issue = %+v", plain)
	}
}

func TestCommentAllows(t *testing.T) {
	allowed := map[Format]bool{
		FormatBold: true, FormatItalic: true, FormatCode: true, FormatLink: true,
		FormatBulletList: true, FormatNumberedList: true, FormatQuote: true, FormatClear: true,
		FormatUnderline: false, FormatHeading: false, FormatAlignment: false, FormatColour: false, FormatImage: false,
		"strike": false,
	}
	for f, want := range allowed {
		if got := CommentAllows(f); got != want {
			t.Errorf("CommentAllows(%q) = %v, want %v", f, got, want)
		}
	}
}

func TestSendProblem(t *testing.T) {
	const empty = "Write a comment first"
	tests := map[string]string{
		"":                               empty,
		" \n\t ":                         empty,
		zwsp + bom + " " + shy:           empty,
		string(rune(0x00A0)):             empty, // NO-BREAK SPACE, as an empty editor paragraph leaves
		"Restarted the VPN concentrator": "",
		" ok ":                           "",
	}
	for in, want := range tests {
		if got := SendProblem(in, tr); got != want {
			t.Errorf("SendProblem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeLabels(t *testing.T) {
	if ReplyLabel(true, tr) != "Comment" || ReplyLabel(false, tr) != "Reply" {
		t.Error("ReplyLabel")
	}
	if CommentQueued(tr) != "Comment queued" {
		t.Error("CommentQueued")
	}
	if got := CommentTitle(" WEB-7\n", tr); got != "Comment on WEB-7" {
		t.Errorf("CommentTitle = %q", got)
	}
}
