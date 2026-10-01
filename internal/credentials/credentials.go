// Package credentials resolves and stores the Tello API key
// (docs/design.md section 7): TELLO_API_KEY, then the OS keychain, then
// <config dir>/credentials.json. The key never appears in errors.
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-go/tello"
	"github.com/zalando/go-keyring"
)

// Keychain entry of the stored API key.
const (
	Service = "tello-cli"
	Account = "api-key"
)

const fileName = "credentials.json"

// Store resolves, saves and deletes the API key.
type Store struct {
	getenv    func(string) string
	configDir func() (string, error)
	keychain  app.SecretStore
}

// New returns a Store. configDir is the tello configuration directory; the
// credentials file lives directly inside it. keychain may be nil.
func New(getenv func(string) string, configDir func() (string, error), keychain app.SecretStore) *Store {
	return &Store{getenv: getenv, configDir: configDir, keychain: keychain}
}

type fileContent struct {
	APIKey string `json:"apiKey"`
}

// Resolve returns the effective API key: TELLO_API_KEY, then the keychain,
// then the credentials file. An unavailable keychain is skipped. It returns
// app.ErrNoCredential when no source has a key.
func (s *Store) Resolve() (app.Credential, error) {
	if key := strings.TrimSpace(s.getenv(tello.EnvAPIKey)); key != "" {
		return app.Credential{Key: key, Source: "env"}, nil
	}
	if s.keychain != nil {
		if key, err := s.keychain.Get(Service, Account); err == nil {
			if key = strings.TrimSpace(key); key != "" {
				return app.Credential{Key: key, Source: "keyring"}, nil
			}
		}
	}
	path, err := s.filePath()
	if err != nil {
		return app.Credential{}, app.ErrNoCredential
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return app.Credential{}, app.ErrNoCredential
	}
	if err != nil {
		return app.Credential{}, fmt.Errorf("read credentials file: %w", err)
	}
	var content fileContent
	// The decode error is dropped on purpose: it can quote file content.
	if json.Unmarshal(data, &content) != nil {
		return app.Credential{}, fmt.Errorf("credentials file %s is not valid JSON; run `tello auth login` again", path)
	}
	key := strings.TrimSpace(content.APIKey)
	if key == "" {
		return app.Credential{}, app.ErrNoCredential
	}
	return app.Credential{Key: key, Source: "file", Path: path}, nil
}

// Saved reports where Save put the key.
type Saved struct {
	app.Credential
	// KeychainMayShadow is set when the key went to the file but an older
	// keychain entry could not be removed: once the keychain is usable
	// again, that entry takes precedence over the file.
	KeychainMayShadow bool
}

// Save stores key in the keychain, or in the credentials file (dir 0700,
// file 0600) when the keychain cannot be written. Saving to one place
// clears the other so a stale key cannot shadow or outlive the new one.
func (s *Store) Save(key string) (Saved, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Saved{}, errors.New("empty API key")
	}
	if s.keychain != nil && s.keychain.Set(Service, Account, key) == nil {
		if path, err := s.filePath(); err == nil {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return Saved{}, fmt.Errorf("remove old credentials file: %w", err)
			}
		}
		return Saved{Credential: app.Credential{Key: key, Source: "keyring"}}, nil
	}
	path, err := s.writeFile(key)
	if err != nil {
		return Saved{}, err
	}
	saved := Saved{Credential: app.Credential{Key: key, Source: "file", Path: path}}
	if s.keychain != nil {
		err := s.keychain.Delete(Service, Account)
		saved.KeychainMayShadow = err != nil && !errors.Is(err, app.ErrSecretNotFound)
	}
	return saved, nil
}

// Delete removes the key from the keychain and the credentials file and
// returns the sources it actually removed ("keyring", "file"). An
// unavailable keychain is skipped.
func (s *Store) Delete() ([]string, error) {
	removed := []string{}
	if s.keychain != nil && s.keychain.Delete(Service, Account) == nil {
		removed = append(removed, "keyring")
	}
	path, err := s.filePath()
	if err != nil {
		return removed, nil
	}
	switch err := os.Remove(path); {
	case err == nil:
		removed = append(removed, "file")
	case !errors.Is(err, os.ErrNotExist):
		return removed, fmt.Errorf("remove credentials file: %w", err)
	}
	return removed, nil
}

func (s *Store) filePath() (string, error) {
	dir, err := s.configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// writeFile replaces the credentials file atomically: the temp file is
// created 0600 in the same directory, so the key is never world-readable.
func (s *Store) writeFile(key string) (string, error) {
	path, err := s.filePath()
	if err != nil {
		return "", fmt.Errorf("locate config directory: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.Marshal(fileContent{APIKey: key})
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, fileName+".*")
	if err != nil {
		return "", fmt.Errorf("write credentials file: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write credentials file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("write credentials file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("write credentials file: %w", err)
	}
	return path, nil
}

// SystemKeychain is the OS keychain (macOS Keychain, Windows Credential
// Manager, Secret Service on Linux).
type SystemKeychain struct{}

var _ app.SecretStore = SystemKeychain{}

func (SystemKeychain) Get(service, user string) (string, error) {
	secret, err := keyring.Get(service, user)
	return secret, mapKeyringError(err)
}

func (SystemKeychain) Set(service, user, secret string) error {
	return mapKeyringError(keyring.Set(service, user, secret))
}

func (SystemKeychain) Delete(service, user string) error {
	return mapKeyringError(keyring.Delete(service, user))
}

func mapKeyringError(err error) error {
	if errors.Is(err, keyring.ErrNotFound) {
		return app.ErrSecretNotFound
	}
	return err
}
