// Package repoconfig loads optional repo-context settings (local clones, caps, PR history).
package repoconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/madicen/appr-ai-sal/internal/appdirs"
)

const defaultMaxBytes = 24576
const defaultMaxConcurrentInference = 3

// Circuit-breaker defaults (R4). The run aborts the remaining AI stages when
// EITHER trips: too many consecutive stage failures, or the whole-run
// wall-clock cap is exceeded. Both are "abort before starting the next stage"
// checks — an in-flight stage is never interrupted mid-call. A configured
// value <= 0 disables that arm of the breaker (see the OrDefault accessors).
const defaultMaxConsecutiveStageFailures = 4
const defaultRunWallClockCap = 30 * time.Minute
const defaultTTL = 24 * time.Hour
const defaultPRHistoryLimit = 30
const defaultRepoExpertReviewPRs = 8
const defaultRepoExpertMaxBytes = 12000
const defaultRepoExpertReviewTTL = 6 * time.Hour

// defaultDiffElisionGlobs is the baked-in glob set the R3 diff budgeter uses
// to drop non-review-worthy files (lockfiles, vendored trees, generated code,
// minified assets) from the diff before it is inlined into review prompts.
// Each dropped file is replaced by a one-line manifest entry so the model —
// and the reviewer — still know it changed. Users can override the list via
// repo-context.json's diff_elision_globs; when that key is unset these apply.
//
// Matching rules (see review.matchDiffGlob): a pattern ending in "/" is a
// directory prefix (e.g. "vendor/"); a pattern containing "/" is matched
// against the full path; a pattern with neither is matched against the file's
// basename. So "*_generated*" matches "pkg/foo_generated.go" (basename match)
// even though path.Match's "*" does not cross a slash.
var defaultDiffElisionGlobs = []string{
	"*.lock",
	"package-lock.json",
	"yarn.lock",
	"pnpm-lock.yaml",
	"go.sum",
	"Cargo.lock",
	"composer.lock",
	"Gemfile.lock",
	"poetry.lock",
	"vendor/",
	"*_generated*",
	"*.min.js",
	"*.min.css",
}

// DefaultDiffElisionGlobs returns a copy of the baked-in elision glob set so
// callers can read it without mutating the package default.
func DefaultDiffElisionGlobs() []string {
	return append([]string(nil), defaultDiffElisionGlobs...)
}

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
	// ParallelSpecialists runs all code-review specialists concurrently (same
	// AI provider). Default true: MaxConcurrentInference now caps the total
	// in-flight inference calls across the run, so parallel dispatch no longer
	// risks unbounded API rate-limit bursts — it is the single biggest
	// wall-clock win available. Set to false in repo-context.json to force
	// sequential dispatch.
	//
	// The json tag intentionally omits `omitempty`: now that the default is
	// true, an explicit false must survive Save (whole-struct marshal), which
	// omitempty would silently drop — reload would then resurrect the default.
	ParallelSpecialists bool `json:"parallel_specialists"`
	// ParallelRepoExperts runs repo code expert and repo history expert concurrently before the arbiter. Default false (sequential).
	ParallelRepoExperts bool `json:"parallel_repo_experts,omitempty"`
	// ParallelPRAgents runs the PR-level agents (description, checks,
	// discussion, scope) concurrently with the code specialists phase — they
	// share no data dependency, so overlapping them shortens wall-clock time
	// — and concurrently among themselves. Default true, mirroring
	// ParallelSpecialists: MaxConcurrentInference caps total concurrent
	// inference, so overlapping the phases is now safe. Set to false to force
	// sequential dispatch after the specialists.
	//
	// Like ParallelSpecialists, the json tag omits `omitempty` so an explicit
	// false persists through Save against the new default of true.
	ParallelPRAgents bool `json:"parallel_pr_agents"`
	// MaxConcurrentInference caps how many inference calls run concurrently
	// across the whole run — including the hidden repair pass and PR-agent
	// calls — regardless of which parallel toggles are on. Default 3. A value
	// <= 0 resolves to the default (never unlimited): this is the client-side
	// rate limit that makes the parallel defaults above safe.
	MaxConcurrentInference int `json:"max_concurrent_inference,omitempty"`
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
	// PRAgents enables the PR-level review agents (description, checks,
	// discussion, scope) that evaluate the pull request as a whole and post
	// their feedback alongside the code specialists. Default true.
	PRAgents bool `json:"pr_agents,omitempty"`
	// DiffElisionGlobs overrides the R3 diff budgeter's set of globs for files
	// dropped from the diff before it is inlined into review prompts (each
	// dropped file becomes a one-line manifest entry). When empty the baked-in
	// defaultDiffElisionGlobs apply — see DefaultDiffElisionGlobs. Matching is
	// basename-based for slash-free patterns, prefix-based for patterns ending
	// in "/", and full-path for the rest.
	DiffElisionGlobs []string `json:"diff_elision_globs,omitempty"`
	// DiffByteCap overrides the whole-diff byte budget the R3 budgeter enforces
	// before inlining the diff into a prompt. 0 (unset) resolves to a
	// conservative per-provider default so a large PR never blows the provider
	// context window / triggers a 400. Files/hunks beyond the cap are elided
	// and disclosed.
	DiffByteCap int `json:"diff_byte_cap,omitempty"`
	// DiffPerFileLineCap overrides the R3 budgeter's per-file unified-diff line
	// cap; the tail of any file exceeding it is elided with a "…N lines
	// omitted" marker (leading lines keep their real line numbers so inline
	// findings still anchor correctly). 0 (unset) resolves to the default.
	DiffPerFileLineCap int `json:"diff_per_file_line_cap,omitempty"`
	// MaxConsecutiveStageFailures is the R4 circuit-breaker limit: after this
	// many AI stages fail in a row (in result order), the run aborts the
	// remaining stages instead of grinding through the whole panel. 0 (unset)
	// resolves to the default (4); a negative value disables this arm.
	MaxConsecutiveStageFailures int `json:"max_consecutive_stage_failures,omitempty"`
	// RunWallClockCapSeconds is the R4 whole-run wall-clock cap. Once elapsed,
	// no further stage is STARTED (in-flight stages finish); the remaining
	// stages are marked skipped and disclosed. 0 (unset) resolves to the
	// default (1800s = 30m); a negative value disables the cap. This never
	// interrupts a running stage mid-call — it only stops starting new ones.
	RunWallClockCapSeconds int `json:"run_wall_clock_cap_seconds,omitempty"`
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
		PRAgents:                   true,
		ParallelSpecialists:        true,
		ParallelPRAgents:           true,
		MaxConcurrentInference:     defaultMaxConcurrentInference,
	}
}

