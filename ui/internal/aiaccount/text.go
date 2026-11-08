// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package aiaccount

import (
	"fmt"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
)

// Translator translates the texts; assistant.Translator (T and N). GTK
// passes i18n.Tr, the tests one that returns the msgid.
type Translator = assistant.Translator

// Strings are the dialog's texts. Every one is plain text (nothing is
// markup); the buttons carry mnemonics.
type Strings struct {
	// AddTitle titles the dialog that adds an account, EditTitle the one
	// that edits it; ProviderLabel the read-only row naming the kind.
	AddTitle, EditTitle, ProviderLabel string
	NameLabel                          string
	// ServerLabel is the address row of an OpenAI-compatible server;
	// ServerHint the line under it (AddressHint).
	ServerLabel, ServerHint string
	// The key row: KeyLabel when adding, KeyOptionalLabel for a server
	// that may have none, NewKeyLabel while a stored key is being
	// replaced; KeyStored and ReplaceKey are the row of a stored key,
	// KeepKey takes the replacement back.
	KeyLabel, KeyOptionalLabel, NewKeyLabel, KeyStored, ReplaceKey, KeepKey string
	// The model rows: a text with the provider's models to pick from.
	ModelLabel, ModelHint, BoardModelLabel, BoardModelHint string
	// The certificate pin of an https server (docs/plans/ai-api.md D8).
	PinLabel, PinForget string
	// Test is the button that lists the provider's models; Testing its
	// progress; Connected the status with the number of models (format
	// of N with %d), ConnectedNoModels without.
	Test, Testing, ConnectedNoModels string
	// Add, Save and their progress texts.
	Add, Save, Adding, Saving string
	// Problems of the fields.
	NameMissing, ServerMissing, ServerInvalid, PinNeedsHTTPS string
	KeyMissing, ModelMissing, ModelInvalid, ConsentMissing   string
	// Failures of the calls (FailureOf).
	KeyRefused, Unreachable, Timeout, Insecure, ServerError string

	tr Translator
}

// Texts translates the dialog's texts.
func Texts(tr Translator) Strings {
	x := assistant.APIText(tr)
	return Strings{
		// TRANSLATORS: title of the dialog that adds an AI provider account (Anthropic API, OpenAI API, an OpenAI-compatible server).
		AddTitle:  tr.T("Add AI Provider"),
		EditTitle: tr.T("Edit Account"),
		// TRANSLATORS: the read-only row of the AI provider dialog naming the kind of provider.
		ProviderLabel: tr.T("Provider"),
		NameLabel:     tr.T("Account Name"),
		// TRANSLATORS: the base URL of an OpenAI-compatible server ("…/v1").
		ServerLabel: tr.T("Server Address"),
		ServerHint:  AddressHint(tr),
		KeyLabel:    tr.T("API Key"),
		// TRANSLATORS: the key row of an OpenAI-compatible server, which may need none.
		KeyOptionalLabel: tr.T("API Key (optional)"),
		NewKeyLabel:      tr.T("New API Key"),
		// TRANSLATORS: the key row of an existing AI provider account: the key is in the keyring and never shown.
		KeyStored: tr.T("Key stored"),
		// TRANSLATORS: button of the key row of an existing AI provider account.
		ReplaceKey: tr.T("Replace Key…"),
		// TRANSLATORS: button that takes a key replacement back and keeps the stored key.
		KeepKey:    tr.T("Keep the Stored Key"),
		ModelLabel: x.ModelRow,
		// TRANSLATORS: under the model row of the AI provider dialog; the list comes from Test.
		ModelHint: tr.T("Choose one of the provider's models or type its id"),
		// TRANSLATORS: the model of the board's triage, an optional second model of the AI provider account.
		BoardModelLabel: tr.T("Board Model"),
		BoardModelHint:  x.BoardModelSubtitle,
		// TRANSLATORS: row of the AI provider dialog showing the pinned certificate of an https server.
		PinLabel: tr.T("Trusted Certificate"),
		// TRANSLATORS: button that drops the pinned certificate.
		PinForget: tr.T("Forget"),
		// TRANSLATORS: button of the AI provider dialog: asks the provider for its models with the key.
		Test: tr.T("_Test"),
		// TRANSLATORS: progressive form for the RPC error text
		Testing: tr.T("Testing the connection"),
		// TRANSLATORS: status of the AI provider dialog after a test that listed no models.
		ConnectedNoModels: tr.T("Connected"),
		Add:               tr.T("_Add Account"),
		Save:              tr.T("_Save"),
		Adding:            tr.T("Adding the account"),
		Saving:            tr.T("Saving the account"),
		NameMissing:       tr.T("Enter a name for the account"),
		// TRANSLATORS: the address example stays as it is.
		ServerMissing: tr.T("Enter the server's address, such as http://localhost:11434/v1"),
		ServerInvalid: tr.T("This is not a web address"),
		PinNeedsHTTPS: tr.T("A certificate can be pinned only for an https address"),
		KeyMissing:    tr.T("Enter the API key"),
		ModelMissing:  tr.T("Enter or choose a model"),
		ModelInvalid:  tr.T("This is not a model id"),
		// TRANSLATORS: Save of the AI provider dialog with the consent check box off.
		ConsentMissing: tr.T("Allow sending mail to the provider first"),
		KeyRefused:     x.AccountKeyRefused,
		Unreachable:    tr.T("The provider could not be reached"),
		Timeout:        tr.T("The provider did not answer in time"),
		Insecure:       tr.T("The connection to the provider is not secure"),
		ServerError:    tr.T("The provider answered with an error"),
		tr:             tr,
	}
}

