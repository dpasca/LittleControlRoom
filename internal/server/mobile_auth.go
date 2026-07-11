package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	mobileAuthCookieName      = "lcroom_mobile_session"
	mobileAuthKeyFileName     = "mobile-auth.key"
	mobileAuthKeyBytes        = 32
	mobileAuthNonceBytes      = 16
	mobilePairingCodeDigits   = 6
	mobilePairingMaxBodyBytes = 4096
	mobilePairingMaxAttempts  = 5
	mobilePairingWindow       = time.Minute
	mobileSessionTTL          = 30 * 24 * time.Hour
)

type MobileAuth struct {
	signingKey    []byte
	pairingCode   string
	sessionTTL    time.Duration
	attemptWindow time.Duration
	maxAttempts   int
	now           func() time.Time
	random        io.Reader

	attemptMu sync.Mutex
	attempts  map[string]mobilePairAttempt
}

type MobileAuthStatus struct {
	Required         bool       `json:"required"`
	Authenticated    bool       `json:"authenticated"`
	TransportSecure  bool       `json:"transport_secure"`
	SessionExpiresAt *time.Time `json:"session_expires_at,omitempty"`
}

type mobilePairRequest struct {
	Code string `json:"code"`
}

type mobilePairAttempt struct {
	WindowStartedAt time.Time
	Failures        int
}

type mobileAuthOptions struct {
	Now           func() time.Time
	Random        io.Reader
	SessionTTL    time.Duration
	AttemptWindow time.Duration
	MaxAttempts   int
}

func NewMobileAuth(dataDir string) (*MobileAuth, error) {
	return newMobileAuth(dataDir, mobileAuthOptions{})
}

func newMobileAuth(dataDir string, options mobileAuthOptions) (*MobileAuth, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, errors.New("mobile auth data directory is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.SessionTTL <= 0 {
		options.SessionTTL = mobileSessionTTL
	}
	if options.AttemptWindow <= 0 {
		options.AttemptWindow = mobilePairingWindow
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = mobilePairingMaxAttempts
	}

	signingKey, err := loadOrCreateMobileAuthKey(filepath.Join(dataDir, mobileAuthKeyFileName), options.Random)
	if err != nil {
		return nil, err
	}
	pairingCode, err := generateMobilePairingCode(options.Random)
	if err != nil {
		return nil, fmt.Errorf("generate mobile pairing code: %w", err)
	}

	return &MobileAuth{
		signingKey:    signingKey,
		pairingCode:   pairingCode,
		sessionTTL:    options.SessionTTL,
		attemptWindow: options.AttemptWindow,
		maxAttempts:   options.MaxAttempts,
		now:           options.Now,
		random:        options.Random,
		attempts:      make(map[string]mobilePairAttempt),
	}, nil
}

func (a *MobileAuth) PairingCode() string {
	if a == nil || len(a.pairingCode) != mobilePairingCodeDigits {
		return ""
	}
	return a.pairingCode[:3] + " " + a.pairingCode[3:]
}

