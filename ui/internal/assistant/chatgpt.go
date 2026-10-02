// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// ChatGPTLabels is the shared gettext reference for the experimental provider.
// Connected is a format with one %s placeholder for the verified account email.
type ChatGPTLabels struct {
	Footer                string
	Provider              string
	Name                  string
	Description           string
	Codex                 string
	Install               string
	SignIn                string
	Disconnect            string
	Usage                 string
	Disconnected          string
	Connecting            string
	Connected             string
	Reconnect             string
	ConnectionFailed      string
	RevocationUnconfirmed string
	MissingCodex          string
	NativeMissingCodex    string
	FlatpakUnavailable    string
	DefaultModel          string
	ModelsUnavailable     string
	ConsentHeading        string
	ConsentBody           string
	BoardConsentHeading   string
	BoardConsentBody      string
}

// ChatGPTText translates provider presentation without any GTK or OS dependency.
func ChatGPTText(tr Translator) ChatGPTLabels {
	return ChatGPTLabels{
		Footer:                tr.T("Mail you ask about is sent to OpenAI using your ChatGPT plan"),
		Provider:              tr.T("In-app provider"),
		Name:                  tr.T("ChatGPT (Codex, experimental)"),
		Description:           tr.T("Connect ChatGPT to use your plan with the in-app assistant."),
		Codex:                 tr.T("Codex"),
		Install:               tr.T("Get Codex…"),
		SignIn:                tr.T("Continue with ChatGPT"),
		Disconnect:            tr.T("Disconnect"),
		Usage:                 tr.T("Manage usage"),
		Disconnected:          tr.T("Not connected"),
		Connecting:            tr.T("Connecting…"),
		Connected:             tr.T("Connected as %s"),
		Reconnect:             tr.T("Reconnect to ChatGPT"),
		ConnectionFailed:      tr.T("Could not connect to ChatGPT."),
		RevocationUnconfirmed: tr.T("Disconnected locally; remote sign-out could not be confirmed."),
		MissingCodex:          tr.T("Codex was not found. Choose a native codex.exe."),
		NativeMissingCodex:    tr.T("Codex was not found. Choose a native Codex executable."),
		FlatpakUnavailable:    tr.T("Codex is unavailable in this Flatpak build."),
		DefaultModel:          tr.T("Use the provider’s default model"),
		ModelsUnavailable:     tr.T("Model catalog unavailable. You can keep the provider’s default model."),
		ConsentHeading:        tr.T("Send Mail to OpenAI?"),
		ConsentBody:           tr.T("Malachi Mail will send the selected mail and text you provide to OpenAI through Codex, using your ChatGPT plan. The assistant can read mail and prepare drafts. It cannot send, delete or move messages. This experimental integration does not import your ChatGPT conversations or memory."),
		BoardConsentHeading:   tr.T("Let OpenAI Refine the Board?"),
		BoardConsentBody:      tr.T("Malachi Mail will send board mail to OpenAI through Codex, using your ChatGPT plan. It can read mail and annotate cases. Automatic triage sends newly received mail while enabled. It cannot send, delete or move messages."),
	}
}
