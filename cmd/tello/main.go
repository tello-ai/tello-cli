// Command tello is the Tello CLI. See docs/design.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/term"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/cli"
	"github.com/tello-ai/tello-cli/internal/credentials"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handleSignals(cancel)

	keychain := credentials.SystemKeychain{}
	store := credentials.New(os.Getenv, configDir, keychain)
	a := &app.App{
		Stdin:           os.Stdin,
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		Getenv:          os.Getenv,
		Getwd:           os.Getwd,
		ConfigDir:       configDir,
		Keychain:        keychain,
		APIKey:          store.Resolve,
		StdinIsTerminal: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		ReadSecret:      readSecret,
	}
	code := cli.Main(ctx, a, os.Args[1:])
	cancel()
	os.Exit(code)
}

// handleSignals turns the first Ctrl-C/SIGTERM into context cancellation,
// which cancels a running call gracefully, and the second into an
// immediate exit. A pending secret prompt exits at once, restoring echo.
func handleSignals(cancel context.CancelFunc) {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	<-sigs
	if restoreTerminal() {
		fmt.Fprintln(os.Stderr)
		os.Exit(130)
	}
	cancel()
	<-sigs
	restoreTerminal()
	fmt.Fprintln(os.Stderr, "tello: interrupted")
	os.Exit(130)
}

func configDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tello"), nil
}

var (
	promptMu    sync.Mutex
	promptState *term.State
)

// readSecret prompts on stderr and reads a line without echo.
func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return "", err
	}
	promptMu.Lock()
	promptState = state
	promptMu.Unlock()
	defer func() {
		promptMu.Lock()
		promptState = nil
		promptMu.Unlock()
	}()

	fmt.Fprint(os.Stderr, prompt)
	secret, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(secret), err
}

// restoreTerminal restores echo if a secret prompt is active and reports
// whether one was.
func restoreTerminal() bool {
	promptMu.Lock()
	defer promptMu.Unlock()
	if promptState == nil {
		return false
	}
	_ = term.Restore(int(os.Stdin.Fd()), promptState)
	return true
}
