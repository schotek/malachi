// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Pictures and links of the site's rendered HTML. The HTML is streamed
// through the tokenizer, never parsed into a tree (a hostile page cannot
// make it grow or nest), and written back as it came except for the tags
// whose href or src changes:
//
//   - a relative link (Jira renders "/secure/attachment/…",
//     "/browse/KEY-1") becomes absolute against the site, so the message
//     makes sense outside the site and the sanitiser can judge it;
//   - an <img> the site itself serves (its origin and path, or the
//     account's API gateway route) is downloaded with the account's
//     credentials, sniffed, and embedded as a cid: part: at most
//     maxPictures, each at most maxPartBytes, within the message's
//     budget, never SVG (a document with scripts of its own), never
//     anything that does not sniff as a picture. Any other picture stays
//     the link it is: the daemon never fetches a URL outside the site on
//     the site's behalf (the viewer's remote-content policy decides, as
//     for mail).
//
// A picture the site will not hand over (gone, refused, too big, not a
// picture) keeps its absolute link; a failure of the site or the network
// as a whole fails the build, so a message is not stored without a
// picture it should have.

// attachmentPath finds the attachment id in a site path of an attachment
// or its thumbnail, on either flavour and route.
var attachmentPath = regexp.MustCompile(`/(?:secure/(?:attachment|thumbnail)|rest/api/[23]/attachment/(?:content|thumbnail))/([0-9]{1,18})(?:/|$)`)

// rewriteAttrs streams src through the tokenizer and hands every href and
// src attribute of a start tag to fn with the tag's name; fn changes the
// value in place and reports whether it did. A tag that changed is
// written again (attribute values escaped), everything else as it came.
func rewriteAttrs(src string, fn func(tag string, a *html.Attribute) bool) string {
	if !strings.Contains(src, "<") {
		return src
	}
	z := html.NewTokenizer(strings.NewReader(src))
	var b strings.Builder
	b.Grow(len(src))
	consumed := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() != io.EOF || consumed > len(src) {
				return src // the tokenizer never fails on a string; be safe
			}
			// A tag cut off by the end is no token: it stays as it was.
			b.WriteString(src[consumed:])
			return b.String()
		}
		raw := z.Raw()
		consumed += len(raw)
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			b.Write(raw)
			continue
		}
		kept := string(raw)
		tok := z.Token()
		changed := false
		for i := range tok.Attr {
			a := &tok.Attr[i]
			if a.Namespace != "" || (a.Key != "href" && a.Key != "src") {
				continue
			}
			if fn(tok.Data, a) {
				changed = true
			}
		}
		if changed {
			b.WriteString(tok.String())
		} else {
			b.WriteString(kept)
		}
	}
}

// resolve makes a link absolute against the site: an http(s) URL without
// user info comes back absolute (without its fragment), anything else
// (another scheme, data:, cid:, something that does not parse) as "".
func (y *synth) resolve(ref string) string {
	return absoluteURL(y.base, ref)
}

// absolutise rewrites the relative links of the HTML; a link within the
// page ("#section") stays as it is, a resolved one keeps its fragment.
func (y *synth) absolutise(src string) string {
	return rewriteAttrs(src, func(_ string, a *html.Attribute) bool {
		v := strings.TrimSpace(a.Val)
		if v == "" || v[0] == '#' || hasScheme(v) {
			return false
		}
		abs := y.resolve(v)
		if abs == "" {
			return false
		}
		if _, frag, ok := strings.Cut(v, "#"); ok && frag != "" {
			abs += "#" + frag
		}
		if abs == a.Val {
			return false
		}
		a.Val = abs
		return true
	})
}

// hasScheme reports a URL that names its scheme ("https:", "data:",
// "mailto:"): nothing to resolve.
func hasScheme(v string) bool {
	i := strings.IndexByte(v, ':')
	if i <= 0 {
		return false
	}
	for j := 0; j < i; j++ {
		c := v[j]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.')) {
			return false
		}
	}
	return true
}

// withinSite reports whether an absolute URL is the site's own (or the
// gateway route's): the only URLs the daemon downloads.
func (y *synth) withinSite(abs string) bool {
	if y.client == nil || abs == "" {
		return false
	}
	_, err := y.client.routePath(abs)
	return err == nil
}

// attachmentRefs lists the attachment ids the links and pictures of the
// HTML point at (on the site).
func (y *synth) attachmentRefs(src string) map[string]bool {
	refs := map[string]bool{}
	rewriteAttrs(src, func(_ string, a *html.Attribute) bool {
		abs := y.resolve(strings.TrimSpace(a.Val))
		if abs == "" || !y.withinSite(abs) {
			return false
		}
		u, err := url.Parse(abs)
		if err != nil {
			return false
		}
		if m := attachmentPath.FindStringSubmatch(u.EscapedPath()); m != nil {
			refs[m[1]] = true
		}
		return false
	})
	return refs
}

