// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/jira"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Issue-tracker accounts (kind jira) in the account service: the two
// methods of the add-account flow (account.detectSite, account.listSpaces),
// the Jira probe of account.test and the validation of JiraConfig
// (docs/api.md §4.1). The token is the account's password (keyring
// auth.KeyPassword); it is sent only to the site it was stored for.

const (
	// listSpacesBudget bounds account.listSpaces, whose clients allow it
	// 45 s (docs/api.md): the sign-in, the lists and at most 20 s of
	// counting.
	listSpacesBudget = 40 * time.Second
	// jiraProbeBudget bounds the Jira probe of account.test.
	jiraProbeBudget = 30 * time.Second
	// maxJiraRefBytes bounds the id, key and name of a space or status in
	// a JiraConfig (the site's own are shorter).
	maxJiraRefBytes = 256
)

// uuidPattern is the shape of a cloud id.
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// DetectSite backs account.detectSite: what kind of site the URL names,
// asked anonymously (jira.DetectSite). Nothing is stored.
func (s *accountService) DetectSite(ctx context.Context, p api.AccountDetectSiteParams) (*api.AccountDetectSiteResult, error) {
	if strings.TrimSpace(p.URL) == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "url is required")
	}
	res, err := s.b.DetectJiraSite(ctx, p.URL)
	if err != nil {
		s.b.log.Info("jira site detection", "ok", false, "code", codeOf(err))
		return nil, apiError(err)
	}
	s.b.log.Info("jira site detection", "ok", true, "deployment", res.Deployment, "cloudId", res.CloudID != "")
	return &res, nil
}

// ListSpaces backs account.listSpaces: it signs in to the site of a jira
// configuration (its connection fields validated as for account.add, the
// spaces may be empty) with the given token, or with the stored token of
// accountId, and lists the user, the spaces and the statuses.
func (s *accountService) ListSpaces(ctx context.Context, p api.AccountListSpacesParams) (*api.AccountListSpacesResult, error) {
	cfg := p.Config
	if err := validateJiraConnection(&cfg); err != nil {
		return nil, err
	}
	if p.Credentials.OAuthSession != "" {
		return nil, api.NewError(api.CodeInvalidArgument, "oauthSession does not apply to a jira account")
	}
	token, err := s.jiraToken(ctx, p.AccountID, cfg, p.Credentials.Password)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, listSpacesBudget)
	defer cancel()
	res, err := s.b.ListJiraSpaces(ctx, *cfg.Jira, token, p.Counts)
	if err != nil {
		s.b.log.Info("jira list spaces", "ok", false, "code", codeOf(err))
		return nil, apiError(err)
	}
	if res.Spaces == nil {
		res.Spaces = []api.Space{}
	}
	if res.Statuses == nil {
		res.Statuses = []api.IssueStatus{}
	}
	s.b.log.Info("jira list spaces", "ok", true, "spaces", len(res.Spaces), "statuses", len(res.Statuses), "counts", p.Counts)
	return &res, nil
}

// jiraToken is the token account.listSpaces and account.test sign in
// with: the one given, else the stored one of accountID, which must be a
// jira account of the same site (a token never goes to another site);
// neither is authRequired.
func (s *accountService) jiraToken(ctx context.Context, accountID api.AccountID, cfg api.AccountConfig, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	if accountID == "" {
		return "", api.NewError(api.CodeAuthRequired, "no token given and no accountId")
	}
	a, err := s.b.requireAccount(ctx, string(accountID))
	if err != nil {
		return "", err
	}
	if !sameJiraSite(a.Config, cfg) {
		return "", api.NewError(api.CodeInvalidArgument, "the stored token of account %s belongs to another site; give the token", accountID)
	}
	return s.b.PasswordFor(ctx, a.ID)
}

// sameJiraSite reports whether both configurations are jira accounts of
// one site (store.RealmOf): the only case a stored token may be used for
// the other.
func sameJiraSite(stored, next api.AccountConfig) bool {
	if stored.Protocol() != api.AccountJira || next.Protocol() != api.AccountJira {
		return false
	}
	realm := store.RealmOf(stored)
	return realm != "" && realm == store.RealmOf(next)
}

// secretMoves reports whether account.update would send the stored
// password of an account where it was not given for: a jira account to
// another site, a mail account's password to a Jira site, or a Jira token
// to a mail server that takes a password.
func secretMoves(old, next api.AccountConfig) bool {
	switch {
	case next.Protocol() == api.AccountJira:
		return !sameJiraSite(old, next)
	case old.Protocol() == api.AccountJira:
		return usesAuth(next, api.AuthPassword)
	}
	return false
}

