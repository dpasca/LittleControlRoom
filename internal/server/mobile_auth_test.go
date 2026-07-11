package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMobileAuthPairingProtectsRoutesAndPersistsSession(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	now := time.Date(2026, 7, 11, 14, 0, 0, 0, time.UTC)
	currentTime := now
	auth, err := newMobileAuth(dataDir, mobileAuthOptions{
		Now:        func() time.Time { return currentTime },
		Random:     bytes.NewReader(bytes.Repeat([]byte{1}, 128)),
		SessionTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("new mobile auth: %v", err)
	}

	keyInfo, err := os.Stat(filepath.Join(dataDir, mobileAuthKeyFileName))
	if err != nil {
		t.Fatalf("stat mobile auth key: %v", err)
	}
	if got, want := keyInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("mobile auth key permissions = %o, want %o", got, want)
	}

	handler := New(nil).WithMobileAuth(auth).Handler(context.Background())
	staticResponse := httptest.NewRecorder()
	staticRequest := httptest.NewRequest(http.MethodGet, "http://lcr.test/", nil)
	staticRequest.RemoteAddr = "192.0.2.10:1234"
	handler.ServeHTTP(staticResponse, staticRequest)
	if staticResponse.Code != http.StatusOK {
		t.Fatalf("public mobile shell status = %d, want 200", staticResponse.Code)
	}

	protectedResponse := httptest.NewRecorder()
	protectedRequest := httptest.NewRequest(http.MethodGet, "http://lcr.test/api/mobile/dashboard", nil)
	protectedRequest.RemoteAddr = "192.0.2.10:1234"
	handler.ServeHTTP(protectedResponse, protectedRequest)
	if protectedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unpaired dashboard status = %d, want 401", protectedResponse.Code)
	}

	statusResponse := httptest.NewRecorder()
	statusRequest := httptest.NewRequest(http.MethodGet, "http://lcr.test/api/mobile/auth/status", nil)
	statusRequest.RemoteAddr = "192.0.2.10:1234"
	handler.ServeHTTP(statusResponse, statusRequest)
	var initialStatus MobileAuthStatus
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &initialStatus); err != nil {
		t.Fatalf("decode initial auth status: %v", err)
	}
	if !initialStatus.Required || initialStatus.Authenticated {
		t.Fatalf("initial auth status = %#v, want required and unauthenticated", initialStatus)
	}

	pairBody := strings.NewReader(`{"code":` + strconvQuote(auth.PairingCode()) + `}`)
	pairResponse := httptest.NewRecorder()
	pairRequest := httptest.NewRequest(http.MethodPost, "http://lcr.test/api/mobile/auth/pair", pairBody)
	pairRequest.RemoteAddr = "192.0.2.10:1234"
	pairRequest.Header.Set("Origin", "http://lcr.test")
	handler.ServeHTTP(pairResponse, pairRequest)
	if pairResponse.Code != http.StatusOK {
		t.Fatalf("pair status = %d, body = %s", pairResponse.Code, pairResponse.Body.String())
	}
	cookies := pairResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("pair cookies = %d, want 1", len(cookies))
	}
	sessionCookie := cookies[0]
	if sessionCookie.Name != mobileAuthCookieName || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("pair cookie = %#v", sessionCookie)
	}
	if sessionCookie.Secure {
		t.Fatal("plain HTTP pairing cookie should not set Secure")
	}

	pairedStatusResponse := httptest.NewRecorder()
	pairedStatusRequest := httptest.NewRequest(http.MethodGet, "http://lcr.test/api/mobile/auth/status", nil)
	pairedStatusRequest.RemoteAddr = "192.0.2.10:1234"
	pairedStatusRequest.AddCookie(sessionCookie)
	handler.ServeHTTP(pairedStatusResponse, pairedStatusRequest)
	var pairedStatus MobileAuthStatus
	if err := json.Unmarshal(pairedStatusResponse.Body.Bytes(), &pairedStatus); err != nil {
		t.Fatalf("decode paired auth status: %v", err)
	}
	if !pairedStatus.Authenticated || pairedStatus.SessionExpiresAt == nil {
		t.Fatalf("paired auth status = %#v", pairedStatus)
	}

	restartedAuth, err := newMobileAuth(dataDir, mobileAuthOptions{
		Now:        func() time.Time { return currentTime },
		Random:     bytes.NewReader(bytes.Repeat([]byte{2}, 64)),
		SessionTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("restart mobile auth: %v", err)
	}
	protected := restartedAuth.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	restartedResponse := httptest.NewRecorder()
	restartedRequest := httptest.NewRequest(http.MethodGet, "http://lcr.test/protected", nil)
	restartedRequest.RemoteAddr = "192.0.2.10:1234"
	restartedRequest.AddCookie(sessionCookie)
	protected.ServeHTTP(restartedResponse, restartedRequest)
	if restartedResponse.Code != http.StatusNoContent {
		t.Fatalf("persisted session status = %d, want 204", restartedResponse.Code)
	}

	currentTime = now.Add(25 * time.Hour)
	expiredResponse := httptest.NewRecorder()
	expiredRequest := httptest.NewRequest(http.MethodGet, "http://lcr.test/protected", nil)
	expiredRequest.RemoteAddr = "192.0.2.10:1234"
	expiredRequest.AddCookie(sessionCookie)
	protected.ServeHTTP(expiredResponse, expiredRequest)
	if expiredResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status = %d, want 401", expiredResponse.Code)
	}
}

