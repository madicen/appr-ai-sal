package settings

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/madicen/appr-ai-sal/internal/ai"
	"github.com/madicen/appr-ai-sal/internal/aiconfig"
	"github.com/madicen/appr-ai-sal/internal/tui/util/dropdown"
)

// modelsListedMsg carries the result of an async ListModels fetch back to the
// settings Update loop. forProfile guards against a stale response landing
// after the user switched profiles.
type modelsListedMsg struct {
	forProfile int
	models     []ai.ModelInfo
	err        error
}

// configFromProfile builds a throwaway *aiconfig.Config from a profile so the
// leaf ai package can resolve the base URL / key exactly as a review would.
func configFromProfile(p aiconfig.Profile) *aiconfig.Config {
	return &aiconfig.Config{
		Provider:        p.Provider,
		BaseURL:         p.BaseURL,
		Model:           p.Model,
		APIKey:          p.APIKey,
		APIKeyEnv:       p.APIKeyEnv,
		APIKeyCmd:       p.APIKeyCmd,
		AzureAPIVersion: p.AzureAPIVersion,
		TimeoutSec:      p.TimeoutSec,
	}
}

// fetchModelsCmd commits in-progress edits and kicks off an async model list
// for the edited profile's provider/base-URL/key. It is fail-open: on error
// the handler keeps the manual model input usable (see handleModelsListed).
func (m *Model) fetchModelsCmd() tea.Cmd {
	if m == nil || m.draft == nil {
		return nil
	}
	m.commitEditorToSelectedProfile()
	if m.selectedProfileIdx < 0 || m.selectedProfileIdx >= len(m.draft.Profiles) {
		return nil
	}
	cfg := configFromProfile(m.draft.Profiles[m.selectedProfileIdx])
	idx := m.selectedProfileIdx
	m.modelsLoading = true
	m.modelsErr = ""
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		models, err := ai.ListModels(ctx, cfg)
		return modelsListedMsg{forProfile: idx, models: models, err: err}
	}
}

// handleModelsListed applies a fetch result: on success it builds the model
// picker dropdown; on failure (or an empty list) it records a short message
// and keeps manual entry as the fallback. Stale results (a different profile
// is now selected) are ignored.
func (m *Model) handleModelsListed(msg modelsListedMsg) {
	m.modelsLoading = false
	if msg.forProfile != m.selectedProfileIdx {
		return
	}
	if msg.err != nil || len(msg.models) == 0 {
		m.modelDD = nil
		m.modelOptions = nil
		if msg.err != nil {
			m.modelsErr = msg.err.Error()
		} else {
			m.modelsErr = "no models returned"
		}
		return
	}
	m.modelOptions = make([]string, len(msg.models))
	labels := make([]string, len(msg.models))
	for i, mi := range msg.models {
		m.modelOptions[i] = mi.ID
		if mi.Name != "" && mi.Name != mi.ID {
			labels[i] = mi.Name + " — " + mi.ID
		} else {
			labels[i] = mi.ID
		}
	}
	dd := dropdown.New("model")
	dd.OnSelect = func(i int) tea.Cmd {
		if i >= 0 && i < len(m.modelOptions) {
			m.model.SetValue(m.modelOptions[i])
		}
		return nil
	}
	dd.Rebuild(labels, 0)
	dd.ContentTop = m.contentTop
	m.modelDD = dd
}

// clearModelList drops any fetched model picker. Called when the edited
// profile changes (the list is provider/base-URL-specific).
func (m *Model) clearModelList() {
	m.modelDD = nil
	m.modelOptions = nil
	m.modelsErr = ""
	m.modelsLoading = false
}
