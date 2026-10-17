package app

import (
	"context"
	"io"
	"testing"
)

func TestBuildAdBlockRequestPiholeAndAdGuard(t *testing.T) {
	req, err := buildAdBlockRequest(context.Background(), "pihole", "http://pi.hole", "tok en", true)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "GET" || req.URL.Query().Get("auth") != "tok en" || req.URL.Query().Get("disable") != "300" {
		t.Fatalf("unexpected pihole request: %s %s", req.Method, req.URL)
	}

	req, err = buildAdBlockRequest(context.Background(), "adguard", "http://ag:3000", "admin:secret", true)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(req.Body)
	user, pass, ok := req.BasicAuth()
	if req.Method != "POST" || req.URL.Path != "/control/protection" || !ok || user != "admin" || pass != "secret" {
		t.Fatalf("unexpected adguard request: %s %s auth=%v", req.Method, req.URL, ok)
	}
	if string(body) != `{"duration":300000,"enabled":false}` {
		t.Fatalf("adguard body = %s", body)
	}

	if _, err := buildAdBlockRequest(context.Background(), "pihole", "file:///etc", "t", true); err == nil {
		t.Fatal("non-http URL must be refused")
	}
}

func TestAdBlockResponseOK(t *testing.T) {
	if !adBlockResponseOK("pihole", []byte(`{"status":"disabled"}`), true) {
		t.Error("pause answer rejected")
	}
	if adBlockResponseOK("pihole", []byte(`[]`), true) {
		t.Error("empty list (bad token) accepted")
	}
	if adBlockResponseOK("pihole", []byte(`{"status":"enabled"}`), true) {
		t.Error("wrong status accepted")
	}
	if !adBlockResponseOK("adguard", nil, false) {
		t.Error("adguard empty body rejected")
	}
}
