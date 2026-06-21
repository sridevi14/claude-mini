package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/sridevi14/claude-mini/internal/ui"
)

// Built-in default provider (OpenAdapter). Keeping it means existing users need
// no setup or migration — the tool behaves exactly as before out of the box.
const (
	defaultBaseURL = "https://api.openadapter.in/v1"
	defaultModel   = "deepseek-v3"
)

// Environment overrides — the highest-precedence configuration layer. They let CI
// and power users point the tool at any OpenAI-compatible endpoint without writing
// to the config file.
const (
	envBaseURL = "CLAUDE_MINI_BASE_URL"
	envAPIKey  = "CLAUDE_MINI_API_KEY"
	envModel   = "CLAUDE_MINI_MODEL"
	envModels  = "CLAUDE_MINI_MODELS" // optional comma-separated /model shortlist

	// Legacy fallback so keys saved before multi-provider support keep working.
	legacyEnvAPIKey = "OPENADAPTER_API_KEY"
)

// provider is a known OpenAI-compatible endpoint preset. Unlisted endpoints are
// still fully supported via the "custom" option — these are just conveniences.
type provider struct {
	Name       string
	Label      string // friendly display name, e.g. "OpenAI"
	Blurb      string // one-line, jargon-free description for menus
	BaseURL    string
	Models     []string // suggested models for the /model shortlist
	KeyHint    string   // where to obtain a key (shown when prompting)
	DefaultKey string   // used when the endpoint ignores auth (e.g. local Ollama)
}

// providerPresets returns the built-in provider shortcuts, in display order.
func providerPresets() []provider {
	return []provider{
		{
			Name:    "openadapter",
			Label:   "OpenAdapter",
			Blurb:   "many models with one key (default)",
			BaseURL: "https://api.openadapter.in/v1",
			Models:  []string{"deepseek-v3", "anthropic/claude-sonnet", "glm-4.5", "qwen2.5-coder-32b-instruct", "kimi-k2-instruct"},
			KeyHint: "https://openadapter.in",
		},
		{
			Name:    "openai",
			Label:   "OpenAI",
			Blurb:   "GPT-4o and others — needs an OpenAI key",
			BaseURL: "https://api.openai.com/v1",
			Models:  []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1", "o3-mini"},
			KeyHint: "https://platform.openai.com/api-keys",
		},
		{
			Name:    "openrouter",
			Label:   "OpenRouter",
			Blurb:   "100s of models incl. Claude — needs a key",
			BaseURL: "https://openrouter.ai/api/v1",
			Models:  []string{"anthropic/claude-sonnet-4", "openai/gpt-4o", "google/gemini-2.0-flash-001", "deepseek/deepseek-chat"},
			KeyHint: "https://openrouter.ai/keys",
		},
		{
			Name:       "ollama",
			Label:      "Ollama",
			Blurb:      "run models on your computer — free, no key",
			BaseURL:    "http://localhost:11434/v1",
			Models:     []string{"qwen3:latest", "llama3.1:latest", "qwen2.5-coder:latest", "deepseek-r1:latest"},
			KeyHint:    "local — no key needed",
			DefaultKey: "ollama", // Ollama ignores the Authorization header
		},
	}
}

// providerLabel returns a friendly display name for a base URL ("OpenAI"), or the
// base URL itself for unknown/custom endpoints.
func providerLabel(base string) string {
	if p, ok := providerFor(base); ok {
		return p.Label
	}
	return base
}

// isFirstRun reports whether the user has no saved config yet — used to show a
// one-time, jargon-free welcome explaining what the tool is.
func isFirstRun() bool {
	if p, err := configPath(); err == nil {
		if _, err := os.Stat(p); err == nil {
			return false
		}
	}
	if lp, err := legacyCredentialPath(); err == nil {
		if _, err := os.Stat(lp); err == nil {
			return false
		}
	}
	return true
}

// providerFor returns the preset matching base (by URL), or false if none.
func providerFor(base string) (provider, bool) {
	base = normalizeBase(base)
	for _, p := range providerPresets() {
		if normalizeBase(p.BaseURL) == base {
			return p, true
		}
	}
	return provider{}, false
}

func normalizeBase(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// --- persisted config ---------------------------------------------------------

// fileConfig is the on-disk config (JSON). API keys are stored per base URL so a
// user can keep keys for several providers and switch between them freely.
type fileConfig struct {
	BaseURL string            `json:"base_url,omitempty"`
	Model   string            `json:"model,omitempty"`
	APIKeys map[string]string `json:"api_keys,omitempty"`
}

// configDir returns this tool's per-user config directory (created if needed),
// e.g. %AppData%\claude-mini on Windows or ~/.config/claude-mini on Unix.
func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		base, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	dir := filepath.Join(base, "claude-mini")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	migrateLegacyDir(base, dir)
	return dir, nil
}