func (a *MobileAuth) Protect(next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.authenticateRequest(r); !ok {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "mobile pairing required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *MobileAuth) status(r *http.Request) MobileAuthStatus {
	localBypass := a != nil && r != nil && mobileAuthRemoteIsLoopback(r.RemoteAddr)
	status := MobileAuthStatus{
		Required:        a != nil && !localBypass,
		Authenticated:   a == nil,
		TransportSecure: r != nil && r.TLS != nil,
	}
	if a == nil || r == nil {
		return status
	}
	expiresAt, ok := a.authenticateRequest(r)
	status.Authenticated = ok
	if ok && !expiresAt.IsZero() {
		status.SessionExpiresAt = &expiresAt
	}
	return status
}

func (a *MobileAuth) pair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a == nil {
		writeJSON(w, MobileAuthStatus{Authenticated: true, TransportSecure: r.TLS != nil})
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	peer := mobileAuthPeer(r.RemoteAddr)
	if allowed, retryAfter := a.pairingAttemptAllowed(peer); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Round(time.Second)/time.Second))))
		http.Error(w, "too many pairing attempts", http.StatusTooManyRequests)
		return
	}

	var request mobilePairRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, mobilePairingMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid pairing request", http.StatusBadRequest)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		http.Error(w, "invalid pairing request", http.StatusBadRequest)
		return
	}

	if !a.pairingCodeMatches(request.Code) {
		if retryAfter := a.recordPairingFailure(peer); retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Round(time.Second)/time.Second))))
			http.Error(w, "too many pairing attempts", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "pairing code not accepted", http.StatusUnauthorized)
		return
	}

	token, expiresAt, err := a.issueSessionToken()
	if err != nil {
		http.Error(w, "could not establish mobile session", http.StatusInternalServerError)
		return
	}
	a.clearPairingFailures(peer)
	http.SetCookie(w, &http.Cookie{
		Name:     mobileAuthCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(a.sessionTTL / time.Second),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, MobileAuthStatus{
		Required:         true,
		Authenticated:    true,
		TransportSecure:  r.TLS != nil,
		SessionExpiresAt: &expiresAt,
	})
}

func (a *MobileAuth) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     mobileAuthCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, MobileAuthStatus{
		Required:        a != nil,
		Authenticated:   a == nil,
		TransportSecure: r.TLS != nil,
	})
}

func (a *MobileAuth) authenticateRequest(r *http.Request) (time.Time, bool) {
	if a == nil {
		return time.Time{}, true
	}
	if r != nil && mobileAuthRemoteIsLoopback(r.RemoteAddr) {
		return time.Time{}, true
	}
	if r == nil {
		return time.Time{}, false
	}
	cookie, err := r.Cookie(mobileAuthCookieName)
	if err != nil {
		return time.Time{}, false
	}
	return a.validateSessionToken(cookie.Value)
}

func (a *MobileAuth) pairingCodeMatches(raw string) bool {
	normalized := normalizeMobilePairingCode(raw)
	want := sha256.Sum256([]byte(a.pairingCode))
	got := sha256.Sum256([]byte(normalized))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1 && len(normalized) == mobilePairingCodeDigits
}

func (a *MobileAuth) issueSessionToken() (string, time.Time, error) {
	nonce := make([]byte, mobileAuthNonceBytes)
	if _, err := io.ReadFull(a.random, nonce); err != nil {
		return "", time.Time{}, err
	}
	expiresAt := a.now().UTC().Add(a.sessionTTL)
	payload := strings.Join([]string{
		"v1",
		strconv.FormatInt(expiresAt.Unix(), 10),
		base64.RawURLEncoding.EncodeToString(nonce),
	}, ".")
	signature := mobileAuthSignature(a.signingKey, payload)
	return payload + "." + base64.RawURLEncoding.EncodeToString(signature), expiresAt, nil
}

func (a *MobileAuth) validateSessionToken(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return time.Time{}, false
	}
	expiresUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(nonce) != mobileAuthNonceBytes {
		return time.Time{}, false
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(providedSignature) != sha256.Size {
		return time.Time{}, false
	}
	payload := strings.Join(parts[:3], ".")
	wantSignature := mobileAuthSignature(a.signingKey, payload)
	if !hmac.Equal(providedSignature, wantSignature) {
		return time.Time{}, false
	}

	expiresAt := time.Unix(expiresUnix, 0).UTC()
	now := a.now().UTC()
	if !expiresAt.After(now) || expiresAt.After(now.Add(a.sessionTTL+5*time.Minute)) {
		return time.Time{}, false
	}
	return expiresAt, true
}

