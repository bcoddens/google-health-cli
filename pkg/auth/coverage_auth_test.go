package auth

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type coverageStringSource struct {
	token       string
	err         error
	invalidated bool
}

func (s *coverageStringSource) Token() (string, error) { return s.token, s.err }
func (s *coverageStringSource) Invalidate()            { s.invalidated = true }

type coverageOAuthSource struct {
	token *oauth2.Token
	err   error
}

func (s coverageOAuthSource) Token() (*oauth2.Token, error) { return s.token, s.err }

func coverageAuthEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	t.Setenv("GHEALTH_ACCESS_TOKEN", "")
	t.Setenv("GHEALTH_CREDENTIALS_FILE", "")
	return dir
}

func coverageClientSecret() *ClientSecret {
	return &ClientSecret{Installed: &ClientConfig{
		ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURIs: []string{"http://localhost/callback"},
	}}
}

func TestCoverageOAuthConfigAndClientSecretVariants(t *testing.T) {
	installed := coverageClientSecret()
	cfg := OAuthConfig(installed, []string{"sleep.readonly", cloudPlatformSuffix}, "")
	if cfg.ClientID != "client-id" || cfg.RedirectURL != "http://localhost/callback" {
		t.Fatalf("OAuth config = %#v", cfg)
	}
	if cfg.Scopes[0] != ScopePrefix+"sleep.readonly" || cfg.Scopes[1] != CloudPlatformScope {
		t.Fatalf("scopes = %#v", cfg.Scopes)
	}
	cfg = OAuthConfig(installed, nil, "http://127.0.0.1:1234")
	if cfg.RedirectURL != "http://127.0.0.1:1234" {
		t.Fatalf("override redirect = %q", cfg.RedirectURL)
	}
	web := &ClientSecret{Web: &ClientConfig{ClientID: "web"}}
	if web.Config().ClientID != "web" {
		t.Fatal("web config not selected")
	}
}

func TestCoverageEnvironmentAndCompositeTokenSources(t *testing.T) {
	coverageAuthEnv(t)
	env := &EnvTokenSource{}
	if _, err := env.Token(); err == nil {
		t.Fatal("empty environment token unexpectedly succeeded")
	}
	t.Setenv("GHEALTH_ACCESS_TOKEN", "env-token")
	if got, err := env.Token(); err != nil || got != "env-token" {
		t.Fatalf("env token = %q, %v", got, err)
	}

	first := &coverageStringSource{err: errors.New("first failed")}
	second := &coverageStringSource{token: "second-token"}
	composite := &CompositeTokenSource{sources: []namedSource{
		{name: "first", source: first}, {name: "second", source: second},
	}}
	if got, err := composite.Token(); err != nil || got != "second-token" {
		t.Fatalf("composite token = %q, %v", got, err)
	}
	composite.Invalidate()
	if !first.invalidated || !second.invalidated {
		t.Fatal("invalidate was not forwarded")
	}

	composite = &CompositeTokenSource{sources: []namedSource{{name: "broken", source: first}}}
	if _, err := composite.Token(); err == nil || !strings.Contains(err.Error(), "first failed") {
		t.Fatalf("composite failure = %v", err)
	}
}

func TestCoverageStoredTokenSourceValidAndInvalidated(t *testing.T) {
	coverageAuthEnv(t)
	creds := &StoredCredentials{AccessToken: "stored-token", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}
	if err := SaveCredentials(creds); err != nil {
		t.Fatal(err)
	}
	source, err := NewKeyringTokenSource()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := source.Token(); err != nil || got != "stored-token" {
		t.Fatalf("stored token = %q, %v", got, err)
	}
	source.Invalidate()
	if _, err := source.Token(); err == nil || !strings.Contains(err.Error(), "no client_secret.json") {
		t.Fatalf("invalidated token error = %v", err)
	}
}

