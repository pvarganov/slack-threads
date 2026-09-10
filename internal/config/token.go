package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

// Keychain coordinates of the stored token.
const (
	// KeyringService is the service name the token is filed under.
	KeyringService = "slack-threads"
	// KeyringUser is the account name inside that service. The app holds
	// exactly one token, so it is a constant.
	KeyringUser = "slack-user-token"
)

// ErrNoToken means no token has been stored yet. It is not a failure: a
// fresh install simply has nothing in the keychain.
var ErrNoToken = errors.New("config: no slack token stored")

// TokenStore is the persistent home of the Slack user token. Production
// uses KeyringStore; tests substitute an in-memory implementation.
type TokenStore interface {
	// Token returns the stored token, or ErrNoToken when there is none.
	Token() (string, error)
	// SetToken replaces the stored token.
	SetToken(token string) error
	// DeleteToken removes the token; deleting a missing token succeeds.
	DeleteToken() error
}

// KeyringStore keeps the token in the system keychain.
type KeyringStore struct {
	// Service and User address the keychain item; empty values fall back
	// to KeyringService and KeyringUser.
	Service string
	User    string
}

// coords resolves the keychain address, filling in the defaults.
func (s KeyringStore) coords() (string, string) {
	service, user := s.Service, s.User
	if service == "" {
		service = KeyringService
	}

	if user == "" {
		user = KeyringUser
	}

	return service, user
}

// Token reads the token from the keychain.
func (s KeyringStore) Token() (string, error) {
	service, user := s.coords()

	secret, err := keyring.Get(service, user)

	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return "", ErrNoToken
	case err != nil:
		return "", fmt.Errorf("config: read token from keychain: %w", err)
	}

	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", ErrNoToken
	}

	return secret, nil
}

// SetToken writes the token into the keychain.
func (s KeyringStore) SetToken(token string) error {
	service, user := s.coords()

	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("config: refusing to store an empty token")
	}

	if err := keyring.Set(service, user, token); err != nil {
		return fmt.Errorf("config: write token to keychain: %w", err)
	}

	return nil
}

// DeleteToken removes the keychain item.
func (s KeyringStore) DeleteToken() error {
	service, user := s.coords()

	err := keyring.Delete(service, user)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("config: delete token from keychain: %w", err)
	}

	return nil
}

// EnvStore is the environment fallback: it reads a token from a variable
// and cannot store anything. It exists for development and for machines
// where the keychain is unavailable (headless CI, remote shells).
type EnvStore struct {
	// Var is the variable read; empty means EnvToken.
	Var string
	// Getenv is the lookup; nil means os.Getenv.
	Getenv func(string) string
}

// ErrReadOnlyStore means the token came from the environment and cannot be
// changed by the app.
var ErrReadOnlyStore = errors.New("config: token comes from the environment and cannot be changed here")

// Token reads the variable.
func (s EnvStore) Token() (string, error) {
	name := s.Var
	if name == "" {
		name = EnvToken
	}

	getenv := s.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	token := strings.TrimSpace(getenv(name))
	if token == "" {
		return "", ErrNoToken
	}

	return token, nil
}

// SetToken always fails: the environment is not ours to write.
func (s EnvStore) SetToken(string) error { return ErrReadOnlyStore }

// DeleteToken always fails, for the same reason.
func (s EnvStore) DeleteToken() error { return ErrReadOnlyStore }

// ChainStore reads from the first store that has a token and writes to the
// first store that accepts one. Ordering the environment before the
// keychain lets a developer override the stored token for one run without
// destroying it.
type ChainStore struct {
	// Stores are consulted in order; an empty chain behaves as empty.
	Stores []TokenStore
}

// NewTokenStore builds the production chain: the environment variable
// first, the system keychain second.
func NewTokenStore() TokenStore {
	return ChainStore{Stores: []TokenStore{EnvStore{}, KeyringStore{}}}
}

// Token returns the first token found. A store that fails for a reason
// other than "nothing stored" aborts the lookup: a broken keychain must
// not look like a missing token.
func (c ChainStore) Token() (string, error) {
	for _, s := range c.Stores {
		token, err := s.Token()

		switch {
		case errors.Is(err, ErrNoToken):
			continue
		case err != nil:
			return "", err
		}

		return token, nil
	}

	return "", ErrNoToken
}

// SetToken writes into the first store that accepts writes.
func (c ChainStore) SetToken(token string) error {
	return c.write(func(s TokenStore) error { return s.SetToken(token) })
}

// DeleteToken removes the token from the first writable store.
func (c ChainStore) DeleteToken() error {
	return c.write(TokenStore.DeleteToken)
}

// write applies op to the stores in order, skipping the read-only ones.
func (c ChainStore) write(op func(TokenStore) error) error {
	lastErr := ErrReadOnlyStore

	for _, s := range c.Stores {
		err := op(s)
		if err == nil {
			return nil
		}

		if !errors.Is(err, ErrReadOnlyStore) {
			return err
		}

		lastErr = err
	}

	return lastErr
}
