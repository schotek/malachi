// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

import (
	"testing"
)

func TestWriteTexts(t *testing.T) {
	reply := WriteTexts(identity{}, WriteReplyAll)
	if reply != (WriteStrings{
		Instruction: "What should the message say? (optional)", Write: "Write a Reply",
		Quoted: "Quoted Message", Include: "Include in Reply",
	}) {
		t.Errorf("reply %+v", reply)
	}
	if f := WriteTexts(identity{}, WriteForward); f.Write != "Write an Introduction" || f.Include != "Include in Forward" {
		t.Errorf("forward %+v", f)
	}
	for _, m := range []WriteMode{WriteNew, WriteEdit} {
		if s := WriteTexts(identity{}, m); s.Write != "Write a Message" {
			t.Errorf("%s %+v", m, s)
		}
	}
	cs := catalog{"Assistant · %s": "Asistent · %s", "Write a Reply": "Napsat odpověď"}
	if got := WriteTexts(cs, WriteReply).Write; got != "Napsat odpověď" {
		t.Errorf("translated %q", got)
	}
	if got := WriteBarTitle(cs, false); got != "Asistent · Claude (Anthropic)" {
		t.Errorf("title %q", got)
	}
	if got := WriteBarTitle(identity{}, true); got != "Assistant · ChatGPT (OpenAI)" {
		t.Errorf("title %q", got)
	}
	if got := WriteBarTitleFor(cs, "Ollama doma"); got != "Asistent · Ollama doma" {
		t.Errorf("named title %q", got)
	}
}
