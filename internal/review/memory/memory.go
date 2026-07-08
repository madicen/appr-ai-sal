// Package memory is appr-ai-sal's reviewer-memory store (B1, the "moat"
// feature): it persists, per repository, which review findings the reviewer
// posted, skipped, or reinstated, and lets the review pipeline learn from that
// signal across runs.
//
// The TUI already computes the perfect training signal every run (which
// findings were skipped, which arbiter-demoted findings the reviewer opted
// back in) and previously discarded it after each run. This package keeps it:
// each decision is folded into a per-repo JSON document under
// appdirs.CacheSubdir("repo-profiles") (i.e. review.RepoProfilesDir()), keyed
// by a privacy-preserving fingerprint of the finding.
//
// The store is:
//   - Local only. Nothing leaves the machine; the raw comment text is never
//     persisted — only a hash of a normalized form (see HashComment).
//   - Fail-open. A missing or corrupt document is treated as "no memory" so a
//     bad file can never break a review or the TUI. Callers log + continue.
//   - Concurrency-safe. Every mutation is a load-modify-save under a mutex,
//     and the write is atomic (temp-file + rename via agentstore).
//
// It is a leaf of the review subtree: it imports only the standard library,
// internal/appdirs, and internal/agentstore, so internal/review can depend on
// it without an import cycle (internal/review must NOT be imported here).
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madicen/appr-ai-sal/internal/agentstore"
)

// storeSubdir is the cache subdirectory the reviewer-memory documents live
// under. It is a child of the repo-profiles cache (review.RepoProfilesDir())
// so memory sits alongside the other per-repo caches, one file per repo.
const storeSubdir = "repo-profiles"

// storeFile is the per-repo document name inside <repo-profiles>/<owner>__<repo>/.
const storeFile = "reviewer-memory.json"

// DefaultSuppressThreshold is N in "N≥3 near-identical skips → suppress". The
// deterministic pre-arbiter suppressor only fires once the reviewer has
// skipped a matching pattern at least this many times. Deliberately
// conservative: three independent skips of the same pattern is a strong, and
// still-resurfaceable, signal that the pattern is noise in this repo.
const DefaultSuppressThreshold = 3

// arbiterMinCount is the minimum skip count for a pattern to appear in the
// arbiter's "previously rejected patterns" section. A single skip is weak
// signal (the reviewer may simply not have gotten to it); two or more is worth
// telling the arbiter about.
const arbiterMinCount = 2

// Decision is what the reviewer did with a finding at post time.
type Decision string

const (
	// DecisionPosted: the reviewer posted the finding (a positive/accept
	// signal). Recorded so a pattern the reviewer keeps posting is never
	// suppressed just because it was once skipped.
	DecisionPosted Decision = "posted"
	// DecisionSkipped: the reviewer skipped the finding (the core reject
	// signal that drives suppression and the arbiter section).
	DecisionSkipped Decision = "skipped"
	// DecisionDemoteReversed: the reviewer reinstated a finding the tool had
	// demoted/suppressed (arbiter demotion opted back in, or a
	// memory-suppressed finding resurfaced and posted). A strong "the tool
	// was wrong to hold this back" signal that counterbalances skips.
	DecisionDemoteReversed Decision = "demote_reversed"
)

func (d Decision) valid() bool {
	switch d {
	case DecisionPosted, DecisionSkipped, DecisionDemoteReversed:
		return true
	}
	return false
}

// Fingerprint is the privacy-preserving identity of a finding pattern. It
// deliberately generalizes the concrete finding so future, similar findings
// match a past decision:
//
//   - Specialist is the lane that filed it (lower-cased).
//   - PathGlob generalizes the concrete path to a directory + extension glob
//     (see PathGlob) so "internal/review/agents.go" and
//     "internal/review/runner.go" share one fingerprint.
//   - CommentHash is a hash of a NORMALIZED comment (see HashComment), never
//     the raw text. Near-identical comments (case / whitespace / punctuation /
//     word-order differences) collapse to the same hash — this is how
//     "near-identical" matching is achieved while keeping the store free of
//     the reviewer's comment text.
//   - Severity is the finding's severity (lower-cased).
//
// All fields are comparable, so Fingerprint equality is plain struct equality.
type Fingerprint struct {
	Specialist  string `json:"specialist"`
	PathGlob    string `json:"path_glob"`
	CommentHash string `json:"comment_hash"`
	Severity    string `json:"severity"`
}

