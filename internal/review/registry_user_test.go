package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/gh"
)

// rebuildRegistry rebuilds the process registry from the built-ins plus any
// user specs under the current APPR_AI_SAL_CONFIG_DIR, and restores the
// built-ins-only registry on test cleanup. It consumes the sync.Once so a
// later getRegistry call cannot rebuild over the test's registry.
func rebuildRegistry(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	registryOnce.Do(func() {})
	liveRegistry = buildRegistry(loadUserSpecialists())
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		liveRegistry = buildRegistry(nil)
		registryMu.Unlock()
	})
}

// useUserSpecialistsDir points the config dir at a temp dir with the given
// specialists/ files already written, then rebuilds the registry. Each file is
// name→content; a "<name>.json"/"<name>.md" pair defines a user specialist.
func useUserSpecialistsDir(t *testing.T, files map[string]string) string {
	t.Helper()
	cfgDir := t.TempDir()
	specDir := filepath.Join(cfgDir, UserSpecialistsSubdir)
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir specialists: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(specDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", cfgDir)
	rebuildRegistry(t)
	return cfgDir
}

const perfSpecJSON = `{
  "name": "performance",
  "kind": "code",
  "inputs": ["diff"],
  "gates": [],
  "lane_priority": 40,
  "arbiter_policy": { "suppressible": true, "demotable": true },
  "witnessable": false,
  "severity_ladder": "warning: an avoidable allocation on a hot path."
}`

const perfSpecPrompt = "You are the performance specialist. SENTINEL-PERF-PROMPT."

// jsonProvider is a fake ai.Provider that returns a fixed completion string,
// so a test can drive the specialist pipeline end-to-end without a real backend.
type jsonProvider struct{ out string }

func (jsonProvider) Name() string                  { return "fake-json" }
func (jsonProvider) Capabilities() ai.Capabilities { return ai.Capabilities{} }
func (p jsonProvider) Complete(context.Context, ai.Request) (ai.Result, error) {
	return ai.Result{Text: p.out}, nil
}

// TestUserSpecialistRegistersAndRunsEndToEnd is the Q1 acceptance test #2: a
// custom specialist is loaded from a .md+.json pair, runs through the real
// specialist pipeline against a fake provider, and its finding flows through
// the gates, cross-specialist dedupe, and the repo arbiter exactly as its spec
// declares (suppressible=true, lane_priority=40).
func TestUserSpecialistRegistersAndRunsEndToEnd(t *testing.T) {
	useUserSpecialistsDir(t, map[string]string{
		"performance.json": perfSpecJSON,
		"performance.md":   perfSpecPrompt,
	})

	// The spec is registered as a user-defined code specialist.
	spec, ok := lookupSpec("performance")
	if !ok {
		t.Fatal("performance spec not registered")
	}
	if !spec.userDefined || spec.Kind != KindCode || spec.LanePriority != 40 {
		t.Fatalf("unexpected spec: %+v", spec)
	}
	if !spec.ArbiterPolicy.Suppressible || !spec.ArbiterPolicy.Demotable {
		t.Fatalf("performance should be suppressible+demotable per its json")
	}

	// It joins the active code panel (a user code spec runs on every review).
	active := ActiveSpecialists(false)
	if !contains(active, "performance") {
		t.Fatalf("ActiveSpecialists should include performance; got %v", active)
	}

	// Its prompt is the .md, with the severity ladder appended.
	prompt, err := SpecialistPrompt("performance")
	if err != nil {
		t.Fatalf("SpecialistPrompt(performance): %v", err)
	}
	if !strings.Contains(prompt, "SENTINEL-PERF-PROMPT") {
		t.Fatalf("prompt missing sentinel: %q", prompt)
	}
	if !strings.Contains(prompt, "avoidable allocation on a hot path") {
		t.Fatalf("prompt missing appended severity ladder: %q", prompt)
	}

	// Run it through the real specialist pipeline against a fake provider that
	// returns one warning finding anchored inline.
	restore := ai.SetBaseProviderForTest(func(*aiconfig.Config) (ai.Provider, error) {
		return jsonProvider{out: `{"summary":"one hot-path allocation","findings":[{"path":"main.go","line":10,"side":"RIGHT","severity":"warning","comment":"This allocates inside the request loop; hoist it out."}]}`}, nil
	})
	defer restore()

	cfg := aiconfig.DefaultConfig()
	cfg.Provider = aiconfig.ProviderClaude // arbitrary; the fake hook intercepts
	cfg.ReviewStrictness = aiconfig.ReviewBalanced
	pr := &gh.PR{Repository: "o/r", Number: 1, Title: "t", Author: "a", BaseRef: "main", HeadRef: "feat"}

	res := runReviewSpecialist(context.Background(), cfg, "performance", "", pr, "", "", "", "", nil, "", "", "")
	if res.Err != nil {
		t.Fatalf("performance specialist errored: %v", res.Err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Severity != SeverityWarning {
		t.Fatalf("expected one warning finding to survive the gates, got %+v", res.Findings)
	}

	// Dedupe: when the custom finding collides on a line with a security
	// finding of the same concern, the registry lane priority (security=0 <
	// performance=40) must keep security — proving the user spec's lane
	// priority is consulted.
	perf := res
	sec := SpecialistResult{Specialist: SpecSecurity, Findings: []Finding{
		{Path: "main.go", Line: 10, Side: "RIGHT", Severity: SeverityWarning, Comment: "This allocates inside the request loop; hoist it out."},
	}}
	merged := dedupeInlineFindingsAcrossSpecialists([]SpecialistResult{perf, sec})
	if len(merged[0].Findings) != 0 || len(merged[1].Findings) != 1 {
		t.Fatalf("security (lane 0) should win the dedupe over performance (lane 40); got perf=%d sec=%d",
			len(merged[0].Findings), len(merged[1].Findings))
	}

	// Arbiter: the custom finding is suppressible per its spec.
	d := &Draft{Specialists: []SpecialistResult{
		{Specialist: "performance", Findings: []Finding{
			{Path: "svc.go", Line: 3, Side: "RIGHT", Severity: SeverityWarning, Comment: "hot-path alloc"},
		}},
	}}
	ar := &RepoArbiterResult{Suppressed: []SuppressedFindingRef{
		{Specialist: "performance", Path: "svc.go", Line: 3, Side: "RIGHT", Reason: "acceptable here"},
	}}
	FinalizeRepoArbiter(ar, d)
	if len(ar.suppressKeySet) != 1 {
		t.Fatalf("performance finding should be suppressible per its spec; dropped: %v", ar.DroppedSuppressions)
	}
}

// TestUserSpecialistFailOpen exercises the fail-open loader: malformed JSON, a
// missing prompt file, and a name that collides with a built-in are each
// skipped without registering (and without crashing), while a valid spec in the
// same directory still loads.
func TestUserSpecialistFailOpen(t *testing.T) {
	useUserSpecialistsDir(t, map[string]string{
		// Malformed JSON — skipped.
		"broken.json": `{ this is not json `,
		"broken.md":   "x",
		// Missing prompt file (no good.md) — skipped.
		"good.json": `{"name":"good","kind":"code"}`,
		// Collides with a built-in — must never shadow it.
		"security.json": `{"name":"security","kind":"code"}`,
		"security.md":   "malicious override attempt",
		// A valid custom spec in the same dir still loads.
		"i18n.json": `{"name":"i18n","kind":"code","arbiter_policy":{"suppressible":true,"demotable":true}}`,
		"i18n.md":   "You are the i18n specialist.",
	})

	if _, ok := lookupSpec("broken"); ok {
		t.Error("malformed spec should not register")
	}
	if _, ok := lookupSpec("good"); ok {
		t.Error("spec with missing prompt should not register")
	}
	// The built-in security spec is intact (not shadowed by the user file).
	sec, ok := lookupSpec("security")
	if !ok || sec.userDefined {
		t.Errorf("security must remain the built-in spec, got ok=%v userDefined=%v", ok, sec.userDefined)
	}
	if specSuppressible("security") {
		t.Error("built-in security must still be non-suppressible after a shadow attempt")
	}
	// The valid custom spec loaded fine.
	if _, ok := lookupSpec("i18n"); !ok {
		t.Error("valid i18n spec should have loaded alongside the bad ones")
	}
}

// TestNoUserSpecialistsDirIsClean confirms that with no specialists directory
// the registry is exactly the built-ins (the common case) — no error, no extra
// lanes.
func TestNoUserSpecialistsDirIsClean(t *testing.T) {
	t.Setenv("APPR_AI_SAL_CONFIG_DIR", t.TempDir())
	rebuildRegistry(t)
	if got := len(ActiveSpecialists(true)); got != len(AllSpecialists) {
		t.Fatalf("with no user specs, active set should equal built-ins (%d); got %d", len(AllSpecialists), got)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
