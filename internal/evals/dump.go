package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/review"
)

// dumpRecord is the per-agent file written under --dump-raw
// (DIR/<case-id>/<agent>.json). Header fields sit alongside the raw model
// text and the post-gate findings so an audit can open one file per agent.
type dumpRecord struct {
	Provider     string           `json:"provider"`
	Model        string           `json:"model"`
	Strictness   string           `json:"strictness"`
	JSONFirstTry bool             `json:"json_first_try"`
	Raw          string           `json:"raw"`
	Findings     []review.Finding `json:"findings"`
}

// writeCaseDumps writes one JSON file per agent observation under
// dir/<case-id>/<agent>.json. dir empty is a no-op.
func writeCaseDumps(dir string, cfg *aiconfig.Config, c Case, obs review.EvalObservation) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	caseDir := filepath.Join(dir, c.Meta.ID)
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", caseDir, err)
	}
	provider := string(cfg.Provider)
	model := cfg.AIModelOrDefault()
	strictness := string(cfg.ReviewStrictness)
	if strictness == "" {
		strictness = string(aiconfig.ReviewBalanced)
	}
	for _, a := range obs.Agents {
		rec := dumpRecord{
			Provider:     provider,
			Model:        model,
			Strictness:   strictness,
			JSONFirstTry: a.ParsedOK,
			Raw:          a.RawText,
			Findings:     a.Findings,
		}
		if rec.Findings == nil {
			rec.Findings = []review.Finding{}
		}
		body, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal dump %s/%s: %w", c.Meta.ID, a.Agent, err)
		}
		path := filepath.Join(caseDir, a.Agent+".json")
		if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