// testJira is account.test for a jira account: the site's probe with the
// token given or stored. A missing token without accountId is the
// endpoint's authRequired; the stored token's own failures fail the call
// (as the stored password of an IMAP account does).
func (s *accountService) testJira(ctx context.Context, p api.AccountTestParams) (*api.AccountTestResult, error) {
	cfg := p.Config
	var token string
	switch {
	case p.Credentials.Password != "":
		token = p.Credentials.Password
	case p.AccountID == "":
		r := endpointResult(nil, 0, api.NewError(api.CodeAuthRequired, "no token given and no accountId"))
		s.b.log.Info("account test", "kind", "jira", "ok", false, "code", r.Error.Code)
		return &api.AccountTestResult{Jira: r}, nil
	default:
		t, err := s.jiraToken(ctx, p.AccountID, cfg, "")
		if err != nil {
			return nil, err
		}
		token = t
	}
	ctx, cancel := context.WithTimeout(ctx, jiraProbeBudget)
	defer cancel()
	res, err := s.b.ProbeJira(ctx, *cfg.Jira, token)
	r := &res
	if err != nil {
		r = endpointResult(nil, time.Duration(res.LatencyMS)*time.Millisecond, apiError(err))
	} else if r.Capabilities == nil {
		r.Capabilities = []string{}
	}
	attrs := []any{"kind", "jira", "deployment", cfg.Jira.Deployment, "ok", r.OK, "latencyMs", r.LatencyMS}
	if r.Error != nil {
		attrs = append(attrs, "code", r.Error.Code)
	}
	s.b.log.Info("account test", attrs...)
	return &api.AccountTestResult{Jira: r}, nil
}

// apiError passes an *api.Error through and makes anything else a
// serverError with a cleaned message.
func apiError(err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return api.NewError(api.CodeServerError, "%s", transport.CleanMessage(err.Error()))
}

// capabilitiesFor is Account.Capabilities of an account: every mail
// capability for a mailbox (imap, graph); for an issue tracker a comment
// (reply writes one), the forward of its messages by e-mail from a mail
// account and the status transitions of its issues; nothing for a kind
// this daemon does not know. Never nil, so the list is always sent (nil
// would mean "a daemon without capabilities" to a client).
func capabilitiesFor(c api.AccountConfig) []api.AccountCapability {
	switch c.Protocol() {
	case api.AccountIMAP, api.AccountGraph:
		return append([]api.AccountCapability{}, api.MailCapabilities...)
	case api.AccountJira:
		return []api.AccountCapability{api.CapabilityComment, api.CapabilityForward, api.CapabilityTransition}
	default:
		return []api.AccountCapability{}
	}
}

// can reports whether an account of the configuration has the capability.
func can(c api.AccountConfig, capability api.AccountCapability) bool {
	for _, have := range capabilitiesFor(c) {
		if have == capability {
			return true
		}
	}
	return false
}

// validateJira checks the jira part of an account of kind jira against
// docs/api.md §4.1 and normalises it in place (the site URL, trimmed
// ids). Every failure is invalidArgument.
func validateJira(c *api.AccountConfig) error {
	if err := validateJiraConnection(c); err != nil {
		return err
	}
	j := c.Jira
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, "jira: "+format, args...)
	}
	if len(j.Spaces) == 0 {
		return bad("at least one space is required")
	}
	if len(j.Spaces) > api.MaxJiraSpaces {
		return bad("too many spaces (limit %d)", api.MaxJiraSpaces)
	}
	seen := make(map[string]bool, len(j.Spaces))
	for i := range j.Spaces {
		sp := &j.Spaces[i]
		sp.ID, sp.Key, sp.Name = strings.TrimSpace(sp.ID), strings.TrimSpace(sp.Key), strings.TrimSpace(sp.Name)
		switch {
		case sp.ID == "" || sp.Key == "":
			return bad("space %d needs an id and a key", i+1)
		case !jiraRefText(sp.ID) || !jiraRefText(sp.Key) || !jiraRefText(sp.Name):
			return bad("space %d: id, key and name must be valid UTF-8 without control characters (limit %d bytes)", i+1, maxJiraRefBytes)
		case seen[sp.ID]:
			return bad("space %q is listed twice", sp.ID)
		}
		seen[sp.ID] = true
	}
	return validateJiraOptions(j)
}

