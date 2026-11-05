package dsh

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const deepSeekRoute = "deepseek-official"

// ModelRoute names a model in the Gateway catalog and the exact DSH adapter
// route behind it. Credentials stay in DSH's native credential store. The
// Gateway neither imports CLI tokens nor falls back to an API-key route.
type ModelRoute struct {
	ID       string   `json:"id"`
	Name     string   `json:"name,omitempty"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Efforts  []string `json:"efforts,omitempty"`
}

var routePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func modelRoutes(cfg Config) (map[string]ModelRoute, error) {
	routes := map[string]ModelRoute{}
	for _, id := range []string{DefaultModel, "deepseek-v4-pro"} {
		routes[id] = ModelRoute{ID: id, Name: id, Provider: deepSeekRoute, Model: id}
	}
	for _, r := range cfg.Models {
		if !providers.ValidModelID(r.ID) || !providers.ValidModelID(r.Model) || !routePattern.MatchString(r.Provider) {
			return nil, fmt.Errorf("invalid DSH model route %q", r.ID)
		}
		if _, exists := routes[r.ID]; exists {
			return nil, fmt.Errorf("duplicate DSH model route %q", r.ID)
		}
		if r.Name == "" {
			r.Name = r.ID
		}
		for _, effort := range r.Efforts {
			if !providers.ValidEffortID(effort) {
				return nil, fmt.Errorf("invalid effort for DSH model %q", r.ID)
			}
		}
		routes[r.ID] = r
	}
	// Existing configurations used dshModel for an unlisted DeepSeek model.
	if _, ok := routes[cfg.Model]; !ok {
		if !providers.ValidModelID(cfg.Model) || !strings.HasPrefix(cfg.Model, "deepseek-") {
			return nil, fmt.Errorf("default DSH model %q needs a dshModels route", cfg.Model)
		}
		routes[cfg.Model] = ModelRoute{ID: cfg.Model, Name: cfg.Model, Provider: deepSeekRoute, Model: cfg.Model}
	}
	return routes, nil
}

func (p *Provider) selectModel(s *session, id string) error {
	r, ok := p.routes[id]
	if !ok {
		// Preserve the pre-catalog API's explicit DeepSeek model ids only.
		if !providers.ValidModelID(id) || !strings.HasPrefix(id, "deepseek-") {
			return fmt.Errorf("DSH model %q needs a dshModels route", id)
		}
		r = ModelRoute{ID: id, Provider: deepSeekRoute, Model: id}
	}
	s.Summary.Model = id
	s.Route = r.Provider
	s.NativeModel = r.Model
	return nil
}
