// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/internal/llm"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// AI provider accounts (docs/api.md §4.1 and §4.18; docs/plans/ai-api.md
// D3): validation of the kind and the ai part of account.add,
// account.update and account.test. The API key is Credentials.Password
// in the keyring (auth.KeyPassword) and goes only to the host of the
// account's effective base URL (internal/llm). An ai account has no mail,
// no supervisor (the kind table's noopSupervisor) and the one capability
// CapabilityAssistant; the daemon's assistant runs over it
// (assistant_sessions.go).

const (
	maxAIModelBytes   = 256
	maxAIBaseURLBytes = 2048
	aiModelsTimeout   = 30 * time.Second
)

// validateAI checks an account of kind ai: the ai block and nothing of
// mail, issue trackers, calendars or CRM. The base URL is normalised in
// place. Every failure is invalidArgument.
func validateAI(c *api.AccountConfig) error { return validateAIConfig(c, true) }

// validateAIConfig is validateAI; modelRequired is false for
// account.test, which lists the models the user then chooses from, so a
// test before the choice has an empty model (an invalid one still
// fails).
func validateAIConfig(c *api.AccountConfig, modelRequired bool) error {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	if c.IMAP != nil || c.SMTP != nil || c.OAuth2 != nil || c.Graph != nil || c.Jira != nil || c.CalDAV != nil || c.Eway != nil || c.Calendar != nil {
		return bad("imap, smtp, oauth2, graph, jira, caldav, eway and calendar settings do not apply to an ai account")
	}
	a := c.AI
	if a == nil {
		return bad("ai settings are required")
	}
	if !a.Provider.Valid() {
		return bad("ai: provider must be anthropic, openai or openaiCompatible")
	}
	a.BaseURL = strings.TrimSpace(a.BaseURL)
	if a.Provider == api.AIOpenAICompatible {
		u, err := normaliseAIBaseURL(a.BaseURL)
		if err != nil {
			return err
		}
		a.BaseURL = u
	} else if a.BaseURL != "" {
		return bad("ai: baseUrl applies to an openaiCompatible account only")
	}
	a.Model = strings.TrimSpace(a.Model)
	if a.Model == "" && modelRequired {
		return bad("ai: model is required")
	}
	a.BoardModel = strings.TrimSpace(a.BoardModel)
	for _, m := range []string{a.Model, a.BoardModel} {
		if len(m) > maxAIModelBytes || !utf8.ValidString(m) || hasControl(m) {
			return bad("ai: a model id must be valid UTF-8 without control characters (limit %d bytes)", maxAIModelBytes)
		}
	}
	a.CertificateSHA256 = strings.TrimSpace(a.CertificateSHA256)
	if a.CertificateSHA256 != "" {
		pin, ok := api.NormalizeCertificateSHA256(a.CertificateSHA256)
		if !ok {
			return bad("ai: certificateSha256 must be a SHA-256 fingerprint of 64 hex digits")
		}
		if !strings.HasPrefix(a.EffectiveBaseURL(), "https://") {
			return bad("ai: certificateSha256 needs an https base URL")
		}
		a.CertificateSHA256 = pin
	}
	if a.Consent != nil && a.Consent.Version < 0 {
		return bad("ai: consent.version must not be negative")
	}
	return nil
}

// normaliseAIBaseURL is the stored form of an OpenAI-compatible server's
// base URL: http or https, a host, no user info, query or fragment, the
// host lower case, no trailing slash, at most maxAIBaseURLBytes.
func normaliseAIBaseURL(raw string) (string, error) {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", bad("ai: baseUrl is required for an openaiCompatible account")
	}
	if len(raw) > maxAIBaseURLBytes {
		return "", bad("ai: baseUrl is too long (limit %d bytes)", maxAIBaseURLBytes)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", bad("ai: baseUrl is not a URL")
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return "", bad("ai: baseUrl must start with http:// or https://")
	case u.Host == "" || u.Hostname() == "":
		return "", bad("ai: baseUrl needs a host")
	case u.User != nil:
		return "", bad("ai: baseUrl must not carry a user name or password")
	case u.Opaque != "":
		return "", bad("ai: baseUrl is not a URL")
	}
	host := strings.ToLower(u.Host)
	path := strings.TrimRight(u.EscapedPath(), "/")
	return u.Scheme + "://" + host + path, nil
}

// isAIAccount: an account of kind ai (no mail, no folders, no board).
func isAIAccount(a store.Account) bool { return a.Config.Protocol() == api.AccountAI }

// sameAIEndpoint: the same provider and effective base URL (realm).
func sameAIEndpoint(a, b api.AccountConfig) bool {
	return a.AI != nil && b.AI != nil && a.AI.Provider == b.AI.Provider && store.RealmOf(a) == store.RealmOf(b)
}

// aiSecretMoves reports an update after which the stored key would go to
// another provider or host: such an update needs its own key.
func aiSecretMoves(old, next api.AccountConfig) bool {
	switch {
	case next.Protocol() == api.AccountAI:
		return old.Protocol() != api.AccountAI || !sameAIEndpoint(old, next)
	case old.Protocol() == api.AccountAI:
		return usesAuth(next, api.AuthPassword) || next.Protocol() == api.AccountJira ||
			next.Protocol() == api.AccountCalDAV || next.Protocol() == api.AccountEway
	}
	return false
}

