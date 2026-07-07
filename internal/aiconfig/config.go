// Package aiconfig holds AI inference settings (provider, model, keys, timeouts).
//
// The on-disk shape supports multiple named profiles. The active profile's
// fields are mirrored onto the Config's top-level fields so existing
// callers that read cfg.Provider / cfg.Model / cfg.BaseURL / cfg.APIKey /
// cfg.TimeoutSec / cfg.RetryMax* keep working unchanged.
package aiconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/appdirs"
)

// Provider selects how appr-ai-sal runs specialist inference.
type Provider string

const (
	ProviderClaude           Provider = "claude"
	ProviderGemini           Provider = "gemini"
	ProviderOllama           Provider = "ollama"
	ProviderOpenAICompatible Provider = "openai_compatible"
)

// ReviewStrictness controls how hard specialists should look for issues.
type ReviewStrictness string

const (
	// ReviewCriticalOnly surfaces only findings at severity "critical"
	// (catastrophic / merge-blocking). Mildest review setting.
	ReviewCriticalOnly ReviewStrictness = "critical_only"
	// ReviewLenient surfaces error and critical (no warnings or info).
	ReviewLenient ReviewStrictness = "lenient"
	// ReviewBalanced is the default tradeoff (warning and above).
	ReviewBalanced ReviewStrictness = "balanced"
	// ReviewStrict surfaces every severity including info-level nits.
	ReviewStrict ReviewStrictness = "strict"
)

// DefaultProfileName is the synthesized profile name for legacy configs
// that have no profiles list on disk.
const DefaultProfileName = "default"

// Profile is one named (provider, model, baseURL, apiKey, timeout, retry)
// preset. The user can switch between profiles from the PR detail
// controls panel without re-typing credentials.
type Profile struct {
	Name             string   `json:"name"`
	Provider         Provider `json:"provider,omitempty"`
	BaseURL          string   `json:"base_url,omitempty"`
	Model            string   `json:"model,omitempty"`
	APIKey           string   `json:"api_key,omitempty"`
	TimeoutSec       int      `json:"timeout_sec,omitempty"`
	RetryMaxAttempts int      `json:"retry_max_attempts,omitempty"`
	RetryBaseMS      int      `json:"retry_base_ms,omitempty"`
	RetryMaxMS       int      `json:"retry_max_ms,omitempty"`
}

// Clone returns a deep copy.
func (p Profile) Clone() Profile { return p }

// Summary returns a short "provider · model" label for UI rows.
func (p Profile) Summary() string {
	prov := string(p.Provider)
	if prov == "" {
		prov = "claude"
	}
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "(default)"
	}
	return prov + " · " + model
}

// Config is the resolved AI settings after Load / merges.
//
// The top-level fields (Provider, BaseURL, Model, APIKey, TimeoutSec,
// RetryMax*) are always a copy of the active profile so existing review
// runner code can continue to read them directly.
type Config struct {
	Provider         Provider         `json:"provider,omitempty"`
	BaseURL          string           `json:"base_url,omitempty"`
	Model            string           `json:"model,omitempty"`
	APIKey           string           `json:"api_key,omitempty"`
	TimeoutSec       int              `json:"timeout_sec,omitempty"`
	ReviewStrictness ReviewStrictness `json:"review_strictness,omitempty"`
	// RetryMaxAttempts is total tries per inference call (including the first). 0 uses default (5); 1 disables retry.
	RetryMaxAttempts int `json:"retry_max_attempts,omitempty"`
	// RetryBaseMS is initial backoff before the second attempt. 0 uses default (1500).
	RetryBaseMS int `json:"retry_base_ms,omitempty"`
	// RetryMaxMS caps exponential backoff growth per wait. 0 uses default (120000).
	RetryMaxMS int `json:"retry_max_ms,omitempty"`

	// Profiles is the on-disk list of named (provider, model, ...) presets.
	// The active profile's fields are mirrored onto the top-level fields.
	Profiles []Profile `json:"profiles,omitempty"`
	// ActiveProfile names which entry of Profiles is currently in use.
	ActiveProfile string `json:"active_profile,omitempty"`
}

// DefaultConfig returns built-in defaults (before file, env, or flags).
func DefaultConfig() *Config {
	c := &Config{
		Provider:         ProviderClaude,
		TimeoutSec:       300,
		ReviewStrictness: ReviewBalanced,
		ActiveProfile:    DefaultProfileName,
	}
	c.Profiles = []Profile{c.snapshotProfile(DefaultProfileName)}
	return c
}

