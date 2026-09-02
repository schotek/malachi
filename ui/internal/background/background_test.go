package background

import "testing"

func TestRequestPath(t *testing.T) {
	got := RequestPath(":1.42", "malachi_7")
	want := "/org/freedesktop/portal/desktop/request/1_42/malachi_7"
	if got != want {
		t.Errorf("RequestPath = %q, want %q", got, want)
	}
}
