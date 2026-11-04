// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Limits of docs/services.md §2.3.
const (
	MaxReleases    = 32
	MaxPackages    = 16
	MaxNotesBytes  = 4000
	MaxNoticeBytes = 1000
	// maxLanguages bounds the languages of one text; the payload limit
	// bounds them anyway.
	maxLanguages = 64
	// maxSize is a sanity bound of Package.size (1 TiB).
	maxSize = 1 << 40
	// futureSkew is how far issuedAt may be ahead of the daemon's clock.
	futureSkew = 24 * time.Hour
)

// Channels are the packages a build can be installed from (§2.3). A
// manifest's other channels are ignored.
var Channels = []string{
	"macos-dmg",
	"windows-setup-x64", "windows-setup-arm64",
	"linux-deb-x64", "linux-deb-arm64",
	"linux-rpm-x64", "linux-rpm-arm64",
	"linux-flatpak-x64", "linux-flatpak-arm64",
}

// KnownChannel reports one of Channels.
func KnownChannel(c string) bool { return slices.Contains(Channels, c) }

// Manifest is an accepted payload.
type Manifest struct {
	IssuedAt time.Time
	Releases []Release // newest first
	Notice   *Notice
}

// Release is one version. Packages holds only known channels.
type Release struct {
	Version     Version
	PublishedAt time.Time
	Security    bool
	Withdrawn   bool
	Notes       map[string]string // by language; "en" always
	InfoURL     string
	Packages    map[string]Package
}

// Package is one installer of a release.
type Package struct {
	URL    string
	SHA256 string
	Size   int64
}

// Notice is a message to the users of some versions.
type Notice struct {
	ID    string
	Below *Version // nil: every version
	Text  map[string]string
	URL   string
}

