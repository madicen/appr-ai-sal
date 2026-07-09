package evals

import (
	"context"
	"strings"

	"github.com/madicen/appr-ai-sal/internal/ai"
)

// ReplayProvider is a deterministic, offline ai.Provider that replays a case's
// canned model outputs (corpus/<id>/responses/) instead of calling a network
// backend. It is how the evals tests exercise the whole review pipeline —
// prompts, gates, arbiter, vibe-coach, scoring, report — with zero network and
// perfectly reproducible outputs.
//
// It routes a request to a canned response by the telemetry stage
// (applog.WithStage, surfaced on ai.Request.Stage): "specialist security" ->
// responses/security.json, "pr-agent checks" -> responses/checks.json,
// "repo-arbiter"/"vibe-coach"/"convention-witness" likewise. The hidden
// suggestion-repair pass shares a specialist's stage, so it is detected by its
// distinctive system prompt and always declined — the canned specialist output
// already encodes the model's final suggestions.
//
// It advertises diff-only capabilities (no repo tools), matching an HTTP
// backend, because the corpus responses are authored for the diff-only prompt
// shape most providers use.
type ReplayProvider struct {
	responses map[string]string
}

// NewReplayProvider builds a provider that replays the given case's responses.
func NewReplayProvider(c Case) *ReplayProvider {
	return &ReplayProvider{responses: c.Responses}
}

// repairPromptMarker is a stable phrase from review's suggestion-repair system
// prompt (repairSystemPrompt). A request carrying it is a repair call, which
// the replay provider always declines.
const repairPromptMarker = "precision patch writer"

func (p *ReplayProvider) Name() string { return "replay" }

func (p *ReplayProvider) Capabilities() ai.Capabilities {
	return ai.Capabilities{RepoTools: false, NativeJSON: false, Streaming: false}
}

func (p *ReplayProvider) Complete(_ context.Context, req ai.Request) (ai.Result, error) {
	text := p.respond(req)
	// Deterministic usage so token-cost scoring is exercised and reproducible:
	// ~4 chars/token, no cost (an Ollama-class local backend reports none).
	usage := ai.Usage{
		InputTokens:  (len(req.System) + len(req.User)) / 4,
		OutputTokens: len(text) / 4,
	}
	return ai.Result{Text: text, Usage: usage, Model: "replay"}, nil
}

// respond picks the canned output for a request.
func (p *ReplayProvider) respond(req ai.Request) string {
	if strings.Contains(req.System, repairPromptMarker) {
		return `{"repairs":[]}`
	}
	agent := agentFromStage(req.Stage)
	if body, ok := p.responses[agent]; ok && strings.TrimSpace(body) != "" {
		return body
	}
	return defaultResponse(agent)
}

// agentFromStage maps a telemetry stage label to the response key.
func agentFromStage(stage string) string {
	stage = strings.TrimSpace(stage)
	switch {
	case strings.HasPrefix(stage, "specialist "):
		return strings.TrimSpace(strings.TrimPrefix(stage, "specialist "))
	case strings.HasPrefix(stage, "pr-agent "):
		return strings.TrimSpace(strings.TrimPrefix(stage, "pr-agent "))
	default:
		return stage
	}
}

// defaultResponse is the benign, in-schema output for an agent with no canned
// response (a clean, finding-free pass).
func defaultResponse(agent string) string {
	switch agent {
	case "vibe-coach":
		return `{"verdict":"comment","summary":"No blocking issues.","prompts":[]}`
	case "repo-arbiter":
		return `{"user_summary":"","rationale_bullets":[],"verdict_override":"","summary_mode":"none","suppress":[],"demote":[]}`
	case "convention-witness":
		return `{"witnesses":[]}`
	default:
		return `{"summary":"No concerns in this specialty.","findings":[]}`
	}
}