// NewFingerprint builds a normalized Fingerprint from a concrete finding's
// fields. It is the single constructor so every call site generalizes the
// path and hashes the comment identically.
func NewFingerprint(specialist, pathConcrete, comment, severity string) Fingerprint {
	return Fingerprint{
		Specialist:  strings.ToLower(strings.TrimSpace(specialist)),
		PathGlob:    PathGlob(pathConcrete),
		CommentHash: HashComment(comment),
		Severity:    strings.ToLower(strings.TrimSpace(severity)),
	}
}

// PathGlob generalizes a concrete path into a directory + extension glob so a
// decision about one file transfers to sibling files of the same type:
//
//	internal/review/agents.go -> internal/review/*.go
//	README.md                 -> *.md
//	Makefile                  -> *
//	"" (PR-wide finding)      -> *
//
// The generalization is intentionally coarse-but-scoped: same directory, same
// extension. It never widens across directories, so a decision in one package
// does not silence findings in another.
func PathGlob(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" {
		return "*"
	}
	dir := path.Dir(p)
	ext := path.Ext(p)
	switch {
	case dir == "." || dir == "/" || dir == "":
		if ext == "" {
			return "*"
		}
		return "*" + ext
	default:
		if ext == "" {
			return dir + "/*"
		}
		return dir + "/*" + ext
	}
}

// HashComment returns a stable, privacy-preserving hash of comment. The
// comment is normalized to its lowercased, de-punctuated, de-duplicated,
// sorted word set before hashing (mirroring the finding-dedupe word-set
// tokenization), so trivially different phrasings of the same concern —
// different case, whitespace, punctuation, or word order — hash to the same
// value. Only the first 16 bytes of the digest are kept (32 hex chars): more
// than enough to avoid collisions while keeping the file small. An
// all-whitespace comment hashes to "" so PR-wide findings without prose don't
// all collapse onto one bogus fingerprint.
func HashComment(comment string) string {
	words := wordSet(comment)
	if len(words) == 0 {
		return ""
	}
	sort.Strings(words)
	sum := sha256.Sum256([]byte(strings.Join(words, " ")))
	return hex.EncodeToString(sum[:16])
}

// wordSet returns the sorted unique lowercased word tokens of s (letters and
// digits only), matching internal/review/finding_dedupe.go's tokenization so
// the two subsystems agree on what "the same words" means.
func wordSet(s string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !isWordRune(r)
	}) {
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	return out
}

func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// Record is one persisted decision-count for a fingerprint. Each
// (fingerprint, decision) pair has at most one Record; Count is how many times
// that decision was made and Last is the most recent time. This is exactly the
// schema in the improvement plan's B1 section.
type Record struct {
	Fingerprint Fingerprint `json:"fingerprint"`
	Decision    Decision    `json:"decision"`
	Count       int         `json:"count"`
	Last        time.Time   `json:"last"`
}

// Memory is one repository's whole reviewer-memory document.
type Memory struct {
	Owner   string   `json:"owner"`
	Repo    string   `json:"repo"`
	Records []Record `json:"records"`
}

// Entry is a single decision to fold into the store (one finding's outcome).
type Entry struct {
	Fingerprint Fingerprint
	Decision    Decision
}

// SkipCount returns how many times a finding matching fp was skipped.
func (m *Memory) SkipCount(fp Fingerprint) int {
	return m.count(fp, DecisionSkipped)
}

// PositiveCount returns how many times a finding matching fp was posted or
// reinstated (the accept signals that counterbalance skips).
func (m *Memory) PositiveCount(fp Fingerprint) int {
	return m.count(fp, DecisionPosted) + m.count(fp, DecisionDemoteReversed)
}

func (m *Memory) count(fp Fingerprint, d Decision) int {
	if m == nil {
		return 0
	}
	for _, r := range m.Records {
		if r.Fingerprint == fp && r.Decision == d {
			return r.Count
		}
	}
	return 0
}

// ShouldSuppress reports whether the deterministic pre-arbiter suppressor
// should hold a finding matching fp back. The rule is conservative: the
// pattern must have been skipped at least threshold times AND skipped strictly
// more often than it was posted/reinstated, so a pattern the reviewer
// sometimes keeps is never auto-suppressed. threshold <= 0 uses
// DefaultSuppressThreshold.
func (m *Memory) ShouldSuppress(fp Fingerprint, threshold int) bool {
	if m == nil {
		return false
	}
	if threshold <= 0 {
		threshold = DefaultSuppressThreshold
	}
	skips := m.SkipCount(fp)
	if skips < threshold {
		return false
	}
	return skips > m.PositiveCount(fp)
}