// DefaultPath is ~/.config/appr-ai-sal/repo-context.json (or APPR_AI_SAL_CONFIG_DIR).
func DefaultPath() string {
	return filepath.Join(appdirs.ConfigDir(), "repo-context.json")
}

// boolPresence mirrors Config's boolean fields as pointers so JSON decoding
// can distinguish an omitted key (nil → keep the Default() value) from an
// explicit `false` (non-nil → honour it). This replaces the fragile
// bytes.Contains(`"field"`) raw-JSON scans that previously drove the same
// "present vs absent" logic and could be fooled by the key appearing inside a
// string value elsewhere in the file.
type boolPresence struct {
	IncludePRHistory           *bool `json:"include_pr_history"`
	RepoCultureSummarize       *bool `json:"repo_culture_summarize"`
	ContextVersusChangeSummary *bool `json:"context_versus_change_summary"`
	RepoExpertPanel            *bool `json:"repo_expert_panel"`
	ParallelSpecialists        *bool `json:"parallel_specialists"`
	ParallelRepoExperts        *bool `json:"parallel_repo_experts"`
	ParallelPRAgents           *bool `json:"parallel_pr_agents"`
	IncludeRepoEvidence        *bool `json:"include_repo_evidence"`
	RepoArbiterDemotions       *bool `json:"repo_arbiter_demotions"`
	ConventionWitness          *bool `json:"convention_witness"`
	TechAgents                 *bool `json:"tech_agents"`
	PRAgents                   *bool `json:"pr_agents"`
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

	// Bool fields need present-vs-absent detection so a default-true toggle
	// isn't forced false just because its key was omitted. Decode them as
	// pointers and only override the Default() value when the key was set.
	var bp boolPresence
	if err := json.Unmarshal(b, &bp); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	applyBoolPresence(c, &bp)
	c.Normalize()
	return c, nil
}

// applyBoolPresence overrides c's boolean fields with any explicitly-set
// values from bp. Absent keys (nil pointers) leave c's Default() value
// untouched, preserving the default-true / default-false behaviour the old
// bytes.Contains scans provided.
func applyBoolPresence(c *Config, bp *boolPresence) {
	if bp.IncludePRHistory != nil {
		c.IncludePRHistory = *bp.IncludePRHistory
	}
	if bp.RepoCultureSummarize != nil {
		c.RepoCultureSummarize = *bp.RepoCultureSummarize
	}
	if bp.ContextVersusChangeSummary != nil {
		c.ContextVersusChangeSummary = *bp.ContextVersusChangeSummary
	}
	if bp.RepoExpertPanel != nil {
		c.RepoExpertPanel = *bp.RepoExpertPanel
	}
	if bp.ParallelSpecialists != nil {
		c.ParallelSpecialists = *bp.ParallelSpecialists
	}
	if bp.ParallelRepoExperts != nil {
		c.ParallelRepoExperts = *bp.ParallelRepoExperts
	}
	if bp.ParallelPRAgents != nil {
		c.ParallelPRAgents = *bp.ParallelPRAgents
	}
	if bp.IncludeRepoEvidence != nil {
		c.IncludeRepoEvidence = *bp.IncludeRepoEvidence
	}
	if bp.RepoArbiterDemotions != nil {
		c.RepoArbiterDemotions = *bp.RepoArbiterDemotions
	}
	if bp.ConventionWitness != nil {
		c.ConventionWitness = *bp.ConventionWitness
	}
	if bp.TechAgents != nil {
		c.TechAgents = *bp.TechAgents
	}
	if bp.PRAgents != nil {
		c.PRAgents = *bp.PRAgents
	}
}

