package window

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestNotificationText(t *testing.T) {
	n := api.NewMessageNotification{Message: api.MessageSummary{
		From:    []api.Address{{Name: "Alice Example", Address: "alice@example.invalid"}},
		Subject: "  Lunch?  ",
	}}
	title, body := notificationText(n)
	if title != "Alice Example" || body != "Lunch?" {
		t.Errorf("got %q / %q", title, body)
	}

	n.Message.From = nil
	n.Message.Subject = ""
	title, body = notificationText(n)
	if title != "New message" || body != "(No subject)" {
		t.Errorf("fallbacks: got %q / %q", title, body)
	}

	n.Message.Subject = strings.Repeat("ž", 500)
	_, body = notificationText(n)
	if len([]rune(body)) > notificationBodyMax+1 || !strings.HasSuffix(body, "…") || !strings.ContainsRune(body, 'ž') {
		t.Errorf("cap failed: len=%d", len(body))
	}
	if strings.Contains(body, "�") {
		t.Error("cap left an invalid UTF-8 sequence")
	}
}
