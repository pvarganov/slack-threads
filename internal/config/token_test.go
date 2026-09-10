package config_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/config"
	"github.com/zalando/go-keyring"
)

// memStore is the in-memory TokenStore the rest of the app is tested with.
type memStore struct {
	mu    sync.Mutex
	token string
	// setErr, when set, is returned by SetToken.
	setErr error
	// getErr, when set, is returned by Token.
	getErr error
}

func (s *memStore) Token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.getErr != nil {
		return "", s.getErr
	}

	if s.token == "" {
		return "", config.ErrNoToken
	}

	return s.token, nil
}

func (s *memStore) SetToken(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.setErr != nil {
		return s.setErr
	}

	s.token = token

	return nil
}

func (s *memStore) DeleteToken() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.token = ""

	return nil
}

func TestMemStoreRoundTrip(t *testing.T) {
	var s config.TokenStore = &memStore{}

	if _, err := s.Token(); !errors.Is(err, config.ErrNoToken) {
		t.Fatalf("Token on empty store = %v, want ErrNoToken", err)
	}

	if err := s.SetToken("xoxp-1"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	got, err := s.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if got != "xoxp-1" {
		t.Errorf("Token = %q, want xoxp-1", got)
	}

	if err := s.DeleteToken(); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}

	if _, err := s.Token(); !errors.Is(err, config.ErrNoToken) {
		t.Fatalf("Token after delete = %v, want ErrNoToken", err)
	}
}

func TestKeyringStoreRoundTrip(t *testing.T) {
	keyring.MockInit()

	s := config.KeyringStore{Service: "slack-threads-test", User: "token"}

	if _, err := s.Token(); !errors.Is(err, config.ErrNoToken) {
		t.Fatalf("Token before write = %v, want ErrNoToken", err)
	}

	if err := s.SetToken("xoxp-secret"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	got, err := s.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if got != "xoxp-secret" {
		t.Errorf("Token = %q, want xoxp-secret", got)
	}

	if err := s.DeleteToken(); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}

	if _, err := s.Token(); !errors.Is(err, config.ErrNoToken) {
		t.Fatalf("Token after delete = %v, want ErrNoToken", err)
	}

	// Deleting again must not fail.
	if err := s.DeleteToken(); err != nil {
		t.Fatalf("DeleteToken twice: %v", err)
	}
}

func TestKeyringStoreRejectsEmptyToken(t *testing.T) {
	keyring.MockInit()

	s := config.KeyringStore{Service: "slack-threads-test-empty", User: "token"}

	if err := s.SetToken("   "); err == nil {
		t.Fatal("SetToken(blank): want error, got nil")
	}
}

func TestKeyringStoreDefaultCoordinates(t *testing.T) {
	keyring.MockInit()

	var s config.TokenStore = config.KeyringStore{}

	if err := s.SetToken("xoxp-default"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	t.Cleanup(func() { s.DeleteToken() }) //nolint:errcheck // best effort cleanup of a mock keyring

	got, err := keyring.Get(config.KeyringService, config.KeyringUser)
	if err != nil {
		t.Fatalf("keyring.Get: %v", err)
	}

	if got != "xoxp-default" {
		t.Errorf("stored token = %q", got)
	}
}

func TestEnvStore(t *testing.T) {
	s := config.EnvStore{
		Var:    "TEST_SLACK_TOKEN",
		Getenv: func(k string) string { return map[string]string{"TEST_SLACK_TOKEN": " xoxp-env "}[k] },
	}

	got, err := s.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if got != "xoxp-env" {
		t.Errorf("Token = %q, want trimmed xoxp-env", got)
	}

	if err := s.SetToken("xoxp-other"); !errors.Is(err, config.ErrReadOnlyStore) {
		t.Errorf("SetToken = %v, want ErrReadOnlyStore", err)
	}

	if err := s.DeleteToken(); !errors.Is(err, config.ErrReadOnlyStore) {
		t.Errorf("DeleteToken = %v, want ErrReadOnlyStore", err)
	}
}

func TestEnvStoreMissing(t *testing.T) {
	s := config.EnvStore{Var: "TEST_SLACK_TOKEN", Getenv: func(string) string { return "" }}

	if _, err := s.Token(); !errors.Is(err, config.ErrNoToken) {
		t.Fatalf("Token = %v, want ErrNoToken", err)
	}
}

func TestEnvStoreDefaultsToProcessEnv(t *testing.T) {
	t.Setenv(config.EnvToken, "xoxp-from-process")

	got, err := config.EnvStore{}.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if got != "xoxp-from-process" {
		t.Errorf("Token = %q", got)
	}
}

func TestChainStorePrefersEnvironment(t *testing.T) {
	env := config.EnvStore{Var: "T", Getenv: func(string) string { return "xoxp-env" }}
	kc := &memStore{token: "xoxp-keychain"}

	chain := config.ChainStore{Stores: []config.TokenStore{env, kc}}

	got, err := chain.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if got != "xoxp-env" {
		t.Errorf("Token = %q, want the environment value", got)
	}
}

func TestChainStoreFallsBackAndWritesToKeychain(t *testing.T) {
	env := config.EnvStore{Var: "T", Getenv: func(string) string { return "" }}
	kc := &memStore{}

	chain := config.ChainStore{Stores: []config.TokenStore{env, kc}}

	if _, err := chain.Token(); !errors.Is(err, config.ErrNoToken) {
		t.Fatalf("Token on empty chain = %v, want ErrNoToken", err)
	}

	if err := chain.SetToken("xoxp-new"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	if kc.token != "xoxp-new" {
		t.Errorf("keychain token = %q, want xoxp-new", kc.token)
	}

	got, err := chain.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if got != "xoxp-new" {
		t.Errorf("Token = %q", got)
	}

	if err := chain.DeleteToken(); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}

	if kc.token != "" {
		t.Errorf("keychain token after delete = %q", kc.token)
	}
}

func TestChainStorePropagatesRealErrors(t *testing.T) {
	boom := errors.New("keychain locked")
	chain := config.ChainStore{Stores: []config.TokenStore{&memStore{getErr: boom}, &memStore{token: "xoxp-x"}}}

	if _, err := chain.Token(); !errors.Is(err, boom) {
		t.Fatalf("Token = %v, want the underlying error", err)
	}
}

func TestChainStoreWriteFailsWhenAllReadOnly(t *testing.T) {
	chain := config.ChainStore{Stores: []config.TokenStore{config.EnvStore{Var: "T"}}}

	if err := chain.SetToken("xoxp-x"); !errors.Is(err, config.ErrReadOnlyStore) {
		t.Errorf("SetToken = %v, want ErrReadOnlyStore", err)
	}
}

func TestEmptyChainHasNoToken(t *testing.T) {
	if _, err := (config.ChainStore{}).Token(); !errors.Is(err, config.ErrNoToken) {
		t.Errorf("Token = %v, want ErrNoToken", err)
	}
}

func TestNewTokenStoreReadsEnvironmentFirst(t *testing.T) {
	keyring.MockInit()
	t.Setenv(config.EnvToken, "xoxp-env-wins")

	got, err := config.NewTokenStore().Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if got != "xoxp-env-wins" {
		t.Errorf("Token = %q", got)
	}
}