// embedPictures downloads the site pictures of the (absolutised) HTML
// within budget and returns them with the HTML pointing at them, and what
// they took of the budget.
func (y *synth) embedPictures(ctx context.Context, msgID, src string, atts []Attachment, budget int64) ([]part, string, int64, error) {
	// The pictures, in order of first appearance.
	var order []string
	seen := map[string]bool{}
	rewriteAttrs(src, func(tag string, a *html.Attribute) bool {
		if tag == "img" && a.Key == "src" && !seen[a.Val] && y.withinSite(a.Val) {
			seen[a.Val] = true
			order = append(order, a.Val)
		}
		return false
	})
	if len(order) == 0 {
		return nil, src, 0, nil
	}
	names := map[string]string{} // attachment id → file name
	for _, a := range atts {
		names[a.ID] = a.Filename
	}
	cids := map[string]string{}
	var pictures []part
	var spent int64
	limit := maxPictures
	if y.picturesCap > 0 {
		limit = min(limit, y.picturesCap)
	}
	for _, u := range order {
		if len(pictures) == limit || budget-spent <= 0 {
			break
		}
		data, ctype, ok, err := y.download(ctx, u, min(maxPartBytes, budget-spent), true)
		if err != nil {
			return nil, "", 0, err
		}
		if !ok {
			continue
		}
		n := len(pictures) + 1
		// The message's own id, numbered: unique, and the same again on
		// a rebuild.
		cid := fmt.Sprintf("img%d.%s", n, msgID)
		pictures = append(pictures, part{filename: y.pictureName(u, names, ctype, n), contentType: ctype, cid: cid, data: data})
		cids[u] = cid
		spent += int64(len(data))
	}
	if len(pictures) == 0 {
		return nil, src, 0, nil
	}
	out := rewriteAttrs(src, func(tag string, a *html.Attribute) bool {
		if tag != "img" || a.Key != "src" {
			return false
		}
		cid, ok := cids[a.Val]
		if !ok {
			return false
		}
		a.Val = "cid:" + cid
		return true
	})
	return pictures, out, spent, nil
}

// pictureName names a picture: the attachment's file name when the link
// is an attachment's, else the link's last path segment, else "image<n>"
// with the extension of its type.
func (y *synth) pictureName(u string, names map[string]string, ctype string, n int) string {
	if pu, err := url.Parse(u); err == nil {
		if m := attachmentPath.FindStringSubmatch(pu.EscapedPath()); m != nil && names[m[1]] != "" {
			return names[m[1]]
		}
		if base := path.Base(pu.Path); base != "" && base != "." && base != "/" && strings.Contains(base, ".") {
			return cleanFilename(flexString(base))
		}
	}
	ext := strings.TrimPrefix(ctype, "image/")
	if ext == "" || strings.ContainsAny(ext, "+;. ") {
		ext = "img"
	}
	return fmt.Sprintf("image%d.%s", n, ext)
}

// fetchFile downloads an attachment for a message: false when it does not
// fit the budget or the site will not hand it over (the message then
// carries a link instead).
func (y *synth) fetchFile(ctx context.Context, a Attachment, budget int64) (part, bool, error) {
	limit := min(maxPartBytes, budget)
	if limit <= 0 || a.Size > limit || a.ContentURL == "" {
		return part{}, false, nil
	}
	data, ctype, ok, err := y.download(ctx, a.ContentURL, limit, false)
	if err != nil || !ok {
		return part{}, false, err
	}
	name := a.Filename
	if name == "" {
		name = a.ID
	}
	return part{filename: name, contentType: ctype, data: data}, true, nil
}

// download fetches a picture or a file of the site, at most limit bytes,
// and returns its bytes with the type they sniff as (the site's claim is
// not trusted). ok is false for content the site will not hand over or
// that is not what the caller takes; err is a failure of the site or the
// network as a whole (the pass is retried).
func (y *synth) download(ctx context.Context, u string, limit int64, picture bool) ([]byte, string, bool, error) {
	if y.remote == nil || !y.withinSite(u) {
		return nil, "", false, nil
	}
	c, err := y.remote.OpenContent(ctx, u, limit)
	if err != nil {
		if contentRefused(err) {
			y.log.Debug("jira content left out", "err", ToAPIError(err).Message)
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	data, err := io.ReadAll(c.Body)
	c.Body.Close()
	switch {
	case errors.Is(err, ErrTooLarge):
		return nil, "", false, nil
	case err != nil:
		return nil, "", false, ToAPIError(err)
	}
	if picture {
		mt := sniffPicture(data)
		return data, mt, mt != "", nil
	}
	return data, sniffFile(data), true, nil
}

// contentRefused reports a download failure that concerns that content
// only: it is too big, outside the site, gone or refused (a 4xx other than
// the token's and the rate limiter's), or it redirected somewhere it may
// not.
func contentRefused(err error) bool {
	if errors.Is(err, ErrTooLarge) {
		return true
	}
	var se *StatusError
	if errors.As(err, &se) {
		switch se.Status {
		case http.StatusUnauthorized, http.StatusRequestTimeout, http.StatusTooManyRequests:
			return false
		}
		return se.Status >= 400 && se.Status < 500
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.Code == api.CodeInvalidArgument || ae.Code == api.CodeServerError
	}
	return false
}

// sniffPicture is the media type of a picture the view may show, "" for
// anything else. SVG is never one, whatever the site calls it.
func sniffPicture(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	mt := mediaTypeOf(http.DetectContentType(data))
	if !strings.HasPrefix(mt, "image/") || mt == "image/svg+xml" {
		return ""
	}
	return mt
}

// sniffFile is the type a file is stored as: what its bytes sniff as, a
// generic type for markup (an attachment is never a document the view
// could take for the message).
func sniffFile(data []byte) string {
	ct := http.DetectContentType(data)
	switch mediaTypeOf(ct) {
	case "text/html", "text/xml", "image/svg+xml", "application/xml":
		return "application/octet-stream"
	}
	return ct
}

func mediaTypeOf(ct string) string {
	mt, _, _ := strings.Cut(ct, ";")
	return strings.ToLower(strings.TrimSpace(mt))
}