func TestMobileAuthRateLimitsFailedPairingByPeer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 14, 0, 0, 0, time.UTC)
	auth, err := newMobileAuth(t.TempDir(), mobileAuthOptions{
		Now:           func() time.Time { return now },
		Random:        bytes.NewReader(bytes.Repeat([]byte{3}, 128)),
		AttemptWindow: time.Minute,
		MaxAttempts:   2,
	})
	if err != nil {
		t.Fatalf("new mobile auth: %v", err)
	}
	handler := New(nil).WithMobileAuth(auth).Handler(context.Background())

	pair := func(remoteAddr string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "http://lcr.test/api/mobile/auth/pair", strings.NewReader(`{"code":"000000"}`))
		request.RemoteAddr = remoteAddr
		request.Header.Set("Origin", "http://lcr.test")
		handler.ServeHTTP(response, request)
		return response
	}

	if got := pair("192.0.2.20:1000").Code; got != http.StatusUnauthorized {
		t.Fatalf("first failed pair status = %d, want 401", got)
	}
	limited := pair("192.0.2.20:2000")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("limited pair response = %d, retry-after %q", limited.Code, limited.Header().Get("Retry-After"))
	}
	if got := pair("192.0.2.20:3000").Code; got != http.StatusTooManyRequests {
		t.Fatalf("continued limited pair status = %d, want 429", got)
	}
	if got := pair("192.0.2.21:1000").Code; got != http.StatusUnauthorized {
		t.Fatalf("different peer pair status = %d, want 401", got)
	}

	now = now.Add(time.Minute + time.Second)
	if got := pair("192.0.2.20:4000").Code; got != http.StatusUnauthorized {
		t.Fatalf("pair status after window = %d, want 401", got)
	}
}

func TestMobileAuthRejectsCrossOriginPairingAndAllowsLoopback(t *testing.T) {
	t.Parallel()
	auth, err := newMobileAuth(t.TempDir(), mobileAuthOptions{
		Random: bytes.NewReader(bytes.Repeat([]byte{4}, 128)),
	})
	if err != nil {
		t.Fatalf("new mobile auth: %v", err)
	}
	handler := New(nil).WithMobileAuth(auth).Handler(context.Background())

	crossOrigin := httptest.NewRecorder()
	crossOriginRequest := httptest.NewRequest(http.MethodPost, "http://lcr.test/api/mobile/auth/pair", strings.NewReader(`{"code":"`+auth.PairingCode()+`"}`))
	crossOriginRequest.RemoteAddr = "192.0.2.30:1234"
	crossOriginRequest.Header.Set("Origin", "http://other.test")
	handler.ServeHTTP(crossOrigin, crossOriginRequest)
	if crossOrigin.Code != http.StatusForbidden {
		t.Fatalf("cross-origin pair status = %d, want 403", crossOrigin.Code)
	}

	loopback := auth.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	loopbackResponse := httptest.NewRecorder()
	loopbackRequest := httptest.NewRequest(http.MethodGet, "http://localhost/protected", nil)
	loopbackRequest.RemoteAddr = "127.0.0.1:4321"
	loopback.ServeHTTP(loopbackResponse, loopbackRequest)
	if loopbackResponse.Code != http.StatusNoContent {
		t.Fatalf("loopback protected status = %d, want 204", loopbackResponse.Code)
	}
}

func TestMobileAuthRejectsMalformedStoredKey(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, mobileAuthKeyFileName), []byte("short"), 0o600); err != nil {
		t.Fatalf("write malformed key: %v", err)
	}
	if _, err := NewMobileAuth(dataDir); err == nil {
		t.Fatal("expected malformed mobile auth key to fail closed")
	}
}

func strconvQuote(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
