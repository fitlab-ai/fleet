package subscription

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDownloaderEnforcesRequestAndResponseSafety(t *testing.T) {
	var userAgent string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte("proxies: []\n"))
	}))
	defer server.Close()
	client := server.Client()
	data, err := (Downloader{Client: client}).Download(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if userAgent != UserAgent || string(data) != "proxies: []\n" {
		t.Fatalf("request contract mismatch: agent=%q data=%q", userAgent, data)
	}

	large := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", MaxBytes+1)))
	}))
	defer large.Close()
	if _, err := (Downloader{Client: large.Client()}).Download(large.URL); err == nil || strings.Contains(err.Error(), large.URL) {
		t.Fatalf("oversized response was accepted or leaked URL: %v", err)
	}
}