// RejectedPatterns returns the skipped-decision records worth telling the
// arbiter about (skip count >= arbiterMinCount), most-skipped first. These
// feed the "previously rejected patterns" section injected into the arbiter
// prompt. Returns nil when there is nothing worth reporting, so the arbiter
// prompt stays byte-identical to a no-memory run.
func (m *Memory) RejectedPatterns() []Record {
	if m == nil {
		return nil
	}
	var out []Record
	for _, r := range m.Records {
		if r.Decision == DecisionSkipped && r.Count >= arbiterMinCount {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Fingerprint.Specialist != out[j].Fingerprint.Specialist {
			return out[i].Fingerprint.Specialist < out[j].Fingerprint.Specialist
		}
		return out[i].Fingerprint.PathGlob < out[j].Fingerprint.PathGlob
	})
	return out
}

// NegativeExample is one exported real-world negative in the shape of an
// internal/evals ExpectFinding's must_not_appear entry. Pattern is left blank
// on purpose: the raw comment text is never persisted (privacy), so a
// maintainer curating the corpus fills it in from their own review history.
// The JSON tags match evals.ExpectFinding so the output pastes straight into a
// corpus case's expectations.json.
type NegativeExample struct {
	Specialist string `json:"specialist"`
	Path       string `json:"path"`
	Pattern    string `json:"pattern"`
	Note       string `json:"note"`
}

// ExportNegatives renders the repeatedly-skipped patterns as evals
// must_not_appear scaffolding (skip count >= arbiterMinCount). Path is left as
// the wildcard "" (the stored fingerprint is a glob, not an exact path) and
// Pattern blank for privacy; the Note carries the glob + count so a maintainer
// can complete the entry.
func (m *Memory) ExportNegatives() []NegativeExample {
	var out []NegativeExample
	for _, r := range m.RejectedPatterns() {
		out = append(out, NegativeExample{
			Specialist: r.Fingerprint.Specialist,
			Path:       "",
			Pattern:    "",
			Note: "reviewer skipped a matching finding " + strconv.Itoa(r.Count) + "× (path_glob=" +
				r.Fingerprint.PathGlob + ", severity=" + r.Fingerprint.Severity +
				"); fill in Pattern from the finding's comment before adding to the corpus",
		})
	}
	return out
}

// Store is the concurrency-safe, atomic reviewer-memory store. Construct it
// with NewStore. Every mutation is a load-modify-save guarded by mu so
// concurrent runs of the same process can't lose a decision, and the
// underlying write is atomic (temp + rename).
type Store struct {
	mu      sync.Mutex
	backing agentstore.Store[Memory]
}

// NewStore returns a Store backed by the repo-profiles cache directory.
func NewStore() *Store {
	return &Store{
		backing: agentstore.Store[Memory]{
			Subdir:   storeSubdir,
			FileName: storeFile,
			New:      func(owner, repo string) *Memory { return &Memory{Owner: owner, Repo: repo} },
			Clean:    cleanMemory,
		},
	}
}

// cleanMemory returns a normalized, marshal-safe copy of doc: owner/repo are
// backfilled from the arguments when blank, and records with an invalid
// decision or empty fingerprint are dropped. It never mutates the input.
func cleanMemory(doc *Memory, owner, repo string) *Memory {
	out := &Memory{Owner: owner, Repo: repo}
	if doc == nil {
		return out
	}
	if strings.TrimSpace(doc.Owner) != "" {
		out.Owner = strings.ToLower(strings.TrimSpace(doc.Owner))
	}
	if strings.TrimSpace(doc.Repo) != "" {
		out.Repo = strings.ToLower(strings.TrimSpace(doc.Repo))
	}
	for _, r := range doc.Records {
		if !r.Decision.valid() {
			continue
		}
		if r.Count <= 0 {
			continue
		}
		out.Records = append(out.Records, r)
	}
	return out
}

// FilePath returns the on-disk path of owner/repo's memory document (for
// diagnostics / the CLI).
func (s *Store) FilePath(owner, repo string) string {
	return s.backing.FilePath(owner, repo)
}