// ConfigDir is the app config directory (same rules as prompt overrides).
// Delegates to internal/appdirs so path resolution lives in one place.
func ConfigDir() string {
	return appdirs.ConfigDir()
}

// DefaultPath returns the default path for persisted AI settings (JSON).
func DefaultPath() string {
	return filepath.Join(ConfigDir(), "ai.json")
}

// Clone returns a deep copy safe to pass into async work.
func (c *Config) Clone() *Config {
	if c == nil {
		return DefaultConfig()
	}
	cp := *c
	if c.Profiles != nil {
		cp.Profiles = make([]Profile, len(c.Profiles))
		copy(cp.Profiles, c.Profiles)
	}
	return &cp
}

// Merge overlays non-zero fields from o onto c (active profile only).
func (c *Config) Merge(o *Config) {
	if o == nil {
		return
	}
	if o.Provider != "" {
		c.Provider = o.Provider
	}
	if o.BaseURL != "" {
		c.BaseURL = o.BaseURL
	}
	if o.Model != "" {
		c.Model = o.Model
	}
	if o.APIKey != "" {
		c.APIKey = o.APIKey
	}
	if o.TimeoutSec != 0 {
		c.TimeoutSec = o.TimeoutSec
	}
	if o.ReviewStrictness != "" {
		c.ReviewStrictness = o.ReviewStrictness
	}
	if o.RetryMaxAttempts != 0 {
		c.RetryMaxAttempts = o.RetryMaxAttempts
	}
	if o.RetryBaseMS != 0 {
		c.RetryBaseMS = o.RetryBaseMS
	}
	if o.RetryMaxMS != 0 {
		c.RetryMaxMS = o.RetryMaxMS
	}
}

// ParseProvider normalizes a user string to a Provider.
func ParseProvider(s string) (Provider, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	switch s {
	case "", "claude":
		return ProviderClaude, nil
	case "gemini":
		return ProviderGemini, nil
	case "ollama":
		return ProviderOllama, nil
	case "openai_compatible":
		return ProviderOpenAICompatible, nil
	default:
		return "", fmt.Errorf("unknown AI provider %q (want claude, gemini, ollama, openai_compatible)", s)
	}
}

// ParseReviewStrictness maps user input to a strictness level.
func ParseReviewStrictness(s string) (ReviewStrictness, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	switch s {
	case "", "balanced", "normal", "default":
		return ReviewBalanced, nil
	case "critical_only", "critical-only", "critical", "minimal", "showstopper", "merge_blockers", "mergeblockers":
		return ReviewCriticalOnly, nil
	case "lenient", "light", "rubber_stamp", "rubberstamp", "easy":
		return ReviewLenient, nil
	case "strict", "thorough", "heavy", "deep":
		return ReviewStrict, nil
	default:
		return "", fmt.Errorf("unknown review strictness %q (want critical_only, lenient, balanced, strict)", s)
	}
}

// Load reads defaults, optional JSON at DefaultPath(), then environment.
// Resolution order for each field: defaults < file < env.
//
// On-disk migration: a legacy file with no `profiles` key is automatically
// wrapped into a single "default" profile.
func Load() (*Config, error) {
	c := DefaultConfig()
	path := DefaultPath()
	if b, err := os.ReadFile(path); err == nil {
		var fileCfg Config
		if err := json.Unmarshal(b, &fileCfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if len(fileCfg.Profiles) == 0 {
			// Legacy flat shape: wrap top-level fields into one "default" profile.
			c.Merge(&fileCfg)
			c.Profiles = []Profile{c.snapshotProfile(DefaultProfileName)}
			c.ActiveProfile = DefaultProfileName
		} else {
			c.Profiles = make([]Profile, len(fileCfg.Profiles))
			copy(c.Profiles, fileCfg.Profiles)
			c.ActiveProfile = strings.TrimSpace(fileCfg.ActiveProfile)
			if fileCfg.ReviewStrictness != "" {
				c.ReviewStrictness = fileCfg.ReviewStrictness
			}
			c.applyActiveProfile()
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	mergeEnv(c)
	if err := c.normalize(); err != nil {
		return nil, err
	}
	return c, nil
}

func mergeEnv(c *Config) {
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_AI_PROVIDER")); v != "" {
		if p, err := ParseProvider(v); err == nil {
			c.Provider = p
		}
	}
	if v := os.Getenv("APPR_AI_SAL_AI_BASE_URL"); v != "" {
		c.BaseURL = strings.TrimSpace(v)
	}
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_AI_MODEL")); v != "" {
		c.Model = v
	}
	if v := os.Getenv("APPR_AI_SAL_AI_API_KEY"); v != "" {
		c.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_AI_TIMEOUT_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.TimeoutSec = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_REVIEW_STRICTNESS")); v != "" {
		if rs, err := ParseReviewStrictness(v); err == nil {
			c.ReviewStrictness = rs
		}
	}
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_AI_RETRY_MAX_ATTEMPTS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.RetryMaxAttempts = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_AI_RETRY_BASE_MS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.RetryBaseMS = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_AI_RETRY_MAX_MS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.RetryMaxMS = n
		}
	}
}

