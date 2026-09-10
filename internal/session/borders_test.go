package session

import "testing"

func TestBorderState(t *testing.T) {
	for _, tc := range []struct{ phase, want string }{{"done", "reply"}, {"input", "reply"}, {"permission", "approval"}, {"thinking", ""}, {"tool", ""}, {"unknown", ""}, {"", ""}} {
		if got := borderState(tc.phase); got != tc.want {
			t.Fatalf("%s: %s", tc.phase, got)
		}
	}
}