// Connected is the status after a test that listed n models.
func (s Strings) Connected(n int) string {
	if n <= 0 {
		return s.ConnectedNoModels
	}
	// TRANSLATORS: status of the AI provider dialog after a test; %d is the number of models the provider listed.
	return fmt.Sprintf(s.tr.N("Connected · %d model", "Connected · %d models", n), n)
}

// HTTPWarning is the warning under an http address outside a loopback
// address (docs/plans/ai-api.md D8); host is the address's host.
func HTTPWarning(tr Translator, host string) string {
	// TRANSLATORS: %s is the host of an OpenAI-compatible server reached over plain http.
	return fmt.Sprintf(tr.T("The API key and your mail travel unencrypted over http to %s."), host)
}

// AddressExample is one example of an OpenAI-compatible server's
// address: a local server or a gateway (docs/plans/ai-api.md Q18).
type AddressExample struct {
	Name, URL string
}

// AddressExamples are the examples the hint under the address field
// names; nothing is prefilled.
var AddressExamples = []AddressExample{
	{"Ollama", "http://localhost:11434/v1"},
	{"llama.cpp", "http://127.0.0.1:9931/v1"},
	{"LM Studio", "http://localhost:1234/v1"},
	{"vLLM", "http://localhost:8000/v1"},
	{"OpenRouter", "https://openrouter.ai/api/v1"},
	{"Groq", "https://api.groq.com/openai/v1"},
	{"Mistral", "https://api.mistral.ai/v1"},
	{"Together", "https://api.together.ai/v1"},
}

// AddressHint is the line under the address field: the examples, one
// per line after the sentence.
func AddressHint(tr Translator) string {
	var b strings.Builder
	// TRANSLATORS: under the address field of an OpenAI-compatible server; the examples follow, one per line.
	b.WriteString(tr.T("The server's base URL, usually ending in /v1. For example:"))
	for _, e := range AddressExamples {
		b.WriteString("\n")
		b.WriteString(e.Name)
		b.WriteString("  ")
		b.WriteString(e.URL)
	}
	return b.String()
}

// ConsentStrings are the consent the dialog asks before the account is
// saved (docs/plans/ai-api.md D7, Q17): the heading, the body (one
// common paragraph on what leaves, then one on where it goes, by the
// kind of provider) and the check box.
type ConsentStrings struct {
	Heading  string
	Common   string
	Specific string
	Checkbox string
}

// Body is the two paragraphs.
func (c ConsentStrings) Body() string { return c.Common + "\n\n" + c.Specific }

// ConsentTexts translates the consent of a provider kind.
func ConsentTexts(tr Translator, p api.AIProvider) ConsentStrings {
	c := ConsentStrings{
		Common: tr.T("The assistant sends the prompt, the text you write, the mail it reads for you and that mail's attachments to the provider. It can read mail and prepare drafts; it cannot send, delete or move messages."),
		// TRANSLATORS: check box of the AI provider dialog; Save needs it on.
		Checkbox: tr.T("Allow Malachi Mail to send mail to this provider"),
	}
	switch p {
	case api.AIAnthropic:
		c.Heading = tr.T("Send Mail to Anthropic?")
		c.Specific = tr.T("They go to Anthropic under its API terms, which do not use data sent through the API for training by default.")
	case api.AIOpenAI:
		c.Heading = tr.T("Send Mail to OpenAI?")
		c.Specific = tr.T("They go to OpenAI under its API terms, which do not use data sent through the API for training by default.")
	default:
		// TRANSLATORS: consent heading of an OpenAI-compatible server the user set the address of.
		c.Heading = tr.T("Send Mail to Your Server?")
		c.Specific = tr.T("They go to the server at the address you set. Over http they travel unencrypted.")
	}
	return c
}