func (c *Config) normalize() error {
	if c.Provider == "" {
		c.Provider = ProviderClaude
	}
	p, err := ParseProvider(string(c.Provider))
	if err != nil {
		return err
	}
	c.Provider = p
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = 300
	}
	rs, err := ParseReviewStrictness(string(c.ReviewStrictness))
	if err != nil {
		rs = ReviewBalanced
	}
	c.ReviewStrictness = rs
	if c.RetryMaxAttempts == 0 {
		c.RetryMaxAttempts = 5
	}
	if c.RetryMaxAttempts > 30 {
		c.RetryMaxAttempts = 30
	}
	if c.RetryBaseMS == 0 {
		c.RetryBaseMS = 1500
	}
	if c.RetryMaxMS == 0 {
		c.RetryMaxMS = 120000
	}
	if c.RetryBaseMS > c.RetryMaxMS {
		c.RetryBaseMS = c.RetryMaxMS
	}
	if len(c.Profiles) == 0 {
		c.ActiveProfile = DefaultProfileName
		c.Profiles = []Profile{c.snapshotProfile(DefaultProfileName)}
	} else {
		// Make sure ActiveProfile points at an existing entry; if not,
		// fall back to the first profile.
		if _, ok := c.findProfileIndex(c.ActiveProfile); !ok {
			c.ActiveProfile = c.Profiles[0].Name
		}
		// Mirror top-level fields back onto the active profile slot so
		// env / flag merges that updated the flat fields stay consistent
		// with the persisted profile.
		c.syncActiveProfileFromFlat()
	}
	return nil
}

// snapshotProfile builds a Profile from the current top-level fields.
func (c *Config) snapshotProfile(name string) Profile {
	return Profile{
		Name:             strings.TrimSpace(name),
		Provider:         c.Provider,
		BaseURL:          c.BaseURL,
		Model:            c.Model,
		APIKey:           c.APIKey,
		TimeoutSec:       c.TimeoutSec,
		RetryMaxAttempts: c.RetryMaxAttempts,
		RetryBaseMS:      c.RetryBaseMS,
		RetryMaxMS:       c.RetryMaxMS,
	}
}

// findProfileIndex returns the index of the named profile (case-insensitive
// on the trimmed name) and whether it was found.
func (c *Config) findProfileIndex(name string) (int, bool) {
	target := strings.ToLower(strings.TrimSpace(name))
	if target == "" {
		return -1, false
	}
	for i, p := range c.Profiles {
		if strings.ToLower(strings.TrimSpace(p.Name)) == target {
			return i, true
		}
	}
	return -1, false
}

// applyActiveProfile copies the active profile's fields onto the top-level
// fields. Called after loading (or after SetActive) so existing callers
// reading cfg.Provider / cfg.Model / etc. see the right values.
func (c *Config) applyActiveProfile() {
	if len(c.Profiles) == 0 {
		return
	}
	idx, ok := c.findProfileIndex(c.ActiveProfile)
	if !ok {
		idx = 0
		c.ActiveProfile = c.Profiles[0].Name
	}
	p := c.Profiles[idx]
	c.Provider = p.Provider
	c.BaseURL = p.BaseURL
	c.Model = p.Model
	c.APIKey = p.APIKey
	c.TimeoutSec = p.TimeoutSec
	c.RetryMaxAttempts = p.RetryMaxAttempts
	c.RetryBaseMS = p.RetryBaseMS
	c.RetryMaxMS = p.RetryMaxMS
}

// syncActiveProfileFromFlat copies the top-level fields back into the
// active profile slot. Called by normalize() so flag / env overrides that
// adjusted the flat fields are reflected in Profiles for the next save.
func (c *Config) syncActiveProfileFromFlat() {
	idx, ok := c.findProfileIndex(c.ActiveProfile)
	if !ok {
		return
	}
	c.Profiles[idx] = c.snapshotProfile(c.Profiles[idx].Name)
}

