// Package repoconfig loads optional repo-context settings (local clones, caps, PR history).
package repoconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
)

const defaultMaxBytes = 24576
const defaultTTL = 24 * time.Hour
const defaultPRHistoryLimit = 30
const defaultRepoExpertReviewPRs = 8
const defaultRepoExpertMaxBytes = 12000
const defaultRepoExpertReviewTTL = 6 * time.Hour

// Config controls repository context harvesting and caching.
type Config struct {
	// RepoRoots maps "owner/repo" (case-insensitive keys normalized to lower) to an absolute local clone path used to supplement missing convention files.
	RepoRoots map[string]string `json:"repo_roots,omitempty"`
	// MaxBytes is the hard cap for the injected repository context block (default 24576).
	MaxBytes int `json:"max_bytes,omitempty"`
	// TTLSeconds is how long a cached bundle on disk is considered fresh (default 86400).
	TTLSeconds int `json:"ttl_seconds,omitempty"`
	// IncludePRHistory when true fetches recent merged PR titles into the bundle.
	IncludePRHistory bool `json:"include_pr_history,omitempty"`
	// PRHistoryLimit caps merged PR rows (default 30).
	PRHistoryLimit int `json:"pr_history_limit,omitempty"`
	// RepoCultureSummarize runs one extra inference pass to turn merged-PR titles into short bullets (uses the same AI provider as reviews).
	RepoCultureSummarize bool `json:"repo_culture_summarize,omitempty"`
	// ContextVersusChangeSummary runs one inference pass that explains how the composed repository context relates to this PR's diff (same AI provider; parallel with specialists).
	ContextVersusChangeSummary bool `json:"context_versus_change_summary,omitempty"`
	// RepoExpertPanel runs repo code + review-history experts and an arbiter before vibe-coach (same AI provider).
	RepoExpertPanel bool `json:"repo_expert_panel,omitempty"`
	// RepoExpertReviewPRs caps how many merged PRs are sampled for review-body history digest (default 8).
	RepoExpertReviewPRs int `json:"repo_expert_review_prs,omitempty"`
	// RepoExpertMaxBytes caps the review-history markdown digest passed to the history expert (default 12000).
	RepoExpertMaxBytes int `json:"repo_expert_max_bytes,omitempty"`
	// RepoExpertReviewTTLSeconds is cache freshness for the review-history digest (default 21600 = 6h).
	RepoExpertReviewTTLSeconds int `json:"repo_expert_review_ttl_seconds,omitempty"`
	// ParallelSpecialists runs all code-review specialists concurrently (same AI provider). Default false (sequential) to reduce API rate-limit bursts.
	ParallelSpecialists bool `json:"parallel_specialists,omitempty"`
	// ParallelRepoExperts runs repo code expert and repo history expert concurrently before the arbiter. Default false (sequential).
	ParallelRepoExperts bool `json:"parallel_repo_experts,omitempty"`
	// IncludeRepoEvidence injects per-PR static evidence (sibling tests,
	// doc.go, exported-symbol coverage) and a path-history aggregate into the
	// testing/docs specialists and into the testing/docs repo-agent generators.
	// Default true.
	IncludeRepoEvidence bool `json:"include_repo_evidence,omitempty"`
	// RepoArbiterDemotions allows the repo arbiter to emit demote actions
	// (drop a finding's severity by one rank when the repo tolerates that
	// kind of finding). Default true.
	RepoArbiterDemotions bool `json:"repo_arbiter_demotions,omitempty"`
	// ConventionWitness runs a per-finding "convention witness" pass between
	// specialists and the repo arbiter, classifying testing/docs findings as
	// congruent / divergent / unknown vs the PR's static evidence. Default true.
	ConventionWitness bool `json:"convention_witness,omitempty"`
	// TechAgents enables per-repo "technology expert" briefs (one brief per
	// technology, shared across all specialists for that repo). When false,
	// stored briefs are left on disk but not injected at review time.
	// Default true.
	TechAgents bool `json:"tech_agents,omitempty"`
}