// settleAIConsent decides the stored consent of next from what the client
// sent and what old (the stored configuration, nil on add) had: a sent
// version higher than the stored one is accepted now (the daemon sets
// acceptedAt); none, zero, or a lower one keeps the stored consent.
func settleAIConsent(next *api.AIConfig, old *api.AIConfig, now time.Time) {
	var stored *api.AIConsent
	if old != nil {
		stored = old.Consent
	}
	switch {
	case next.Consent == nil || next.Consent.Version <= 0:
		next.Consent = stored
	case stored != nil && stored.Version >= next.Consent.Version:
		next.Consent = stored
	default:
		at := now.UTC().Truncate(time.Second)
		next.Consent = &api.AIConsent{Version: next.Consent.Version, AcceptedAt: &at}
	}
}

// aiUserAgent is what the daemon sends to a model API.
func aiUserAgent(version string) string { return "MalachiMail/" + version }

// aiProvider is the model API client of a configuration and key.
func (b *Backend) aiProvider(cfg api.AIConfig, key string) (llm.Provider, error) {
	return b.AIProvider(llm.Config{
		Provider:          cfg.Provider,
		BaseURL:           cfg.EffectiveBaseURL(),
		Key:               key,
		CertificateSHA256: cfg.CertificateSHA256,
		UserAgent:         aiUserAgent(b.Version),
	})
}

// defaultAIProvider builds the real clients (internal/llm).
func defaultAIProvider(cfg llm.Config) (llm.Provider, error) { return llm.New(cfg) }

// aiModels lists the provider's models once with key, within
// aiModelsTimeout; the error is the contract's (llm.ToAPIError).
func (b *Backend) aiModels(ctx context.Context, cfg api.AIConfig, key string) ([]api.AIModel, error) {
	p, err := b.aiProvider(cfg, key)
	if err != nil {
		return nil, llm.ToAPIError(err)
	}
	ctx, cancel := context.WithTimeout(ctx, aiModelsTimeout)
	defer cancel()
	models, err := p.Models(ctx)
	if err != nil {
		return nil, llm.ToAPIError(err)
	}
	return models, nil
}

// aiNeedsKey: the named providers always take a key; a compatible server
// may have none.
func aiNeedsKey(cfg api.AIConfig) bool { return cfg.Provider != api.AIOpenAICompatible }

// prepareAI completes an ai configuration before account.add stores it:
// validation, the consent's stamp, and one listing of the provider's
// models with the key, so that a refused key stores nothing.
func (b *Backend) prepareAI(ctx context.Context, cfg *api.AccountConfig, key string) error {
	if err := validateAI(cfg); err != nil {
		return err
	}
	if key == "" && aiNeedsKey(*cfg.AI) {
		return api.NewError(api.CodeInvalidArgument, "credentials.password (the API key) is required for an %s account", cfg.AI.Provider)
	}
	settleAIConsent(cfg.AI, nil, time.Now())
	_, err := b.aiModels(ctx, *cfg.AI, key)
	return err
}

// aiKeyFor is the key account.test or assistant.providerModels uses: the
// one given, else with accountID the stored one (only for an ai account
// of the same provider and host), where a compatible server may have
// none.
func (b *Backend) aiKeyFor(ctx context.Context, cfg api.AccountConfig, accountID api.AccountID, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	if accountID == "" {
		if aiNeedsKey(*cfg.AI) {
			return "", api.NewError(api.CodeAuthRequired, "no key given and no accountId")
		}
		return "", nil
	}
	a, err := b.requireAccount(ctx, string(accountID))
	if err != nil {
		return "", err
	}
	if a.Config.Protocol() != api.AccountAI || !sameAIEndpoint(a.Config, cfg) {
		return "", api.NewError(api.CodeInvalidArgument, "the stored key belongs to another provider or host")
	}
	key, err := b.PasswordFor(ctx, a.ID)
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.CodeAuthRequired && !aiNeedsKey(*cfg.AI) {
			return "", nil
		}
		return "", err
	}
	return key, nil
}

// testAI is account.test for an ai account: one listing of the models
// with the key given or stored.
func (s *accountService) testAI(ctx context.Context, p api.AccountTestParams) (*api.AccountTestResult, error) {
	cfg := *p.Config.AI
	key, err := s.b.aiKeyFor(ctx, p.Config, p.AccountID, p.Credentials.Password)
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.CodeAuthRequired && p.AccountID == "" {
			return &api.AccountTestResult{AI: &api.AITestResult{EndpointTestResult: *endpointResult(nil, 0, ae)}}, nil
		}
		return nil, err
	}
	start := time.Now()
	models, err := s.b.aiModels(ctx, cfg, key)
	res := &api.AITestResult{EndpointTestResult: *endpointResult(nil, time.Since(start), err)}
	if err == nil {
		res.Models = models
	}
	if p.AccountID != "" && p.Credentials.Password == "" {
		// A test with the stored key says what the account's state is.
		if st, ok := aiOutcome(err); ok {
			s.b.assist.setStatus(p.AccountID, st)
		}
	}
	code := ""
	if res.Error != nil {
		code = res.Error.Code.String()
	}
	s.b.log.Info("account test", "kind", "ai", "provider", cfg.Provider, "ok", res.OK, "code", code, "models", len(res.Models))
	return &api.AccountTestResult{AI: res}, nil
}