// Active returns a copy of the active profile (synthesised from top-level
// fields if Profiles is empty). The returned value is safe to mutate.
func (c *Config) Active() Profile {
	if c == nil {
		return Profile{Name: DefaultProfileName, Provider: ProviderClaude, TimeoutSec: 300}
	}
	if idx, ok := c.findProfileIndex(c.ActiveProfile); ok {
		return c.Profiles[idx]
	}
	if len(c.Profiles) > 0 {
		return c.Profiles[0]
	}
	return c.snapshotProfile(DefaultProfileName)
}

// SetActive switches to the named profile and mirrors its fields onto the
// top-level fields. Returns an error when the name does not match any
// existing profile.
func (c *Config) SetActive(name string) error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	idx, ok := c.findProfileIndex(name)
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	c.ActiveProfile = c.Profiles[idx].Name
	c.applyActiveProfile()
	return nil
}

// AddProfile appends p to the profile list. Returns an error when a
// profile with the same name already exists or when the name is empty.
func (c *Config) AddProfile(p Profile) error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return fmt.Errorf("profile name is empty")
	}
	if _, ok := c.findProfileIndex(name); ok {
		return fmt.Errorf("profile %q already exists", name)
	}
	p.Name = name
	c.Profiles = append(c.Profiles, p)
	return nil
}

// UpdateProfile replaces the profile at the given name. The top-level
// fields are re-synced from the active profile when the updated profile
// is the active one.
func (c *Config) UpdateProfile(name string, p Profile) error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	idx, ok := c.findProfileIndex(name)
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = c.Profiles[idx].Name
	}
	// If the rename collides with a different existing entry, refuse.
	if !strings.EqualFold(p.Name, c.Profiles[idx].Name) {
		if other, exists := c.findProfileIndex(p.Name); exists && other != idx {
			return fmt.Errorf("profile %q already exists", p.Name)
		}
	}
	wasActive := strings.EqualFold(c.ActiveProfile, c.Profiles[idx].Name)
	c.Profiles[idx] = p
	if wasActive {
		c.ActiveProfile = p.Name
		c.applyActiveProfile()
	}
	return nil
}

// DeleteProfile removes the named profile. The last profile cannot be
// deleted (the file would have no active profile to fall back to). If
// the deleted profile was active, the first remaining profile becomes
// active.
func (c *Config) DeleteProfile(name string) error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	if len(c.Profiles) <= 1 {
		return fmt.Errorf("cannot delete the last profile")
	}
	idx, ok := c.findProfileIndex(name)
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	wasActive := strings.EqualFold(c.ActiveProfile, c.Profiles[idx].Name)
	c.Profiles = append(c.Profiles[:idx], c.Profiles[idx+1:]...)
	if wasActive {
		c.ActiveProfile = c.Profiles[0].Name
		c.applyActiveProfile()
	}
	return nil
}

// RenameProfile renames an existing profile. The active-profile pointer
// is updated when the renamed entry is currently active.
func (c *Config) RenameProfile(oldName, newName string) error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("new profile name is empty")
	}
	idx, ok := c.findProfileIndex(oldName)
	if !ok {
		return fmt.Errorf("profile %q not found", oldName)
	}
	if other, exists := c.findProfileIndex(newName); exists && other != idx {
		return fmt.Errorf("profile %q already exists", newName)
	}
	wasActive := strings.EqualFold(c.ActiveProfile, c.Profiles[idx].Name)
	c.Profiles[idx].Name = newName
	if wasActive {
		c.ActiveProfile = newName
	}
	return nil
}

// CycleActive moves the active pointer by delta (typically +/-1) through
// the profile list, wrapping at the ends. No-op when fewer than 2
// profiles are configured.
func (c *Config) CycleActive(delta int) {
	if c == nil || len(c.Profiles) < 2 {
		return
	}
	idx, ok := c.findProfileIndex(c.ActiveProfile)
	if !ok {
		idx = 0
	}
	n := len(c.Profiles)
	idx = ((idx+delta)%n + n) % n
	c.ActiveProfile = c.Profiles[idx].Name
	c.applyActiveProfile()
}

// MergeFlags applies non-empty CLI overrides (empty string means “leave unchanged”).
// Pass timeoutSec < 0 to leave timeout unchanged.
func (c *Config) MergeFlags(provider, baseURL, model, apiKey, reviewStrictness string, timeoutSec int) error {
	if strings.TrimSpace(provider) != "" {
		p, err := ParseProvider(provider)
		if err != nil {
			return err
		}
		c.Provider = p
	}
	if baseURL != "" {
		c.BaseURL = strings.TrimSpace(baseURL)
	}
	if model != "" {
		c.Model = strings.TrimSpace(model)
	}
	if apiKey != "" {
		c.APIKey = apiKey
	}
	if strings.TrimSpace(reviewStrictness) != "" {
		rs, err := ParseReviewStrictness(reviewStrictness)
		if err != nil {
			return err
		}
		c.ReviewStrictness = rs
	}
	if timeoutSec >= 0 {
		c.TimeoutSec = timeoutSec
	}
	return c.normalize()
}

