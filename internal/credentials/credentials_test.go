package credentials

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/zalando/go-keyring"
)

// fakeKeychain is an in-memory app.SecretStore. When err is set every call
// fails with it, like a keychain that is unavailable; setErr fails only Set.
type fakeKeychain struct {
	secrets map[string]string
	err     error
	setErr  error
}

func newFakeKeychain() *fakeKeychain { return &fakeKeychain{secrets: map[string]string{}} }

func (k *fakeKeychain) Get(service, user string) (string, error) {
	if k.err != nil {
		return "", k.err
	}
	v, ok := k.secrets[service+"/"+user]
	if !ok {
		return "", app.ErrSecretNotFound
	}
	return v, nil
}

func (k *fakeKeychain) Set(service, user, secret string) error {
	if k.err != nil {
		return k.err
	}
	if k.setErr != nil {
		return k.setErr
	}
	k.secrets[service+"/"+user] = secret
	return nil
}

func (k *fakeKeychain) Delete(service, user string) error {
	if k.err != nil {
		return k.err
	}
	if _, ok := k.secrets[service+"/"+user]; !ok {
		return app.ErrSecretNotFound
	}
	delete(k.secrets, service+"/"+user)
	return nil
}

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func dirFunc(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

func writeCredentialsFile(t *testing.T, dir, key string) string {
	t.Helper()
	path := filepath.Join(dir, "credentials.json")
	data, _ := json.Marshal(map[string]string{"apiKey": key})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

var errUnavailable = errors.New("dbus: no session bus")

func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	filePath := writeCredentialsFile(t, dir, "file-key")
	full := newFakeKeychain()
	full.secrets[Service+"/"+Account] = "keyring-key\n"

	tests := []struct {
		name     string
		env      map[string]string
		keychain *fakeKeychain
		want     app.Credential
	}{
		{"env wins", map[string]string{"TELLO_API_KEY": " env-key\n"}, full, app.Credential{Key: "env-key", Source: "env"}},
		{"keyring over file", nil, full, app.Credential{Key: "keyring-key", Source: "keyring"}},
		{"file when keyring empty", nil, newFakeKeychain(), app.Credential{Key: "file-key", Source: "file", Path: filePath}},
		{"file when keyring unavailable", nil, &fakeKeychain{err: errUnavailable}, app.Credential{Key: "file-key", Source: "file", Path: filePath}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(env(tt.env), dirFunc(dir), tt.keychain).Resolve()
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Resolve = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveWithoutAnyKeyIsErrNoCredential(t *testing.T) {
	_, err := New(env(nil), dirFunc(t.TempDir()), newFakeKeychain()).Resolve()
	if !errors.Is(err, app.ErrNoCredential) {
		t.Fatalf("Resolve error = %v, want ErrNoCredential", err)
	}
}

func TestSaveFallsBackToPrivateFileWhenKeychainUnavailable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tello")
	store := New(env(nil), dirFunc(dir), &fakeKeychain{err: errUnavailable})

	cred, err := store.Save("  secret-key\n")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	wantPath := filepath.Join(dir, "credentials.json")
	if cred.Source != "file" || cred.Path != wantPath {
		t.Fatalf("Save = %+v, want file at %s", cred, wantPath)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(wantPath)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("file mode = %o, want 600", perm)
		}
		dirInfo, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if perm := dirInfo.Mode().Perm(); perm != 0o700 {
			t.Fatalf("dir mode = %o, want 700", perm)
		}
	}

	got, err := store.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Key != "secret-key" || got.Source != "file" {
		t.Fatalf("Resolve = %+v, want trimmed key from file", got)
	}
}

func TestSaveToFileReportsKeychainEntryThatMayShadowIt(t *testing.T) {
	errLocked := errors.New("keychain is locked")
	tests := []struct {
		name       string
		keychain   *fakeKeychain
		wantShadow bool
	}{
		{"keychain unusable: old entry cannot be removed", &fakeKeychain{secrets: map[string]string{}, err: errLocked}, true},
		{"write fails, no old entry", &fakeKeychain{secrets: map[string]string{}, setErr: errLocked}, false},
		{"write fails, old entry removed", &fakeKeychain{secrets: map[string]string{Service + "/" + Account: "old-key"}, setErr: errLocked}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saved, err := New(env(nil), dirFunc(t.TempDir()), tt.keychain).Save("new-key")
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			if saved.Source != "file" || saved.KeychainMayShadow != tt.wantShadow {
				t.Fatalf("Save = %+v, want file with KeychainMayShadow %v", saved, tt.wantShadow)
			}
			if _, left := tt.keychain.secrets[Service+"/"+Account]; left && tt.keychain.err == nil {
				t.Fatalf("old keychain entry was not removed")
			}
		})
	}
}

func TestSaveToKeychainRemovesOldPlainFile(t *testing.T) {
	dir := t.TempDir()
	path := writeCredentialsFile(t, dir, "old-key")
	kc := newFakeKeychain()

	cred, err := New(env(nil), dirFunc(dir), kc).Save("new-key")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if cred.Source != "keyring" || kc.secrets[Service+"/"+Account] != "new-key" {
		t.Fatalf("Save = %+v, keychain = %v", cred, kc.secrets)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old credentials file still present: %v", err)
	}
}

func TestDeleteRemovesKeychainAndFile(t *testing.T) {
	dir := t.TempDir()
	path := writeCredentialsFile(t, dir, "file-key")
	kc := newFakeKeychain()
	kc.secrets[Service+"/"+Account] = "keyring-key"
	store := New(env(nil), dirFunc(dir), kc)

	removed, err := store.Delete()
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !slices.Equal(removed, []string{"keyring", "file"}) {
		t.Fatalf("removed = %v, want [keyring file]", removed)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials file still present: %v", err)
	}
	if _, err := store.Resolve(); !errors.Is(err, app.ErrNoCredential) {
		t.Fatalf("Resolve after Delete = %v, want ErrNoCredential", err)
	}

	removed, err = store.Delete()
	if err != nil || len(removed) != 0 {
		t.Fatalf("second Delete = %v, %v; want nothing removed", removed, err)
	}
}

func TestDeleteSkipsUnavailableKeychain(t *testing.T) {
	dir := t.TempDir()
	writeCredentialsFile(t, dir, "file-key")

	removed, err := New(env(nil), dirFunc(dir), &fakeKeychain{err: errUnavailable}).Delete()
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !slices.Equal(removed, []string{"file"}) {
		t.Fatalf("removed = %v, want [file]", removed)
	}
}

func TestSystemKeychainMapsNotFound(t *testing.T) {
	keyring.MockInit() // in-memory provider; never touches the OS keychain
	var kc SystemKeychain
	if _, err := kc.Get(Service, Account); !errors.Is(err, app.ErrSecretNotFound) {
		t.Fatalf("Get missing = %v, want ErrSecretNotFound", err)
	}
	if err := kc.Delete(Service, Account); !errors.Is(err, app.ErrSecretNotFound) {
		t.Fatalf("Delete missing = %v, want ErrSecretNotFound", err)
	}
}
