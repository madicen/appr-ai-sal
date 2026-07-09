// Package evals is the prompt-quality regression harness (Q4): the "quality
// flywheel" that keeps prompt changes (Q3) from landing blind.
//
// It loads a corpus of fixture PRs — a unified diff plus optional repo-context
// and brief blocks, with golden expectations — runs each through the REAL
// review pipeline (internal/review.EvalRun) against a pluggable provider, and
// scores the result: per-specialist precision / recall, anchor-hit rate,
// suggestion-survival rate, JSON-parse-first-try rate, and token cost. The
// scorer is pure and unit-tested; the runner reuses aiconfig / internal/ai so
// `make evals PROVIDER=ollama` selects a backend the same way a normal run
// does, and can A/B two prompt versions via the prompt-override mechanism.
//
// Tests never touch the network: they inject a deterministic ReplayProvider
// (replay.go) that returns each case's canned model output from
// corpus/<case>/responses/, so the scoring math and report generation are
// proven against fixed outputs.
package evals

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed all:corpus
var corpusFS embed.FS

// corpusRoot is the embedded directory holding every case subdirectory.
const corpusRoot = "corpus"

// CaseMeta is the corpus/<id>/case.json shape: the PR metadata plus which
// synthesis stages to run for this case. Every field is optional except id and
// target; sensible defaults apply.
type CaseMeta struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Target is the specialist / PR-agent this case primarily exercises
	// (documentary; used to prove corpus coverage).
	Target string `json:"target"`

	Repo         string `json:"repo"`
	Number       int    `json:"number"`
	Author       string `json:"author"`
	BaseRef      string `json:"base_ref"`
	HeadRef      string `json:"head_ref"`
	Body         string `json:"body"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changed_files"`

	// Strictness is the review strictness floor for the case ("balanced" when
	// empty). It is applied to the config the runner passes to EvalRun.
	Strictness string `json:"strictness"`
	// TechConfigured forces the tech specialist on (it otherwise only runs
	// when a tech brief is present). Auto-set true when the case ships a
	// tech.md.
	TechConfigured bool `json:"tech_configured"`

	RunPRAgents  bool `json:"run_pr_agents"`
	RunWitness   bool `json:"run_witness"`
	RunArbiter   bool `json:"run_arbiter"`
	RunVibeCoach bool `json:"run_vibe_coach"`
}

// Case is one fully-loaded corpus fixture.
type Case struct {
	Meta         CaseMeta
	Diff         string
	RepoContext  string
	TechSection  string
	LangSection  string
	Evidence     string
	Briefs       map[string]string // specialist -> brief markdown
	Expectations Expectations
	// Responses maps an agent name (specialist / pr-agent / "vibe-coach" /
	// "repo-arbiter" / "convention-witness") to the canned raw model output
	// the ReplayProvider replays. Empty for cases meant to run only against a
	// live provider.
	Responses map[string]string
}

// LoadCorpus reads every embedded case, sorted by id for deterministic
// ordering. An unreadable / malformed case fails the whole load so a broken
// fixture can never silently drop out of the suite.
func LoadCorpus() ([]Case, error) {
	entries, err := fs.ReadDir(corpusFS, corpusRoot)
	if err != nil {
		return nil, fmt.Errorf("read corpus: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	cases := make([]Case, 0, len(ids))
	for _, id := range ids {
		c, err := loadCase(path.Join(corpusRoot, id))
		if err != nil {
			return nil, fmt.Errorf("load case %q: %w", id, err)
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("corpus is empty")
	}
	return cases, nil
}

func loadCase(dir string) (Case, error) {
	var c Case

	metaRaw, err := fs.ReadFile(corpusFS, path.Join(dir, "case.json"))
	if err != nil {
		return c, fmt.Errorf("read case.json: %w", err)
	}
	if err := json.Unmarshal(metaRaw, &c.Meta); err != nil {
		return c, fmt.Errorf("parse case.json: %w", err)
	}
	if strings.TrimSpace(c.Meta.ID) == "" {
		c.Meta.ID = path.Base(dir)
	}

	diff, err := fs.ReadFile(corpusFS, path.Join(dir, "diff.patch"))
	if err != nil {
		return c, fmt.Errorf("read diff.patch: %w", err)
	}
	c.Diff = string(diff)

	// Optional single-file brief blocks.
	c.RepoContext = optionalFile(dir, "repo-context.md")
	c.TechSection = optionalFile(dir, "tech.md")
	c.LangSection = optionalFile(dir, "lang.md")
	c.Evidence = optionalFile(dir, "evidence.md")
	if strings.TrimSpace(c.TechSection) != "" {
		c.Meta.TechConfigured = true
	}

	// Optional per-specialist briefs.
	c.Briefs = readDirMap(path.Join(dir, "briefs"))
	// Canned responses for the ReplayProvider.
	c.Responses = readDirMap(path.Join(dir, "responses"))

	expRaw, err := fs.ReadFile(corpusFS, path.Join(dir, "expectations.json"))
	if err != nil {
		return c, fmt.Errorf("read expectations.json: %w", err)
	}
	if err := json.Unmarshal(expRaw, &c.Expectations); err != nil {
		return c, fmt.Errorf("parse expectations.json: %w", err)
	}
	return c, nil
}

// optionalFile returns the trimmed contents of dir/name, or "" when absent.
func optionalFile(dir, name string) string {
	b, err := fs.ReadFile(corpusFS, path.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(b)
}

// readDirMap reads every regular file in dir into a map keyed by the file name
// with its extension stripped (so briefs/security.md -> "security",
// responses/vibe-coach.json -> "vibe-coach"). Missing dir yields an empty map.
func readDirMap(dir string) map[string]string {
	entries, err := fs.ReadDir(corpusFS, dir)
	if err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(corpusFS, path.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		key := strings.TrimSuffix(e.Name(), path.Ext(e.Name()))
		out[key] = string(b)
	}
	return out
}