// Save writes c to path with restrictive permissions. If path is empty, uses DefaultPath().
func Save(c *Config, path string) error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	if err := c.normalize(); err != nil {
		return err
	}
	if path == "" {
		path = DefaultPath()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Persist only the profile list + active selector + strictness; the
	// flat fields are rebuilt from the active profile on load.
	persisted := struct {
		ReviewStrictness ReviewStrictness `json:"review_strictness,omitempty"`
		Profiles         []Profile        `json:"profiles"`
		ActiveProfile    string           `json:"active_profile,omitempty"`
	}{
		ReviewStrictness: c.ReviewStrictness,
		Profiles:         c.Profiles,
		ActiveProfile:    c.ActiveProfile,
	}
	b, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// EffectiveTimeoutSec returns a positive timeout in seconds for HTTP / run context.
func (c *Config) EffectiveTimeoutSec() int {
	if c == nil || c.TimeoutSec <= 0 {
		return 300
	}
	return c.TimeoutSec
}

// AIBaseURLResolved returns the HTTP base URL for OpenAI-compatible and Ollama,
// or the Gemini API origin. Trailing slashes are stripped where relevant.
func (c *Config) AIBaseURLResolved() string {
	if c == nil {
		return ""
	}
	switch c.Provider {
	case ProviderOllama:
		if strings.TrimSpace(c.BaseURL) == "" {
			return "http://127.0.0.1:11434/v1"
		}
	case ProviderGemini:
		if strings.TrimSpace(c.BaseURL) == "" {
			return "https://generativelanguage.googleapis.com"
		}
	}
	return strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
}

// AIModelOrDefault picks a model id, including Claude legacy env when applicable.
func (c *Config) AIModelOrDefault() string {
	if c == nil {
		return ""
	}
	if strings.TrimSpace(c.Model) != "" {
		return strings.TrimSpace(c.Model)
	}
	if c.Provider == ProviderClaude {
		if v := strings.TrimSpace(os.Getenv("APPR_AI_SAL_MODEL")); v != "" {
			return v
		}
		return "sonnet"
	}
	return ""
}

// EffectiveAPIKey returns the configured API key (may be empty for Ollama).
func (c *Config) EffectiveAPIKey() string {
	if c == nil {
		return ""
	}
	return c.APIKey
}

// BearerForHTTP returns the Authorization bearer token for OpenAI-compatible APIs.
// When no key is set (typical for local Ollama), uses a placeholder bearer like jj-tui.
func (c *Config) BearerForHTTP() string {
	key := strings.TrimSpace(c.EffectiveAPIKey())
	if key != "" {
		return key
	}
	if c != nil && (c.Provider == ProviderOllama || c.Provider == ProviderOpenAICompatible) {
		return "ollama"
	}
	return ""
}

// RunContextTimeout is the context timeout for an entire review run (specialists + vibe).
func (c *Config) RunContextTimeout() time.Duration {
	sec := c.EffectiveTimeoutSec()
	// Keep a reasonable floor so tiny values do not abort immediately.
	if sec < 120 {
		sec = 120
	}
	return time.Duration(sec) * time.Second
}

// InferenceRetryMaxAttempts returns how many times each Complete call may try (≥1).
func (c *Config) InferenceRetryMaxAttempts() int {
	if c == nil {
		return 5
	}
	n := c.RetryMaxAttempts
	if n <= 0 {
		return 5
	}
	if n > 30 {
		return 30
	}
	return n
}

// InferenceRetryBase returns the first backoff delay before a retry.
func (c *Config) InferenceRetryBase() time.Duration {
	ms := 1500
	if c != nil && c.RetryBaseMS > 0 {
		ms = c.RetryBaseMS
	}
	return time.Duration(ms) * time.Millisecond
}

// InferenceRetryMaxBackoff caps each backoff wait between retries.
func (c *Config) InferenceRetryMaxBackoff() time.Duration {
	ms := 120000
	if c != nil && c.RetryMaxMS > 0 {
		ms = c.RetryMaxMS
	}
	return time.Duration(ms) * time.Millisecond
}
