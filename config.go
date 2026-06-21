package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sridevi14/claude-mini/internal/ui"
)

// envKeyName is the environment variable the key is read from when present.
const envKeyName = "OPENADAPTER_API_KEY"

// configDir returns this tool's per-user config directory (created if needed),
// e.g. %AppData%\mini-code on Windows or ~/.config/mini-code on Unix. The key is
// stored here — in the user's own profile — never in the project's .env.
func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		base, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	dir := filepath.Join(base, "mini-code")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// credentialPath is the file holding the saved API key.
func credentialPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials"), nil
}

// loadSavedKey returns the key stored in the user's config, or "" if none.
func loadSavedKey() string {
	p, err := credentialPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// saveKey writes the key to the user's config with owner-only permissions.
func saveKey(key string) error {
	p, err := credentialPath()
	if err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.TrimSpace(key)+"\n"), 0o600)
}

// resolveAPIKey returns the OpenAdapter API key, asking the user for it the first
// time instead of requiring a committed .env. Resolution order:
//  1. the OPENADAPTER_API_KEY environment variable (and any .env already loaded);
//  2. the key previously saved in the user's config;
//  3. an interactive prompt — offering to save it for next time.
//
// Returns "" only when no key is available and none can be prompted for.
func resolveAPIKey() string {
	if k := strings.TrimSpace(os.Getenv(envKeyName)); k != "" {
		return k
	}
	if k := loadSavedKey(); k != "" {
		return k
	}
	return promptForKey()
}

// promptForKey asks the user to paste their OpenAdapter API key (input hidden)
// and offers to remember it. Returns "" if stdin isn't interactive or nothing
// was entered.
func promptForKey() string {
	if !ui.Interactive() {
		return ""
	}
	ui.Info("")
	ui.Info("  no OpenAdapter API key found.")
	ui.Info("  get one at https://openadapter.in and paste it below (input is hidden).")
	key, ok := ui.ReadSecret(ui.Bold + "  OpenAdapter API key › " + ui.Reset)
	if !ok || key == "" {
		return ""
	}
	// Save it automatically so the user never has to re-enter it; tell them where
	// and how to change it. (No extra yes/no prompt — keeps the startup flow to a
	// single input on every terminal.)
	if err := saveKey(key); err != nil {
		ui.Errorf("couldn't save the key (%v) — using it just for this session.", err)
	} else if p, _ := credentialPath(); p != "" {
		ui.Success("saved to %s  (use /login to change it)", p)
	}
	return key
}