// Default returns defaults suitable for merging.
func Default() *Config {
	return &Config{
		MaxBytes:                   defaultMaxBytes,
		TTLSeconds:                 int(defaultTTL.Seconds()),
		IncludePRHistory:           true,
		PRHistoryLimit:             defaultPRHistoryLimit,
		ContextVersusChangeSummary: true,
		RepoExpertPanel:            true,
		RepoExpertReviewPRs:        defaultRepoExpertReviewPRs,
		RepoExpertMaxBytes:         defaultRepoExpertMaxBytes,
		RepoExpertReviewTTLSeconds: int(defaultRepoExpertReviewTTL.Seconds()),
		RepoRoots:                  map[string]string{},
		IncludeRepoEvidence:        true,
		RepoArbiterDemotions:       true,
		ConventionWitness:          true,
		TechAgents:                 true,
	}
}

// DefaultPath is ~/.config/appr-ai-sal/repo-context.json (or APPR_AI_SAL_CONFIG_DIR).
func DefaultPath() string {
	return filepath.Join(aiconfig.ConfigDir(), "repo-context.json")
}

// Load reads repo-context.json if present; otherwise returns defaults.
func Load() (*Config, error) {
	c := Default()
	path := DefaultPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var fileCfg Config
	if err := json.Unmarshal(b, &fileCfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.Merge(&fileCfg)
	if bytes.Contains(b, []byte(`"include_pr_history"`)) {
		c.IncludePRHistory = fileCfg.IncludePRHistory
	} else {
		c.IncludePRHistory = true
	}
	if bytes.Contains(b, []byte(`"repo_culture_summarize"`)) {
		c.RepoCultureSummarize = fileCfg.RepoCultureSummarize
	}
	if bytes.Contains(b, []byte(`"context_versus_change_summary"`)) {
		c.ContextVersusChangeSummary = fileCfg.ContextVersusChangeSummary
	} else {
		c.ContextVersusChangeSummary = true
	}
	if bytes.Contains(b, []byte(`"repo_expert_panel"`)) {
		c.RepoExpertPanel = fileCfg.RepoExpertPanel
	} else {
		c.RepoExpertPanel = true
	}
	if bytes.Contains(b, []byte(`"parallel_specialists"`)) {
		c.ParallelSpecialists = fileCfg.ParallelSpecialists
	}
	if bytes.Contains(b, []byte(`"parallel_repo_experts"`)) {
		c.ParallelRepoExperts = fileCfg.ParallelRepoExperts
	}
	if bytes.Contains(b, []byte(`"include_repo_evidence"`)) {
		c.IncludeRepoEvidence = fileCfg.IncludeRepoEvidence
	} else {
		c.IncludeRepoEvidence = true
	}
	if bytes.Contains(b, []byte(`"repo_arbiter_demotions"`)) {
		c.RepoArbiterDemotions = fileCfg.RepoArbiterDemotions
	} else {
		c.RepoArbiterDemotions = true
	}
	if bytes.Contains(b, []byte(`"convention_witness"`)) {
		c.ConventionWitness = fileCfg.ConventionWitness
	} else {
		c.ConventionWitness = true
	}
	if bytes.Contains(b, []byte(`"tech_agents"`)) {
		c.TechAgents = fileCfg.TechAgents
	} else {
		c.TechAgents = true
	}
	c.Normalize()
	return c, nil
}

// ApplyParallelExecutionEnv overrides parallel_specialists / parallel_repo_experts
// when APPR_AI_SAL_PARALLEL_SPECIALISTS or APPR_AI_SAL_PARALLEL_REPO_EXPERTS is set
// (truthy: 1/true/yes/on; falsy otherwise).
func ApplyParallelExecutionEnv(c *Config) {
	if c == nil {
		return
	}
	if v, ok := os.LookupEnv("APPR_AI_SAL_PARALLEL_SPECIALISTS"); ok {
		c.ParallelSpecialists = parseBoolEnv(v)
	}
	if v, ok := os.LookupEnv("APPR_AI_SAL_PARALLEL_REPO_EXPERTS"); ok {
		c.ParallelRepoExperts = parseBoolEnv(v)
	}
}

func parseBoolEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Merge overlays non-zero / meaningful fields from o.
func (c *Config) Merge(o *Config) {
	if o == nil {
		return
	}
	if len(o.RepoRoots) > 0 {
		if c.RepoRoots == nil {
			c.RepoRoots = map[string]string{}
		}
		for k, v := range o.RepoRoots {
			c.RepoRoots[normalizeRepoKey(k)] = v
		}
	}
	if o.MaxBytes > 0 {
		c.MaxBytes = o.MaxBytes
	}
	if o.TTLSeconds > 0 {
		c.TTLSeconds = o.TTLSeconds
	}
	if o.PRHistoryLimit > 0 {
		c.PRHistoryLimit = o.PRHistoryLimit
	}
	if o.RepoExpertReviewPRs > 0 {
		c.RepoExpertReviewPRs = o.RepoExpertReviewPRs
	}
	if o.RepoExpertMaxBytes > 0 {
		c.RepoExpertMaxBytes = o.RepoExpertMaxBytes
	}
	if o.RepoExpertReviewTTLSeconds > 0 {
		c.RepoExpertReviewTTLSeconds = o.RepoExpertReviewTTLSeconds
	}
	// IncludePRHistory and RepoCultureSummarize are applied in Load() only when keys appear in JSON.
}

func (c *Config) normalize() {
	if c.MaxBytes < 2048 {
		c.MaxBytes = defaultMaxBytes
	}
	if c.TTLSeconds < 60 {
		c.TTLSeconds = int(defaultTTL.Seconds())
	}
	if c.PRHistoryLimit < 1 {
		c.PRHistoryLimit = defaultPRHistoryLimit
	}
	if c.RepoExpertReviewPRs < 1 {
		c.RepoExpertReviewPRs = defaultRepoExpertReviewPRs
	}
	if c.RepoExpertMaxBytes < 1024 {
		c.RepoExpertMaxBytes = defaultRepoExpertMaxBytes
	}
	if c.RepoExpertReviewTTLSeconds < 60 {
		c.RepoExpertReviewTTLSeconds = int(defaultRepoExpertReviewTTL.Seconds())
	}
	if c.RepoRoots == nil {
		c.RepoRoots = map[string]string{}
	}
	out := make(map[string]string, len(c.RepoRoots))
	for k, v := range c.RepoRoots {
		out[normalizeRepoKey(k)] = strings.TrimSpace(v)
	}
	c.RepoRoots = out
}

// Normalize applies defaults and sanitizes maps after loading or unmarshalling.
func (c *Config) Normalize() {
	if c == nil {
		return
	}
	c.normalize()
}

// Save writes cfg as indented JSON to path (DefaultPath if empty).
func Save(cfg *Config, path string) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	if path == "" {
		path = DefaultPath()
	}
	cfg.Normalize()
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// TTL returns cache TTL as duration.
func (c *Config) TTL() time.Duration {
	if c == nil || c.TTLSeconds <= 0 {
		return defaultTTL
	}
	return time.Duration(c.TTLSeconds) * time.Second
}

// RepoExpertReviewTTL returns cache TTL for the review-history digest.
func (c *Config) RepoExpertReviewTTL() time.Duration {
	if c == nil || c.RepoExpertReviewTTLSeconds <= 0 {
		return defaultRepoExpertReviewTTL
	}
	return time.Duration(c.RepoExpertReviewTTLSeconds) * time.Second
}

// LocalRootFor returns the configured absolute path for owner/repo, or "".
func (c *Config) LocalRootFor(owner, repo string) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.RepoRoots[normalizeRepoKey(owner+"/"+repo)])
}

func normalizeRepoKey(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, "github.com/", "")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	return s
}

// FormatRepoRootsLines returns repo_roots as editable "owner/repo=/abs/path" lines, sorted by key.
func FormatRepoRootsLines(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := strings.TrimSpace(m[k])
		if v == "" {
			continue
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(v)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ParseRepoRootsLines parses one mapping per line: owner/repo=/absolute/path
// (equals separates key from path; whitespace around = is trimmed).
func ParseRepoRootsLines(s string) (map[string]string, error) {
	out := make(map[string]string)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, "=")
		if i <= 0 {
			return nil, fmt.Errorf("repo roots line %q: need owner/repo=/path", line)
		}
		key := normalizeRepoKey(line[:i])
		val := strings.TrimSpace(line[i+1:])
		if key == "" {
			return nil, fmt.Errorf("repo roots line %q: empty owner/repo", line)
		}
		if val == "" {
			continue
		}
		out[key] = val
	}
	return out, nil
}
