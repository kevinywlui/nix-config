package main

import (
	"net/http/httptest"
	"testing"
)

// csrfOK must FAIL CLOSED: a state-changing request that carries neither the
// CLI header nor a same-origin Origin/Referer is rejected. Locking this in so a
// future refactor can't silently invert the check into a bypass.
func TestCSRFFailsClosed(t *testing.T) {
	s := &server{}
	cases := []struct {
		name              string
		client, orig, ref string
		host              string
		want              bool
	}{
		{"cli header", "cli", "", "", "gtd.example", true},
		{"same-origin origin", "", "https://gtd.example", "", "gtd.example", true},
		{"same-origin referer", "", "", "https://gtd.example/next", "gtd.example", true},
		{"no headers at all", "", "", "", "gtd.example", false},
		{"cross-origin origin", "", "https://evil.example", "", "gtd.example", false},
		{"prefix-spoof host", "", "https://gtd.example.evil.net", "", "gtd.example", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/done", nil)
		r.Host = c.host
		if c.client != "" {
			r.Header.Set("X-GTD-Client", c.client)
		}
		if c.orig != "" {
			r.Header.Set("Origin", c.orig)
		}
		if c.ref != "" {
			r.Header.Set("Referer", c.ref)
		}
		if got := s.csrfOK(r); got != c.want {
			t.Errorf("%s: csrfOK = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSafeBack(t *testing.T) {
	cases := map[string]string{
		"/next":              "/next",
		"/next?context=home": "/next?context=home",
		"":                   "/next",
		"//evil.example":     "/next",
		"https://evil.com":   "/next",
		"/\\evil.example":    "/next", // browsers normalise /\ to scheme-relative
	}
	for in, want := range cases {
		if got := safeBack(in); got != want {
			t.Errorf("safeBack(%q) = %q, want %q", in, got, want)
		}
	}
}
