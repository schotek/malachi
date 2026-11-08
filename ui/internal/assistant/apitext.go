// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// The failures of a session the daemon runs over an ai account
// (docs/api.md §4.18, api.AssistantFailureClass; ui/internal/apiassistant
// turns them into the reasons "api_<class>"), in the user's words. The
// provider names and the consent texts of the ai accounts are api.go's.

// APIFailureStrings are the sentences of a session's failures.
type APIFailureStrings struct {
	// ConsentRequired: the account's consent was not given.
	ConsentRequired string
	// Auth: the key was refused; Permission: the key may not use this.
	Auth, Permission string
	// RateLimit: too many requests; Overloaded: the provider is busy.
	RateLimit, Overloaded string
	// Quota: the account at the provider has no credit or used up its
	// quota (the key tests fine: listing the models costs nothing).
	Quota string
	// Model: the server has no such model or could not load it (a local
	// server whose model does not fit in memory).
	Model string
	// InvalidRequest: the provider refused the request.
	InvalidRequest string
	// ToolsUnsupported: the model cannot call tools; Bridge: the Malachi
	// Mail tools could not be started.
	ToolsUnsupported, Bridge string
	// Network: no connection; Protocol: the answer could not be read.
	Network, Protocol string
	// ContextFull: the conversation grew too long.
	ContextFull string
	// Timeout: no answer in time; Limit: too many tool calls in one turn.
	Timeout, Limit string
	// Stopped: the session ended (stopped, the connection closed).
	Stopped string
}

// APIFailureTexts returns the sentences of a session's failures,
// translated: a sentence for each api.AssistantFailureClass, shown where a
// provider's failure is shown (the panel's error line, the compose
// window's popover, the board), never the daemon's technical message.
func APIFailureTexts(tr Translator) APIFailureStrings {
	return APIFailureStrings{
		// TRANSLATORS: An error line of the assistant: the user has not allowed sending mail to the AI account's provider yet.
		ConsentRequired: tr.T("Allow sending mail to this provider under AI in the preferences first."),
		// TRANSLATORS: An error line of the assistant: the provider refused the API key.
		Auth: tr.T("The provider refused the API key. Check it under AI in the preferences."),
		// TRANSLATORS: An error line of the assistant: the API key may not use this model.
		Permission: tr.T("The API key may not use this model."),
		// TRANSLATORS: An error line of the assistant: the provider's rate limit was reached.
		RateLimit: tr.T("The provider is receiving too many requests. Try again in a moment."),
		// TRANSLATORS: An error line of the assistant: the provider is overloaded.
		Overloaded: tr.T("The provider is busy right now. Try again in a moment."),
		// TRANSLATORS: An error line of the assistant: the provider rejected the request.
		InvalidRequest: tr.T("The provider refused the request."),
		// TRANSLATORS: An error line of the assistant: the account at the AI provider has no credit (prepaid) or used up its quota; the user tops it up on the provider's website, not in Malachi Mail.
		Quota: tr.T("The account at the provider has no credit or has used up its quota. Top it up with the provider, then try again."),
		// TRANSLATORS: An error line of the assistant: the AI server has no such model or could not load it (a local server, where the model did not fit in memory); the user picks another model under Integrations in the preferences or loads it on the server.
		Model: tr.T("The server could not load the model. Load it on the server, or choose another model under Integrations in the preferences."),
		// TRANSLATORS: An error line of the assistant: the chosen model cannot call tools, which the assistant needs to read mail.
		ToolsUnsupported: tr.T("This model cannot use the Malachi Mail tools. Choose another model under AI in the preferences."),
		// TRANSLATORS: An error line of the assistant: the MCP bridge (the Malachi Mail tools) could not be started.
		Bridge: tr.T("The Malachi Mail tools could not be started."),
		// TRANSLATORS: An error line of the assistant: the provider could not be reached.
		Network: tr.T("The provider could not be reached."),
		// TRANSLATORS: An error line of the assistant: the provider's answer could not be read.
		Protocol: tr.T("The provider's answer could not be read."),
		// TRANSLATORS: An error line of the assistant: the conversation grew too long for the model.
		ContextFull: tr.T("The conversation is too long for the model. Start a new one."),
		// TRANSLATORS: An error line of the assistant: the model did not answer in time.
		Timeout: tr.T("The model did not answer in time."),
		// TRANSLATORS: An error line of the assistant: the model called too many tools in one turn.
		Limit: tr.T("The model made too many tool calls in one turn."),
		// TRANSLATORS: An error line of the assistant: the session ended.
		Stopped: tr.T("The assistant's session ended."),
	}
}

// APIFailurePrefix starts every reason ui/internal/apiassistant makes of
// a session's failure class: "api_" and the class ("api_auth").
const APIFailurePrefix = "api_"

// APIFailureText is the sentence of a reason "api_<class>" (false for any
// other reason, and for a class without a sentence).
func APIFailureText(tr Translator, reason string) (string, bool) {
	class, ok := cutPrefix(reason, APIFailurePrefix)
	if !ok {
		return "", false
	}
	t := APIFailureTexts(tr)
	switch class {
	case "consent_required":
		return t.ConsentRequired, true
	case "auth":
		return t.Auth, true
	case "permission":
		return t.Permission, true
	case "rateLimit":
		return t.RateLimit, true
	case "overloaded":
		return t.Overloaded, true
	case "invalidRequest":
		return t.InvalidRequest, true
	case "quota":
		return t.Quota, true
	case "model":
		return t.Model, true
	case "toolsUnsupported":
		return t.ToolsUnsupported, true
	case "bridge":
		return t.Bridge, true
	case "network":
		return t.Network, true
	case "protocol":
		return t.Protocol, true
	case "contextFull":
		return t.ContextFull, true
	case "timeout":
		return t.Timeout, true
	case "limit":
		return t.Limit, true
	case "stopped":
		return t.Stopped, true
	}
	return "", false
}

// cutPrefix is strings.CutPrefix without the import.
func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}
