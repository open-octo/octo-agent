package tools

import (
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
)

// The model-override description must list only the sibling models of the
// endpoint serving the session model, marking the endpoint's lite model.
func TestSubAgentModelParamDescFor_ListsSiblingModels(t *testing.T) {
	cfg := config.Config{
		Endpoints: []config.Endpoint{
			{
				ID:        "main",
				LiteModel: "cheap-model",
				Models: []config.EndpointModel{
					{Model: "big-model"},
					{Model: "cheap-model"},
				},
			},
			{
				ID:     "other",
				Models: []config.EndpointModel{{Model: "elsewhere-model"}},
			},
		},
	}

	desc := subAgentModelParamDescFor(cfg, "big-model")
	if !strings.Contains(desc, "big-model") || !strings.Contains(desc, "cheap-model (lite)") {
		t.Errorf("description missing sibling models: %q", desc)
	}
	if strings.Contains(desc, "elsewhere-model") {
		t.Errorf("description leaked another endpoint's model: %q", desc)
	}
}

func TestSubAgentModelParamDescFor_UnknownModelOmitsList(t *testing.T) {
	cfg := config.Config{
		Endpoints: []config.Endpoint{
			{ID: "main", Models: []config.EndpointModel{{Model: "big-model"}}},
		},
	}
	if got := subAgentModelParamDescFor(cfg, "adhoc-model"); got != subAgentModelParamBase {
		t.Errorf("unknown session model should fall back to the base description, got %q", got)
	}
}

// Definition (no session model) must still document the "lite" keyword.
func TestAgentToolDefinition_DocumentsLiteKeyword(t *testing.T) {
	def := AgentTool{}.Definition()
	props := def.Parameters["properties"].(map[string]any)
	modelDesc := props["model"].(map[string]any)["description"].(string)
	if !strings.Contains(modelDesc, "lite") {
		t.Errorf("model parameter description does not mention lite: %q", modelDesc)
	}
}