func (a *MobileAuth) pairingAttemptAllowed(peer string) (bool, time.Duration) {
	a.attemptMu.Lock()
	defer a.attemptMu.Unlock()
	now := a.now()
	a.prunePairingAttempts(now)
	attempt, ok := a.attempts[peer]
	if !ok || now.Sub(attempt.WindowStartedAt) >= a.attemptWindow {
		return true, 0
	}
	if attempt.Failures < a.maxAttempts {
		return true, 0
	}
	return false, max(time.Second, a.attemptWindow-now.Sub(attempt.WindowStartedAt))
}

func (a *MobileAuth) recordPairingFailure(peer string) time.Duration {
	a.attemptMu.Lock()
	defer a.attemptMu.Unlock()
	now := a.now()
	attempt, ok := a.attempts[peer]
	if !ok || now.Sub(attempt.WindowStartedAt) >= a.attemptWindow {
		attempt = mobilePairAttempt{WindowStartedAt: now}
	}
	attempt.Failures++
	a.attempts[peer] = attempt
	if attempt.Failures < a.maxAttempts {
		return 0
	}
	return max(time.Second, a.attemptWindow-now.Sub(attempt.WindowStartedAt))
}

func (a *MobileAuth) clearPairingFailures(peer string) {
	a.attemptMu.Lock()
	delete(a.attempts, peer)
	a.attemptMu.Unlock()
}

func (a *MobileAuth) prunePairingAttempts(now time.Time) {
	if len(a.attempts) < 128 {
		return
	}
	for peer, attempt := range a.attempts {
		if now.Sub(attempt.WindowStartedAt) >= 2*a.attemptWindow {
			delete(a.attempts, peer)
		}
	}
}

func mobileAuthSignature(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func loadOrCreateMobileAuthKey(path string, random io.Reader) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create mobile auth directory: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		key, err := readMobileAuthKey(path)
		if err == nil {
			return key, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		key = make([]byte, mobileAuthKeyBytes)
		if _, err := io.ReadFull(random, key); err != nil {
			return nil, fmt.Errorf("generate mobile auth key: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("create mobile auth key: %w", err)
		}
		writeErr := writeMobileAuthKey(file, key)
		if writeErr != nil {
			_ = os.Remove(path)
			return nil, writeErr
		}
		return key, nil
	}
	return readMobileAuthKey(path)
}

func readMobileAuthKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("read mobile auth key: %w", err)
	}
	if len(key) != mobileAuthKeyBytes {
		return nil, fmt.Errorf("mobile auth key %s has invalid length", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure mobile auth key: %w", err)
	}
	return key, nil
}

func writeMobileAuthKey(file *os.File, key []byte) error {
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		return fmt.Errorf("write mobile auth key: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync mobile auth key: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close mobile auth key: %w", err)
	}
	return nil
}

func generateMobilePairingCode(random io.Reader) (string, error) {
	const sampleLimit = 16_000_000
	var sample [3]byte
	for {
		if _, err := io.ReadFull(random, sample[:]); err != nil {
			return "", err
		}
		value := int(sample[0])<<16 | int(sample[1])<<8 | int(sample[2])
		if value >= sampleLimit {
			continue
		}
		return fmt.Sprintf("%06d", value%1_000_000), nil
	}
}

func normalizeMobilePairingCode(raw string) string {
	var normalized strings.Builder
	normalized.Grow(mobilePairingCodeDigits)
	for _, char := range strings.TrimSpace(raw) {
		switch {
		case char >= '0' && char <= '9':
			normalized.WriteRune(char)
		case char == ' ' || char == '-' || char == '\t':
			continue
		default:
			return ""
		}
	}
	return normalized.String()
}

func mobileAuthPeer(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.String()
	}
	if host == "" {
		return "unknown"
	}
	return strings.ToLower(host)
}

func mobileAuthRemoteIsLoopback(remoteAddr string) bool {
	ip := net.ParseIP(mobileAuthPeer(remoteAddr))
	return ip != nil && ip.IsLoopback()
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("unexpected trailing JSON value")
}