// Load reads owner/repo's memory. A missing file is not an error — an empty
// Memory is returned so first-run is a normal state.
func (s *Store) Load(owner, repo string) (*Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backing.Load(owner, repo)
}

// Record folds one decision into owner/repo's memory: it finds the existing
// (fingerprint, decision) record and increments its count (updating Last), or
// appends a new one. Invalid decisions are ignored. It is a no-op when the
// entry list is empty.
func (s *Store) Record(owner, repo string, entries ...Entry) error {
	valid := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Decision.valid() {
			valid = append(valid, e)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mem, err := s.backing.Load(owner, repo)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, e := range valid {
		foldEntry(mem, e, now)
	}
	return s.backing.Save(owner, repo, mem)
}

// foldEntry increments the matching record's count or appends a new one.
func foldEntry(mem *Memory, e Entry, now time.Time) {
	for i := range mem.Records {
		if mem.Records[i].Fingerprint == e.Fingerprint && mem.Records[i].Decision == e.Decision {
			mem.Records[i].Count++
			mem.Records[i].Last = now
			return
		}
	}
	mem.Records = append(mem.Records, Record{
		Fingerprint: e.Fingerprint,
		Decision:    e.Decision,
		Count:       1,
		Last:        now,
	})
}

// ListRepos returns the "owner/repo" keys that have a memory document, sorted.
func (s *Store) ListRepos() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backing.ListRepos()
}

// Matcher is a partial-fingerprint filter for CLI clear-by-pattern. Empty
// fields are wildcards; an all-empty Matcher matches nothing (so `clear`
// without any selector never silently wipes the repo — use Clear for that).
type Matcher struct {
	Specialist  string
	PathGlob    string
	Severity    string
	CommentHash string
}

// Empty reports whether the matcher has no constraints.
func (mt Matcher) Empty() bool {
	return strings.TrimSpace(mt.Specialist) == "" &&
		strings.TrimSpace(mt.PathGlob) == "" &&
		strings.TrimSpace(mt.Severity) == "" &&
		strings.TrimSpace(mt.CommentHash) == ""
}

func (mt Matcher) matches(fp Fingerprint) bool {
	if s := strings.ToLower(strings.TrimSpace(mt.Specialist)); s != "" && s != fp.Specialist {
		return false
	}
	if g := strings.TrimSpace(mt.PathGlob); g != "" && g != fp.PathGlob {
		return false
	}
	if v := strings.ToLower(strings.TrimSpace(mt.Severity)); v != "" && v != fp.Severity {
		return false
	}
	if h := strings.TrimSpace(mt.CommentHash); h != "" && h != fp.CommentHash {
		return false
	}
	return true
}

// ClearMatching removes every record whose fingerprint satisfies mt from
// owner/repo, returning the number removed. An empty matcher removes nothing.
// The document is deleted when it becomes empty.
func (s *Store) ClearMatching(owner, repo string, mt Matcher) (int, error) {
	if mt.Empty() {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mem, err := s.backing.Load(owner, repo)
	if err != nil {
		return 0, err
	}
	kept := mem.Records[:0:0]
	removed := 0
	for _, r := range mem.Records {
		if mt.matches(r.Fingerprint) {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	if removed == 0 {
		return 0, nil
	}
	mem.Records = kept
	if len(mem.Records) == 0 {
		return removed, s.backing.DeleteRepo(owner, repo)
	}
	return removed, s.backing.Save(owner, repo, mem)
}

// Clear removes owner/repo's entire memory document.
func (s *Store) Clear(owner, repo string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backing.DeleteRepo(owner, repo)
}

// ClearFingerprint removes every record matching fp from owner/repo's memory,
// leaving the rest intact. When the document becomes empty it is deleted.
// Returns the number of records removed.
func (s *Store) ClearFingerprint(owner, repo string, fp Fingerprint) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mem, err := s.backing.Load(owner, repo)
	if err != nil {
		return 0, err
	}
	kept := mem.Records[:0:0]
	removed := 0
	for _, r := range mem.Records {
		if r.Fingerprint == fp {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	if removed == 0 {
		return 0, nil
	}
	mem.Records = kept
	if len(mem.Records) == 0 {
		return removed, s.backing.DeleteRepo(owner, repo)
	}
	return removed, s.backing.Save(owner, repo, mem)
}