// migrateLegacyDir performs a one-time copy of saved settings from the old
// "mini-code" directory into the current "claude-mini" one, so users who set a key
// before the rename don't have to re-enter it. It's a no-op once the new dir is set
// up or if there's nothing to migrate.
func migrateLegacyDir(base, newDir string) {
	if _, err := os.Stat(filepath.Join(newDir, "config.json")); err == nil {
		return // already configured under the new name
	}
	old := filepath.Join(base, "mini-code")
	if old == newDir {
		return
	}
	for _, name := range []string{"config.json", "credentials"} {
		if b, err := os.ReadFile(filepath.Join(old, name)); err == nil {
			_ = os.WriteFile(filepath.Join(newDir, name), b, 0o600)
		}
	}
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// legacyCredentialPath is the pre-multi-provider single-key file, migrated on load.
func legacyCredentialPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials"), nil
}

// loadConfig reads the JSON config, migrating an old single-key credentials file
// (if present and the new config has no keys yet) into the per-provider structure.
func loadConfig() fileConfig {
	var cfg fileConfig
	if p, err := configPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &cfg)
		}
	}
	if cfg.APIKeys == nil {
		cfg.APIKeys = map[string]string{}
	}
	if len(cfg.APIKeys) == 0 {
		if lp, err := legacyCredentialPath(); err == nil {
			if b, err := os.ReadFile(lp); err == nil {
				if k := strings.TrimSpace(string(b)); k != "" {
					cfg.APIKeys[normalizeBase(defaultBaseURL)] = k
				}
			}
		}
	}
	return cfg
}

func saveConfig(cfg fileConfig) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o600)
}

// savedKeyFor returns the stored key for a base URL, or "".
func savedKeyFor(base string) string { return loadConfig().APIKeys[normalizeBase(base)] }

// saveKeyFor persists an API key for a specific base URL (owner-only perms).
func saveKeyFor(base, key string) error {
	cfg := loadConfig()
	if cfg.APIKeys == nil {
		cfg.APIKeys = map[string]string{}
	}
	cfg.APIKeys[normalizeBase(base)] = strings.TrimSpace(key)
	return saveConfig(cfg)
}

// saveActive records the active provider (base URL) and model.
func saveActive(base, model string) error {
	cfg := loadConfig()
	cfg.BaseURL = normalizeBase(base)
	if model != "" {
		cfg.Model = model
	}
	return saveConfig(cfg)
}

// saveModelPref records the active model.
func saveModelPref(model string) error {
	cfg := loadConfig()
	cfg.Model = model
	return saveConfig(cfg)
}

// --- resolution ---------------------------------------------------------------

// Settings is the fully-resolved active configuration for a run.
type Settings struct {
	BaseURL string
	APIKey  string
	Model   string
}

// resolveSettings layers env vars over the saved config over built-in defaults to
// produce the active base URL, model and key. When no key is found for the active
// provider it prompts (and saves) — unless stdin isn't interactive, in which case
// APIKey is left "" for the caller to handle.
func resolveSettings() Settings {
	cfg := loadConfig()
	base := normalizeBase(firstNonEmpty(os.Getenv(envBaseURL), cfg.BaseURL, defaultBaseURL))
	model := firstNonEmpty(os.Getenv(envModel), cfg.Model, defaultModel)

	defKey := ""
	if p, ok := providerFor(base); ok {
		defKey = p.DefaultKey
	}
	key := firstNonEmpty(os.Getenv(envAPIKey), os.Getenv(legacyEnvAPIKey), cfg.APIKeys[base], defKey)

	s := Settings{BaseURL: base, APIKey: key, Model: model}
	if s.APIKey == "" {
		s.APIKey = promptForKey(base)
	}
	return s
}

// promptForKey asks the user to paste an API key for base (input hidden) and saves
// it. Returns "" if stdin isn't interactive or nothing was entered.
func promptForKey(base string) string {
	if !ui.Interactive() {
		return ""
	}
	label := providerLabel(base)
	ui.Info("")
	ui.Info("  To use %s, paste your API key below — it stays hidden and is saved", label)
	ui.Info("  only on this computer.")
	if p, ok := providerFor(base); ok && p.KeyHint != "" {
		ui.Info("  Get a key at: %s", p.KeyHint)
	}
	ui.Info("  No key? Press Enter to skip, then run /provider and pick Ollama (free, local).")
	key, ok := ui.ReadSecret(ui.Bold + "  " + label + " API key › " + ui.Reset)
	if !ok || key == "" {
		return ""
	}
	if err := saveKeyFor(base, key); err != nil {
		ui.Errorf("couldn't save the key (%v) — using it just for this session.", err)
	} else if p, _ := configPath(); p != "" {
		ui.Success("saved to %s  (use /login to change it)", p)
	}
	return key
}

// maskKey renders a key for display without revealing it.
func maskKey(k string) string {
	k = strings.TrimSpace(k)
	switch {
	case k == "":
		return "(none)"
	case len(k) <= 8:
		return "********"
	default:
		return k[:4] + "…" + k[len(k)-4:]
	}
}