var (
	reTime     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`)
	reInt      = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	reSHA256   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reNoticeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	reLanguage = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)
)

// Parse checks a verified payload against §2.3 and returns the manifest
// and the paths of what it ignored (unknown fields and channels). now is
// the daemon's clock, last the issuedAt of the last manifest it accepted
// (zero: none). Any violation is an *Error of kind BadManifest; one with
// Newer set is a schema this version cannot read.
func Parse(payload []byte, now, last time.Time) (*Manifest, []string, error) {
	c := &checker{}
	m, err := c.manifest(payload, now, last)
	if err != nil {
		return nil, nil, err
	}
	return m, c.warnings, nil
}

type checker struct {
	warnings []string
}

func fail(path, format string, a ...any) *Error {
	return &Error{Kind: BadManifest, Path: path, Msg: fmt.Sprintf(format, a...)}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func index(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }

// fields indexes an object's members. A key twice, or one that differs
// from a known key only in case, is an error; another unknown key is a
// warning (ignored).
func (c *checker) fields(v *value, path string, known []string) (map[string]*value, error) {
	if v.kind != kObject {
		return nil, fail(path, "not an object")
	}
	out := make(map[string]*value, len(v.obj))
	for _, m := range v.obj {
		p := join(path, m.key)
		if _, dup := out[m.key]; dup {
			return nil, fail(p, "key twice")
		}
		if twin(m.key, known) {
			return nil, fail(p, "differs from a known key only in case")
		}
		out[m.key] = m.val
		if !slices.Contains(known, m.key) {
			c.warnings = append(c.warnings, p)
		}
	}
	return out, nil
}

func required(f map[string]*value, path, key string) (*value, error) {
	v, ok := f[key]
	if !ok {
		return nil, fail(join(path, key), "missing")
	}
	return v, nil
}

func (c *checker) manifest(payload []byte, now, last time.Time) (*Manifest, error) {
	if !validUTF8(payload) {
		return nil, fail("", "not UTF-8")
	}
	if bytes.HasPrefix(payload, []byte{0xef, 0xbb, 0xbf}) {
		return nil, fail("", "starts with a byte order mark")
	}
	root, err := parseJSON(payload)
	if err != nil {
		return nil, fail("", "not JSON")
	}
	f, err := c.fields(root, "", []string{"schema", "issuedAt", "releases", "notice"})
	if err != nil {
		return nil, err
	}
	// The schema first: a newer one may mean anything below.
	sv, err := required(f, "", "schema")
	if err != nil {
		return nil, err
	}
	if sv.kind != kNumber || !reInt.MatchString(sv.num) {
		return nil, fail("schema", "not an integer")
	}
	switch sv.num {
	case "1":
	case "0":
		return nil, fail("schema", "schema 0")
	default:
		e := fail("schema", "schema %s is newer than this version reads", sv.num)
		e.Newer = true
		return nil, e
	}
	m := &Manifest{}
	iv, err := required(f, "", "issuedAt")
	if err != nil {
		return nil, err
	}
	if m.IssuedAt, err = timeValue(iv, "issuedAt"); err != nil {
		return nil, err
	}
	if !last.IsZero() && m.IssuedAt.Before(last) {
		return nil, fail("issuedAt", "older than the last accepted manifest")
	}
	if m.IssuedAt.After(now.Add(futureSkew)) {
		return nil, fail("issuedAt", "more than a day in the future")
	}
	rv, err := required(f, "", "releases")
	if err != nil {
		return nil, err
	}
	if rv.kind != kArray {
		return nil, fail("releases", "not an array")
	}
	if len(rv.arr) > MaxReleases {
		return nil, fail("releases", "more than %d releases", MaxReleases)
	}
	for i, r := range rv.arr {
		rel, err := c.release(r, index("releases", i))
		if err != nil {
			return nil, err
		}
		if i > 0 && rel.Version.Compare(m.Releases[i-1].Version) >= 0 {
			return nil, fail(join(index("releases", i), "version"), "not lower than the release before it")
		}
		m.Releases = append(m.Releases, rel)
	}
	if nv, ok := f["notice"]; ok {
		if m.Notice, err = c.notice(nv, "notice"); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (c *checker) release(v *value, path string) (Release, error) {
	var r Release
	f, err := c.fields(v, path, []string{"version", "publishedAt", "security", "withdrawn", "notes", "infoUrl", "packages"})
	if err != nil {
		return r, err
	}
	vv, err := required(f, path, "version")
	if err != nil {
		return r, err
	}
	if r.Version, err = versionValue(vv, join(path, "version")); err != nil {
		return r, err
	}
	pv, err := required(f, path, "publishedAt")
	if err != nil {
		return r, err
	}
	if r.PublishedAt, err = timeValue(pv, join(path, "publishedAt")); err != nil {
		return r, err
	}
	for _, k := range []string{"security", "withdrawn"} {
		bv, err := required(f, path, k)
		if err != nil {
			return r, err
		}
		if bv.kind != kBool {
			return r, fail(join(path, k), "not a boolean")
		}
		if k == "security" {
			r.Security = bv.boolean
		} else {
			r.Withdrawn = bv.boolean
		}
	}
	nv, err := required(f, path, "notes")
	if err != nil {
		return r, err
	}
	if r.Notes, err = c.texts(nv, join(path, "notes"), MaxNotesBytes); err != nil {
		return r, err
	}
	uv, err := required(f, path, "infoUrl")
	if err != nil {
		return r, err
	}
	if r.InfoURL, err = urlValue(uv, join(path, "infoUrl"), siteHosts); err != nil {
		return r, err
	}
	kv, err := required(f, path, "packages")
	if err != nil {
		return r, err
	}
	pp := join(path, "packages")
	if kv.kind != kObject {
		return r, fail(pp, "not an object")
	}
	if len(kv.obj) > MaxPackages {
		return r, fail(pp, "more than %d packages", MaxPackages)
	}
	pf, err := c.fields(kv, pp, Channels)
	if err != nil {
		return r, err
	}
	r.Packages = map[string]Package{}
	for _, m := range kv.obj {
		pkg, err := c.pkg(pf[m.key], join(pp, m.key))
		if err != nil {
			return r, err
		}
		if KnownChannel(m.key) {
			r.Packages[m.key] = pkg
		}
	}
	return r, nil
}

func (c *checker) pkg(v *value, path string) (Package, error) {
	var p Package
	f, err := c.fields(v, path, []string{"url", "sha256", "size"})
	if err != nil {
		return p, err
	}
	uv, err := required(f, path, "url")
	if err != nil {
		return p, err
	}
	if p.URL, err = urlValue(uv, join(path, "url"), downloadHosts); err != nil {
		return p, err
	}
	hv, err := required(f, path, "sha256")
	if err != nil {
		return p, err
	}
	if hv.kind != kString || !reSHA256.MatchString(hv.str) {
		return p, fail(join(path, "sha256"), "not 64 lowercase hex digits")
	}
	p.SHA256 = hv.str
	sv, err := required(f, path, "size")
	if err != nil {
		return p, err
	}
	if sv.kind != kNumber || !reInt.MatchString(sv.num) {
		return p, fail(join(path, "size"), "not an integer")
	}
	n, err := strconv.ParseInt(sv.num, 10, 64)
	if err != nil || n <= 0 || n > maxSize {
		return p, fail(join(path, "size"), "out of range")
	}
	p.Size = n
	return p, nil
}

func (c *checker) notice(v *value, path string) (*Notice, error) {
	f, err := c.fields(v, path, []string{"id", "below", "text", "url"})
	if err != nil {
		return nil, err
	}
	n := &Notice{}
	iv, err := required(f, path, "id")
	if err != nil {
		return nil, err
	}
	if iv.kind != kString || !reNoticeID.MatchString(iv.str) {
		return nil, fail(join(path, "id"), "not an id")
	}
	n.ID = iv.str
	if bv, ok := f["below"]; ok {
		b, err := versionValue(bv, join(path, "below"))
		if err != nil {
			return nil, err
		}
		n.Below = &b
	}
	tv, err := required(f, path, "text")
	if err != nil {
		return nil, err
	}
	if n.Text, err = c.texts(tv, join(path, "text"), MaxNoticeBytes); err != nil {
		return nil, err
	}
	if uv, ok := f["url"]; ok {
		if n.URL, err = urlValue(uv, join(path, "url"), siteHosts); err != nil {
			return nil, err
		}
	}
	return n, nil
}

// texts reads {language: text}, which must have "en".
func (c *checker) texts(v *value, path string, maxBytes int) (map[string]string, error) {
	if v.kind != kObject {
		return nil, fail(path, "not an object")
	}
	if len(v.obj) > maxLanguages {
		return nil, fail(path, "more than %d languages", maxLanguages)
	}
	out := make(map[string]string, len(v.obj))
	for _, m := range v.obj {
		p := join(path, m.key)
		if _, dup := out[m.key]; dup {
			return nil, fail(p, "key twice")
		}
		if !reLanguage.MatchString(m.key) {
			return nil, fail(p, "not a language tag")
		}
		if m.val.kind != kString {
			return nil, fail(p, "not a string")
		}
		if len(m.val.str) > maxBytes {
			return nil, fail(p, "more than %d bytes", maxBytes)
		}
		if why := textProblem(m.val.str, m.val.bad); why != "" {
			return nil, fail(p, "%s", why)
		}
		out[m.key] = m.val.str
	}
	if _, ok := out["en"]; !ok {
		return nil, fail(join(path, "en"), "missing")
	}
	return out, nil
}

func versionValue(v *value, path string) (Version, error) {
	if v.kind != kString {
		return Version{}, fail(path, "not a string")
	}
	ver, ok := ParseVersion(v.str)
	if !ok {
		return Version{}, fail(path, "not MAJOR.MINOR.PATCH")
	}
	return ver, nil
}

// timeValue reads an RFC 3339 time in UTC written with "Z".
func timeValue(v *value, path string) (time.Time, error) {
	if v.kind != kString || !reTime.MatchString(v.str) {
		return time.Time{}, fail(path, "not an RFC 3339 UTC time")
	}
	t, err := time.Parse(time.RFC3339Nano, v.str)
	if err != nil {
		return time.Time{}, fail(path, "not an RFC 3339 UTC time")
	}
	return t, nil
}

// Hosts a URL of the manifest may name.
var (
	downloadHosts = []string{"github.com", "malachimail.app"}
	siteHosts     = []string{"malachimail.app"}
)

// urlValue accepts an https URL on one of hosts, written exactly so: the
// host in lower case, no port (not even :443), no credentials, no
// fragment, nothing outside printable ASCII.
func urlValue(v *value, path string, hosts []string) (string, error) {
	if v.kind != kString {
		return "", fail(path, "not a string")
	}
	if !AllowedURL(v.str, hosts) {
		return "", fail(path, "not an https URL on %s", strings.Join(hosts, " or "))
	}
	return v.str, nil
}

// AllowedURL is the URL rule of urlValue. Clients get only URLs that
// passed it; the daemon checks them again before it hands one out.
func AllowedURL(s string, hosts []string) bool {
	if len(s) > 2048 || !strings.HasPrefix(s, "https://") || strings.ContainsAny(s, "#\\") {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] >= 0x7f {
			return false
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return slices.Contains(hosts, u.Host) // u.Host keeps a port; "host:443" is not in hosts
}
