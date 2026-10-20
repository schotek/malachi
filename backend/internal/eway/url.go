// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// MaxServiceURLBytes caps EwayConfig.ServiceURL.
const MaxServiceURLBytes = 2048

// ErrHTTPOnly is the reason NormaliseServiceURL gives for an http URL; the
// wizard shows its own text for it (docs/eway.md D20).
const ErrHTTPOnly = "eway: only https service addresses are supported"

// NormaliseServiceURL makes the user's service address an EwayConfig
// ServiceURL: https (assumed when no scheme is given; http is refused), no
// user info, the query and fragment dropped, a trailing "/API.svc",
// "/API.svc/…" or "/InsecureAPI.svc…" the user pasted cut off, the host
// lower case, no trailing slash. Errors are invalidArgument.
func NormaliseServiceURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", api.NewError(api.CodeInvalidArgument, "eway: service address is required")
	}
	if len(s) > MaxServiceURLBytes {
		return "", api.NewError(api.CodeInvalidArgument, "eway: service address too long")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" {
		return "", api.NewError(api.CodeInvalidArgument, "eway: service address does not parse")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		return "", api.NewError(api.CodeInvalidArgument, ErrHTTPOnly)
	default:
		return "", api.NewError(api.CodeInvalidArgument, "eway: service address must be https")
	}
	if u.User != nil {
		return "", api.NewError(api.CodeInvalidArgument, "eway: service address must not carry a user name or password")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.ContainsAny(host, " \t\r\n") {
		return "", api.NewError(api.CodeInvalidArgument, "eway: service address has no host")
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if p := u.Port(); p != "" && p != "443" {
		host += ":" + p
	}
	path := u.EscapedPath()
	lower := strings.ToLower(path)
	for _, svc := range []string{"/insecureapi.svc", "/api.svc"} {
		if i := strings.Index(lower, svc); i >= 0 && (len(lower) == i+len(svc) || lower[i+len(svc)] == '/') {
			path, lower = path[:i], lower[:i]
		}
	}
	path = strings.TrimRight(path, "/")
	return "https://" + host + path, nil
}

// apiBase is the service's API endpoint.
func apiBase(serviceURL string) string { return serviceURL + "/API.svc" }

var guidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}$`)

// ValidGUID reports an item GUID of eWay-CRM's shape.
func ValidGUID(s string) bool { return guidPattern.MatchString(s) }

// openBase is eWay-CRM's link service, which opens an item in the user's
// web or desktop application.
const openBase = "https://open.eway-crm.com/"

// OpenLink is the link that opens the item of the folder (Emails,
// Contacts, Companies, Leads, Projects) in eWay-CRM (docs/eway.md D15):
// built from the stored service URL and a GUID of fixed shape, never from
// anything the server returned; "" for a GUID of another shape or an
// unknown folder. Base64url without padding.
func OpenLink(serviceURL, folder, guid string) string {
	if !ValidGUID(guid) || serviceURL == "" {
		return ""
	}
	switch folder {
	case FolderEmails, FolderContacts, FolderCompanies, FolderLeads, FolderProjects:
	default:
		return ""
	}
	enc := base64.RawURLEncoding
	return openBase + "?ws=" + enc.EncodeToString([]byte(serviceURL)) +
		"&l=" + enc.EncodeToString([]byte("eway://"+folder+"/"+strings.ToLower(guid)))
}

// ItemLink is OpenLink for an item of a kind.
func ItemLink(serviceURL string, kind api.CrmItemKind, guid string) string {
	return OpenLink(serviceURL, FolderOf(kind), guid)
}