func TestCoverageADCAndFileTokenFailures(t *testing.T) {
	adc := &ADCTokenSource{ts: coverageOAuthSource{token: &oauth2.Token{AccessToken: "adc"}}}
	if got, err := adc.Token(); err != nil || got != "adc" {
		t.Fatalf("ADC token = %q, %v", got, err)
	}
	adc.ts = coverageOAuthSource{err: errors.New("offline")}
	if _, err := adc.Token(); err == nil || !strings.Contains(err.Error(), "ADC token error") {
		t.Fatalf("ADC error = %v", err)
	}

	coverageAuthEnv(t)
	if _, err := NewFileTokenSource(); err == nil {
		t.Fatal("missing file env unexpectedly succeeded")
	}
	t.Setenv("GHEALTH_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := NewFileTokenSource(); err == nil || !strings.Contains(err.Error(), "credentials file not found") {
		t.Fatalf("missing file error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte(`not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHEALTH_CREDENTIALS_FILE", path)
	fs, err := NewFileTokenSource()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Token(); err == nil || !strings.Contains(err.Error(), "failed to parse credentials file") {
		t.Fatalf("invalid credentials error = %v", err)
	}
}

func TestCoverageNonInteractiveStartAndPendingLifecycle(t *testing.T) {
	coverageAuthEnv(t)
	authURL, pending, err := NonInteractiveStart(coverageClientSecret(), []string{"sleep.readonly"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.State) < 20 || len(pending.CodeVerifier) != 43 {
		t.Fatalf("pending state/verifier lengths = %d/%d", len(pending.State), len(pending.CodeVerifier))
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("state") != pending.State || parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("auth URL query = %s", parsed.RawQuery)
	}
	loaded, err := LoadPendingAuth()
	if err != nil || loaded.CodeVerifier != pending.CodeVerifier {
		t.Fatalf("loaded pending = %#v, %v", loaded, err)
	}
	if err := ClearPendingAuth(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPendingAuth(); !os.IsNotExist(err) {
		t.Fatalf("load after clear = %v", err)
	}
	if err := ClearPendingAuth(); err != nil {
		t.Fatalf("idempotent clear = %v", err)
	}
}

func TestCoverageParseCodeInputAndStateMismatch(t *testing.T) {
	cases := []struct {
		input, code, state, errContains string
	}{
		{" bare-code ", "bare-code", "", ""},
		{"", "", "", "empty authorization code"},
		{"http://localhost/?error=denied", "", "", "OAuth provider returned error"},
		{"http://localhost/?state=s", "", "", "no 'code'"},
		{"http://localhost/?code=a%2Fb%3D&state=s", "a/b=", "s", ""},
	}
	for _, tc := range cases {
		code, state, err := parseCodeInput(tc.input)
		if tc.errContains != "" {
			if err == nil || !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("parse %q error = %v", tc.input, err)
			}
			continue
		}
		if err != nil || code != tc.code || state != tc.state {
			t.Errorf("parse %q = %q, %q, %v", tc.input, code, state, err)
		}
	}

	coverageAuthEnv(t)
	pending := &PendingAuth{State: "expected", CodeVerifier: "verifier", RedirectURL: "http://localhost", Scopes: []string{"sleep.readonly"}}
	if err := SavePendingAuth(pending); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CompleteNonInteractive(coverageClientSecret(), "http://localhost/?code=c&state=wrong"); err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("state mismatch error = %v", err)
	}
	if _, err := os.Stat(PendingAuthPath()); !os.IsNotExist(err) {
		t.Fatalf("pending state not cleared: %v", err)
	}
}

func TestCoveragePKCEHelpers(t *testing.T) {
	state, err := randomState()
	if err != nil || len(state) != 22 {
		t.Fatalf("state len=%d err=%v", len(state), err)
	}
	verifier, err := generateCodeVerifier()
	if err != nil || len(verifier) != 43 {
		t.Fatalf("verifier len=%d err=%v", len(verifier), err)
	}
	if got := codeChallengeS256("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("RFC 7636 challenge = %q", got)
	}
}
