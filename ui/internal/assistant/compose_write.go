// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// The compose window's assistant bar (target App only; macOS
// ComposeAssistantBar and ComposeAssistantPrompt): from an optional
// instruction, the user's own text, the subject and the prepared original
// under the editor, the user's assistant writes a reply, a forward's
// introduction or a message. The prompt is the daemon's (assistant.prompt,
// purpose write, api.WriteInput); the answer, cleaned (CleanRewrite), is
// previewed, and only Replace puts it into the editor. Everything but the
// instruction is mail data, which the system prompt says is never an
// instruction.
//
// The panel with the prepared original under the editor (ui/internal/
// composebody) shares the texts of WriteTexts.

import (
	"fmt"

	"github.com/schotek/malachi/backend/pkg/api"
)

// WriteMode is the kind of compose window the bar writes for, as the
// request names it to the model (macOS `String(describing: ComposeKind)`).
// Its values are api.WriteMode's.
type WriteMode string

// The modes.
const (
	WriteNew      WriteMode = WriteMode(api.WriteNew)
	WriteReply    WriteMode = WriteMode(api.WriteReply)
	WriteReplyAll WriteMode = WriteMode(api.WriteReplyAll)
	WriteForward  WriteMode = WriteMode(api.WriteForward)
	// WriteEdit is a draft opened from the Drafts folder.
	WriteEdit WriteMode = WriteMode(api.WriteEdit)
)

// API is the mode as the daemon's input names it.
func (m WriteMode) API() api.WriteMode { return api.WriteMode(m) }

// The providers as the bar names them (brands, not translated).
const (
	ClaudeBrand  = "Claude (Anthropic)"
	ChatGPTBrand = "ChatGPT (OpenAI)"
)

// WriteStrings are the fixed texts of the compose window's assistant bar
// and of the panel with the prepared original under the editor, for one
// kind of window (WriteTexts).
type WriteStrings struct {
	// Instruction is the placeholder of the bar's field.
	Instruction string
	// Write is the bar's button: Write a Reply, Write an Introduction (a
	// forward) or Write a Message (a new message, a draft).
	Write string
	// Quoted is the heading of the panel with the prepared original;
	// Include its checkbox (Include in Reply, Include in Forward).
	Quoted, Include string
}

// WriteTexts returns the fixed texts of the bar and of the quoted panel
// for a window of mode, translated.
func WriteTexts(tr Translator, mode WriteMode) WriteStrings {
	s := WriteStrings{
		// TRANSLATORS: Placeholder of the field of the assistant's bar at the bottom of the compose window.
		Instruction: tr.T("What should the message say? (optional)"),
		// TRANSLATORS: Heading of the panel under the compose window's editor that shows the message being replied to or forwarded.
		Quoted: tr.T("Quoted Message"),
		// TRANSLATORS: A checkbox in the compose window: whether the reply includes the quoted message.
		Include: tr.T("Include in Reply"),
	}
	switch mode {
	case WriteForward:
		// TRANSLATORS: A button of the assistant's bar in the compose window of a forward.
		s.Write = tr.T("Write an Introduction")
		// TRANSLATORS: A checkbox in the compose window: whether the forward includes the forwarded message.
		s.Include = tr.T("Include in Forward")
	case WriteNew, WriteEdit:
		// TRANSLATORS: A button of the assistant's bar in the compose window of a new message.
		s.Write = tr.T("Write a Message")
	default:
		// TRANSLATORS: A button of the assistant's bar in the compose window of a reply.
		s.Write = tr.T("Write a Reply")
	}
	return s
}

// WriteBarTitle is the bar's heading: "Assistant · " and the provider in
// effect (ClaudeBrand, or ChatGPTBrand with chatGPT).
func WriteBarTitle(tr Translator, chatGPT bool) string {
	brand := ClaudeBrand
	if chatGPT {
		brand = ChatGPTBrand
	}
	return WriteBarTitleFor(tr, brand)
}

// WriteBarTitleFor is the bar's heading with the provider named: a brand
// (ClaudeBrand, ChatGPTBrand) or the name of an AI provider account.
func WriteBarTitleFor(tr Translator, brand string) string {
	// TRANSLATORS: Heading of the assistant's bar in the compose window; %s is the provider, e.g. "Claude (Anthropic)".
	return fmt.Sprintf(tr.T("Assistant · %s"), brand)
}