// validateJiraConnection checks what account.listSpaces needs of a
// configuration: kind jira with a jira block and nothing of the other
// kinds, the site URL (normalised in place as account.detectSite returns
// it), the deployment, the login and the cloud id, and the window the
// counts use. The spaces and the other options are not looked at.
func validateJiraConnection(c *api.AccountConfig) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	if c.Protocol() != api.AccountJira {
		return bad("kind must be jira")
	}
	if c.IMAP != nil || c.SMTP != nil || c.OAuth2 != nil || c.Graph != nil {
		return bad("imap, smtp, oauth2 and graph settings do not apply to a jira account")
	}
	j := c.Jira
	if j == nil {
		return bad("jira settings are required")
	}
	site, err := jira.NormaliseSiteURL(j.SiteURL)
	if err != nil {
		return err
	}
	u, err := url.Parse(site)
	if err != nil {
		return bad("jira: site URL does not parse")
	}
	j.SiteURL = site
	j.Login = strings.TrimSpace(j.Login)
	j.CloudID = strings.ToLower(strings.TrimSpace(j.CloudID))
	switch j.Deployment {
	case api.JiraCloud:
		if u.Scheme != "https" && !transport.IsLoopbackHost(u.Hostname()) {
			return bad("jira: a cloud site must use https")
		}
		if j.Login == "" {
			return bad("jira: login is required for a cloud site")
		}
		if err := validateAddress(api.Address{Address: j.Login}); err != nil {
			return bad("jira: login must be a bare address")
		}
		if j.CloudID != "" && !uuidPattern.MatchString(j.CloudID) {
			return bad("jira: cloudId must be a UUID")
		}
	case api.JiraDataCenter:
		if j.Login != "" {
			return bad("jira: login applies to a cloud site only")
		}
		if j.CloudID != "" {
			return bad("jira: cloudId applies to a cloud site only")
		}
	default:
		return bad("jira: deployment must be cloud or datacenter")
	}
	if j.OfflineDays < 0 || j.OfflineDays > api.MaxJiraOfflineDays {
		return bad("jira: offlineDays must be 0–%d", api.MaxJiraOfflineDays)
	}
	return nil
}

// validateJiraOptions checks the options past the connection and the
// spaces: views, closed statuses, the notification mail and the comment
// rules.
func validateJiraOptions(j *api.JiraConfig) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, "jira: "+format, args...)
	}
	views := map[api.VirtualFolder]bool{}
	for _, v := range j.DisabledFolders {
		switch v {
		case api.VirtualAssignedToMe, api.VirtualWatching, api.VirtualOpen:
		default:
			return bad("disabledFolders: unknown view %q", v)
		}
		if views[v] {
			return bad("disabledFolders: %q is listed twice", v)
		}
		views[v] = true
	}
	if len(j.ClosedStatuses) > api.MaxJiraStatuses {
		return bad("too many closedStatuses (limit %d)", api.MaxJiraStatuses)
	}
	for i := range j.ClosedStatuses {
		st := &j.ClosedStatuses[i]
		st.ID, st.Name = strings.TrimSpace(st.ID), strings.TrimSpace(st.Name)
		if st.ID == "" || !jiraRefText(st.ID) || !jiraRefText(st.Name) {
			return bad("closed status %d needs an id; id and name must be valid UTF-8 without control characters (limit %d bytes)", i+1, maxJiraRefBytes)
		}
	}
	switch j.NotificationMail {
	case "", api.NotificationMailSync, api.NotificationMailHide, api.NotificationMailIgnore:
	default:
		return bad("notificationMail must be sync, hide or ignore")
	}
	for name, list := range map[string][]string{
		"notificationSenders": j.NotificationSenders, "botNames": j.BotNames,
		"metadataFilters": j.MetadataFilters, "authorPrefixes": j.AuthorPrefixes,
	} {
		if len(list) > api.MaxJiraListEntries {
			return bad("too many %s (limit %d)", name, api.MaxJiraListEntries)
		}
		for i, e := range list {
			if len(e) > api.MaxJiraPatternBytes || !utf8.ValidString(e) || hasControl(e) {
				return bad("%s entry %d must be valid UTF-8 without control characters (limit %d bytes)", name, i+1, api.MaxJiraPatternBytes)
			}
		}
	}
	for i, p := range j.MetadataFilters {
		if _, err := regexp.Compile(p); err != nil {
			return bad("metadataFilters entry %d is not a valid RE2 pattern", i+1)
		}
	}
	for i := range j.NotificationSenders {
		sender := strings.ToLower(strings.TrimSpace(j.NotificationSenders[i]))
		if !validNotificationSender(sender) {
			return bad("notificationSenders entry %d must be addr@host or @host", i+1)
		}
		j.NotificationSenders[i] = sender
	}
	return nil
}

// validNotificationSender accepts "addr@host" (a bare address) and "@host"
// (any address of the host).
func validNotificationSender(s string) bool {
	if host, ok := strings.CutPrefix(s, "@"); ok {
		return transport.ValidHost(host)
	}
	return validateAddress(api.Address{Address: s}) == nil
}

// jiraRefText is a valid id, key or name of a space or status.
func jiraRefText(s string) bool {
	return len(s) <= maxJiraRefBytes && utf8.ValidString(s) && !hasControl(s)
}