// ApplyParallelExecutionEnv overrides parallel_specialists /
// parallel_repo_experts / parallel_pr_agents when the matching
// APPR_AI_SAL_PARALLEL_SPECIALISTS / _REPO_EXPERTS / _PR_AGENTS env var is set
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
	if v, ok := os.LookupEnv("APPR_AI_SAL_PARALLEL_PR_AGENTS"); ok {
		c.ParallelPRAgents = parseBoolEnv(v)
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
	if o.MaxConcurrentInference > 0 {
		c.MaxConcurrentInference = o.MaxConcurrentInference
	}
	if len(o.DiffElisionGlobs) > 0 {
		c.DiffElisionGlobs = append([]string(nil), o.DiffElisionGlobs...)
	}
	if o.DiffByteCap > 0 {
		c.DiffByteCap = o.DiffByteCap
	}
	if o.DiffPerFileLineCap > 0 {
		c.DiffPerFileLineCap = o.DiffPerFileLineCap
	}
	// Copy non-zero values so a negative (explicit "disable") survives Merge —
	// the OrDefault accessors treat 0 as "use default" and <0 as "disabled".
	if o.MaxConsecutiveStageFailures != 0 {
		c.MaxConsecutiveStageFailures = o.MaxConsecutiveStageFailures
	}
	if o.RunWallClockCapSeconds != 0 {
		c.RunWallClockCapSeconds = o.RunWallClockCapSeconds
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
	// A missing key (zero) or a hostile <= 0 value resolves to the default so
	// the concurrency cap is never unlimited and can never size a semaphore at
	// zero (which would deadlock every inference call).
	if c.MaxConcurrentInference <= 0 {
		c.MaxConcurrentInference = defaultMaxConcurrentInference
	}
	// A hostile negative cap resolves to 0 (= "use the default") rather than
	// disabling the budget; the budgeter treats a non-positive cap as "apply
	// the conservative default", never as "unbounded".
	if c.DiffByteCap < 0 {
		c.DiffByteCap = 0
	}
	if c.DiffPerFileLineCap < 0 {
		c.DiffPerFileLineCap = 0
	}
	// Drop blank / whitespace-only globs so an empty override line can't turn
	// into a glob that matches every basename.
	if len(c.DiffElisionGlobs) > 0 {
		cleaned := make([]string, 0, len(c.DiffElisionGlobs))
		for _, g := range c.DiffElisionGlobs {
			if g = strings.TrimSpace(g); g != "" {
				cleaned = append(cleaned, g)
			}
		}
		c.DiffElisionGlobs = cleaned
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

// MaxConcurrentInferenceOrDefault returns the per-run inference concurrency
// cap, resolving a nil config or a non-positive value to the default (3). It
// never returns <= 0, so callers can size a semaphore with it directly.
func (c *Config) MaxConcurrentInferenceOrDefault() int {
	if c == nil || c.MaxConcurrentInference <= 0 {
		return defaultMaxConcurrentInference
	}
	return c.MaxConcurrentInference
}

// MaxConsecutiveStageFailuresOrDefault returns the circuit-breaker's
// consecutive-failure limit: the default (4) when unset (0), 0 (disabled) when
// configured negative, or the configured positive value. A returned 0 means
// "this arm of the breaker is off".
func (c *Config) MaxConsecutiveStageFailuresOrDefault() int {
	if c == nil || c.MaxConsecutiveStageFailures == 0 {
		return defaultMaxConsecutiveStageFailures
	}
	if c.MaxConsecutiveStageFailures < 0 {
		return 0 // disabled
	}
	return c.MaxConsecutiveStageFailures
}

// RunWallClockCap returns the whole-run wall-clock cap: the default (30m) when
// unset (0), 0 (disabled) when configured negative, or the configured positive
// value. A returned 0 duration means "no cap".
func (c *Config) RunWallClockCap() time.Duration {
	if c == nil || c.RunWallClockCapSeconds == 0 {
		return defaultRunWallClockCap
	}
	if c.RunWallClockCapSeconds < 0 {
		return 0 // disabled
	}
	return time.Duration(c.RunWallClockCapSeconds) * time.Second
}

// DiffElisionGlobsOrDefault returns the configured diff-elision globs, or the
// baked-in defaults when none are set. It never returns nil, so the budgeter
// can range over the result directly.
func (c *Config) DiffElisionGlobsOrDefault() []string {
	if c == nil || len(c.DiffElisionGlobs) == 0 {
		return DefaultDiffElisionGlobs()
	}
	return append([]string(nil), c.DiffElisionGlobs...)
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
