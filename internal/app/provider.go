// Package app defines the provider-vendor registry and protocol mapping.
//
// A "provider" here is a business-level vendor (kimi, deepseek, openai,
// anthropic, …), not a wire protocol.  Each vendor carries:
//   - the wire protocol it speaks (anthropic-messages or openai-completions)
//   - its default endpoint, default model, and API-key env var
//
// CLI, server, and Web UI all resolve through this single registry so a new
// vendor only needs one line here.
package app

import "strings"

// EndpointVariant is a regional endpoint alternative for a vendor.
type EndpointVariant struct {
	Label    string `json:"label"`
	LabelKey string `json:"label_key,omitempty"`
	BaseURL  string `json:"base_url"`
	Region   string `json:"region,omitempty"`
}

// VendorModel is one catalogue entry: the model id plus whether it accepts
// image input. Vision is authoritative for predefined vendors — the value is
// copied onto the model entry when the user picks it, so the capability is
// recorded rather than re-guessed from the id at runtime.
type VendorModel struct {
	ID     string
	Vision bool
}

// Vendor is everything needed to bootstrap a provider client and render it in
type Vendor struct {
	ID               string            // canonical identifier, e.g. "kimi"
	DisplayName      string            // human label, e.g. "Kimi (Moonshot)"
	Protocol         string            // "anthropic" or "openai"
	API              string            // "anthropic-messages" or "openai-completions"
	DefaultBaseURL   string            // vendor's official endpoint (host only; the client appends the protocol path)
	DefaultModel     string            // cheapest/reasoning-capable default
	Models           []VendorModel     // available models (for UI dropdown), each with its vision capability
	LiteModel        string            // lightweight/cheaper model variant
	APIKeyEnvVar     string            // environment variable name for the key
	WebsiteURL       string            // link to the key-management page
	EndpointVariants []EndpointVariant // regional endpoint alternatives
	// CustomEndpoint marks a vendor with no fixed endpoint: the user must
	// supply a base URL. Vendors without it are pinned to DefaultBaseURL
	// (plus EndpointVariants) — they don't take arbitrary URLs.
	CustomEndpoint bool
}

// Registry is the ordered list of supported vendors.  The order is the
// display order in the Web UI onboarding dropdown.
var Registry = []Vendor{
	{
		ID:             "openai",
		DisplayName:    "OpenAI",
		Protocol:       "openai",
		API:            "openai-completions",
		DefaultBaseURL: "https://api.openai.com",
		DefaultModel:   "gpt-5.4",
		Models: []VendorModel{
			{ID: "gpt-5.6-sol", Vision: true},
			{ID: "gpt-5.6-terra", Vision: true},
			{ID: "gpt-5.6-luna", Vision: true},
			{ID: "gpt-5.5", Vision: true},
			{ID: "gpt-5.4", Vision: true},
			{ID: "gpt-5.4-mini", Vision: true},
			{ID: "gpt-5.4-nano", Vision: true},
			{ID: "gpt-4.1", Vision: true},
			{ID: "gpt-4.1-mini", Vision: true},
			{ID: "gpt-4.1-nano", Vision: true},
			{ID: "o3", Vision: true},
			{ID: "o3-mini", Vision: false}, // o3-mini has no image input (unlike o3 / o4-mini)
			{ID: "o4-mini", Vision: true},
		},
		LiteModel:    "gpt-5.4-mini",
		APIKeyEnvVar: "OPENAI_API_KEY",
		WebsiteURL:   "https://platform.openai.com/api-keys",
	},
	{
		ID:             "anthropic",
		DisplayName:    "Anthropic",
		Protocol:       "anthropic",
		API:            "anthropic-messages",
		DefaultBaseURL: "https://api.anthropic.com",
		DefaultModel:   "claude-sonnet-4-6",
		Models: []VendorModel{
			{ID: "claude-fable-5", Vision: true},
			{ID: "claude-opus-4-8", Vision: true},
			{ID: "claude-opus-4-7", Vision: true},
			{ID: "claude-opus-4-6", Vision: true},
			{ID: "claude-sonnet-5", Vision: true},
			{ID: "claude-sonnet-4-6", Vision: true},
			{ID: "claude-sonnet-4-5", Vision: true},
			{ID: "claude-haiku-4-5", Vision: true},
		},
		LiteModel:    "claude-haiku-4-5",
		APIKeyEnvVar: "ANTHROPIC_API_KEY",
		WebsiteURL:   "https://console.anthropic.com/settings/keys",
	},
	{
		ID:             "openrouter",
		DisplayName:    "OpenRouter",
		Protocol:       "openai",
		API:            "openai-completions",
		DefaultBaseURL: "https://openrouter.ai/api",
		DefaultModel:   "anthropic/claude-sonnet-4-6",
		Models: []VendorModel{
			{ID: "anthropic/claude-sonnet-4-6", Vision: true},
			{ID: "anthropic/claude-opus-4-8", Vision: true},
			{ID: "anthropic/claude-opus-4-7", Vision: true},
			{ID: "anthropic/claude-opus-4-6", Vision: true},
			{ID: "anthropic/claude-haiku-4-5", Vision: true},
			{ID: "openai/gpt-5.5", Vision: true},
			{ID: "openai/gpt-5.4", Vision: true},
			{ID: "openai/gpt-5.4-mini", Vision: true},
			{ID: "google/gemini-2.5-pro", Vision: true},
			{ID: "google/gemini-2.5-flash", Vision: true},
			{ID: "meta-llama/llama-3.3-70b-instruct", Vision: false}, // Llama 3.3 70B is text-only
			{ID: "x-ai/grok-4.3", Vision: true},
		},
		APIKeyEnvVar: "OPENROUTER_API_KEY",
		WebsiteURL:   "https://openrouter.ai/keys",
	},
	{
		ID:             "deepseek",
		DisplayName:    "DeepSeek",
		Protocol:       "openai",
		API:            "openai-completions",
		DefaultBaseURL: "https://api.deepseek.com",
		DefaultModel:   "deepseek-v4-pro",
		// DeepSeek V4 has vision in the chat app but not over the API — image
		// content isn't accepted on the endpoint, so treat as text-only here.
		Models: []VendorModel{
			{ID: "deepseek-v4-flash", Vision: false},
			{ID: "deepseek-v4-pro", Vision: false},
		},
		LiteModel:    "deepseek-v4-flash",
		APIKeyEnvVar: "DEEPSEEK_API_KEY",
		WebsiteURL:   "https://platform.deepseek.com/api_keys",
	},
	{
		ID:             "minimax",
		DisplayName:    "Minimax",
		Protocol:       "openai",
		API:            "openai-completions",
		DefaultBaseURL: "https://api.minimaxi.com",
		DefaultModel:   "MiniMax-M3",
		Models: []VendorModel{
			{ID: "MiniMax-M2.5", Vision: false}, // text-only generation
			{ID: "MiniMax-M2.7", Vision: false}, // text-only (image input not supported on the API)
			{ID: "MiniMax-M3", Vision: true},    // multimodal: text/image/video
		},
		APIKeyEnvVar: "MINIMAX_API_KEY",
		WebsiteURL:   "https://www.minimaxi.com/user-center/basic-information/interface-key",
		EndpointVariants: []EndpointVariant{
			{Label: "Mainland China", LabelKey: "settings.models.baseurl.variant.mainland_cn", BaseURL: "https://api.minimaxi.com", Region: "cn"},
			{Label: "International", LabelKey: "settings.models.baseurl.variant.international", BaseURL: "https://api.minimax.io", Region: "intl"},
		},
	},
	{
		ID:             "kimi",
		DisplayName:    "Kimi (Moonshot)",
		Protocol:       "openai",
		API:            "openai-completions",
		DefaultBaseURL: "https://api.moonshot.cn",
		DefaultModel:   "kimi-k2.6",
		// Kimi K2 family is natively multimodal (image input via MoonViT).
		Models: []VendorModel{
			{ID: "kimi-k2.6", Vision: true},
			{ID: "kimi-k2.7-code", Vision: true},
		},
		APIKeyEnvVar: "MOONSHOT_API_KEY",
		WebsiteURL:   "https://platform.moonshot.cn/console/api-keys",
		EndpointVariants: []EndpointVariant{
			{Label: "Mainland China", LabelKey: "settings.models.baseurl.variant.mainland_cn", BaseURL: "https://api.moonshot.cn", Region: "cn"},
			{Label: "International", LabelKey: "settings.models.baseurl.variant.international", BaseURL: "https://api.moonshot.ai", Region: "intl"},
		},
	},
	{
		ID:             "kimi-coding-plan",
		DisplayName:    "Kimi Coding Plan",
		Protocol:       "anthropic",
		API:            "anthropic-messages",
		DefaultBaseURL: "https://api.kimi.com/coding",
		DefaultModel:   "kimi-for-coding",
		Models: []VendorModel{
			{ID: "kimi-for-coding", Vision: true},
			{ID: "kimi-for-coding-highspeed", Vision: true},
			{ID: "k3", Vision: true},
			{ID: "kimi-k2.6", Vision: true},
			{ID: "kimi-k2.7-code", Vision: true},
		},
		APIKeyEnvVar: "MOONSHOT_API_KEY",
		WebsiteURL:   "https://platform.moonshot.cn/console/api-keys",
	},
	{
		ID:             "glm",
		DisplayName:    "GLM (Zhipu)",
		Protocol:       "openai",
		API:            "openai-completions",
		DefaultBaseURL: "https://open.bigmodel.cn/api/paas/v4",
		DefaultModel:   "glm-4.5",
		// GLM-4.5 line is text-only; the vision variant (GLM-4.5V) isn't offered here.
		Models: []VendorModel{
			{ID: "glm-4.5", Vision: false},
			{ID: "glm-4.5-air", Vision: false},
			{ID: "glm-4.5-flash", Vision: false},
		},
		LiteModel:    "glm-4.5-flash",
		APIKeyEnvVar: "ZHIPU_API_KEY",
		WebsiteURL:   "https://open.bigmodel.cn/usercenter/apikey",
		EndpointVariants: []EndpointVariant{
			{Label: "Mainland China", LabelKey: "settings.models.baseurl.variant.mainland_cn", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Region: "cn"},
			{Label: "International", LabelKey: "settings.models.baseurl.variant.international", BaseURL: "https://api.z.ai", Region: "intl"},
		},
	},
	{
		ID:             "bailian",
		DisplayName:    "Bailian (Alibaba)",
		Protocol:       "openai",
		API:            "openai-completions",
		DefaultBaseURL: "https://dashscope.aliyuncs.com/compatible-mode",
		DefaultModel:   "qwen3.7-plus",
		// qwen3.7-plus and the flash generations accept image input; qwen3.7-max
		// and the legacy qwen-plus alias are text-only.
		Models: []VendorModel{
			{ID: "qwen3.7-max", Vision: false},
			{ID: "qwen3.7-plus", Vision: true},
			{ID: "qwen3.6-flash", Vision: true},
			{ID: "qwen3.5-flash", Vision: true},
			{ID: "qwen-plus", Vision: false},
		},
		LiteModel:    "qwen3.5-flash",
		APIKeyEnvVar: "DASHSCOPE_API_KEY",
		WebsiteURL:   "https://bailian.console.aliyun.com",
		EndpointVariants: []EndpointVariant{
			{Label: "Mainland China", LabelKey: "settings.models.baseurl.variant.mainland_cn", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode", Region: "cn"},
			{Label: "International", LabelKey: "settings.models.baseurl.variant.international", BaseURL: "https://dashscope-us.aliyuncs.com/compatible-mode", Region: "intl"},
		},
	},
	{
		ID:             "mimo",
		DisplayName:    "MiMo (Xiaomi)",
		Protocol:       "openai",
		API:            "openai-completions",
		DefaultBaseURL: "https://api.xiaomimimo.com",
		DefaultModel:   "mimo-v2.5-pro",
		// MiMo-V2.5 is a native omnimodal model (image/video/audio input).
		Models: []VendorModel{
			{ID: "mimo-v2.5-pro", Vision: true},
			{ID: "mimo-v2.5-flash", Vision: true},
		},
		APIKeyEnvVar: "MIMO_API_KEY",
		WebsiteURL:   "https://platform.xiaomimimo.com/#/console/api-keys",
	},
	{
		ID:             "longcat",
		DisplayName:    "LongCat (Meituan)",
		Protocol:       "openai",
		API:            "openai-completions",
		DefaultBaseURL: "https://api.longcat.chat/openai",
		DefaultModel:   "LongCat-2.0",
		// LongCat-2.0 accepts text input only (no image/vision support on the API).
		Models: []VendorModel{
			{ID: "LongCat-2.0", Vision: false},
		},
		APIKeyEnvVar: "LONGCAT_API_KEY",
		WebsiteURL:   "https://longcat.chat/platform/api_keys",
	},
	{
		ID:          ProviderCustom,
		DisplayName: "Custom",
		// Protocol is empty: a Custom endpoint has no fixed wire format, so the
		// user picks "anthropic" or "openai" at config time and it is stored on
		// the model entry (config.ModelEntry.Protocol), not here.
		Protocol:       "",
		API:            "",
		APIKeyEnvVar:   "CUSTOM_API_KEY",
		CustomEndpoint: true,
	},
}

// ProviderCustom is the catch-all vendor for self-hosted / third-party
// endpoints. It is the only vendor that accepts a free-form base URL
// (CustomEndpoint) and the only one whose wire protocol is chosen per config
// entry rather than pinned in the registry — a base URL and a protocol are
// both required to build its client.
const ProviderCustom = "custom"

// VendorCustomEndpoint reports whether the vendor has no fixed endpoint and
// requires a user-supplied base URL.
func VendorCustomEndpoint(id string) bool {
	if v := vendorByID(id); v != nil {
		return v.CustomEndpoint
	}
	return false
}

// VendorNeedsProtocol reports whether the vendor has no fixed wire protocol, so
// the caller must supply one ("anthropic" or "openai") from the model entry.
// Only the Custom catch-all needs this; every named vendor pins its protocol.
func VendorNeedsProtocol(id string) bool {
	if v := vendorByID(id); v != nil {
		return v.Protocol == ""
	}
	return false
}

// vendorByID returns the vendor with the given ID, or nil if unknown.
func vendorByID(id string) *Vendor {
	for i := range Registry {
		if Registry[i].ID == id {
			return &Registry[i]
		}
	}
	return nil
}

// VendorBaseURL returns the default base URL for a vendor ID, or "" if unknown.
func VendorBaseURL(id string) string {
	if v := vendorByID(id); v != nil {
		return v.DefaultBaseURL
	}
	return ""
}

// VendorProtocol returns "anthropic" or "openai" for a vendor ID, or "" if unknown.
func VendorProtocol(id string) string {
	if v := vendorByID(id); v != nil {
		return v.Protocol
	}
	return ""
}

// VendorDefaultModel returns the built-in default model for a vendor ID.
func VendorDefaultModel(id string) string {
	if v := vendorByID(id); v != nil {
		return v.DefaultModel
	}
	return ""
}

// VendorModels returns the catalogue of model IDs offered for a vendor (for a
// UI dropdown), or nil if the vendor is unknown.
func VendorModels(id string) []string {
	v := vendorByID(id)
	if v == nil || len(v.Models) == 0 {
		return nil
	}
	ids := make([]string, len(v.Models))
	for i, m := range v.Models {
		ids[i] = m.ID
	}
	return ids
}

// VendorModelVision reports a predefined model's image-input capability. known
// is false when the vendor or model isn't in the catalogue (e.g. the Custom
// vendor, or a model id the user typed by hand) — callers then fall back to the
// heuristic (config.ModelSupportsVision) or ask the user.
func VendorModelVision(vendorID, modelID string) (vision, known bool) {
	v := vendorByID(vendorID)
	if v == nil {
		return false, false
	}
	for _, m := range v.Models {
		if m.ID == modelID {
			return m.Vision, true
		}
	}
	return false, false
}

// VendorModelVisionMap returns model-id → vision for a vendor's catalogue, for
// the web form to pre-fill the toggle when a predefined model is picked. Nil for
// unknown vendors or vendors with no predefined models (e.g. Custom).
func VendorModelVisionMap(id string) map[string]bool {
	v := vendorByID(id)
	if v == nil || len(v.Models) == 0 {
		return nil
	}
	m := make(map[string]bool, len(v.Models))
	for _, vm := range v.Models {
		m[vm.ID] = vm.Vision
	}
	return m
}

// VendorEndpointVariants returns the regional endpoint alternatives for a
// vendor, or nil if it has none / is unknown.
func VendorEndpointVariants(id string) []EndpointVariant {
	if v := vendorByID(id); v != nil {
		return v.EndpointVariants
	}
	return nil
}

// VendorAPIKeyEnvVar returns the environment-variable name for a vendor's API key.
func VendorAPIKeyEnvVar(id string) string {
	if v := vendorByID(id); v != nil {
		return v.APIKeyEnvVar
	}
	return ""
}

// VendorDisplayName returns the human-readable label for a vendor ID.
func VendorDisplayName(id string) string {
	if v := vendorByID(id); v != nil {
		return v.DisplayName
	}
	return id
}

// VendorWebsiteURL returns the key-management page URL for a vendor ID.
func VendorWebsiteURL(id string) string {
	if v := vendorByID(id); v != nil {
		return v.WebsiteURL
	}
	return ""
}

// IsKnownVendor reports whether id is a registered vendor ID.
func IsKnownVendor(id string) bool {
	return vendorByID(id) != nil
}

// ImplicitLiteModel returns the lite model a session should compact on when
// the user configured none explicitly: the vendor's registry LiteModel,
// served over the SAME sender as the primary model — same endpoint, key, and
// prompt-cache routing. It returns "" (no implicit lite) when:
//   - the vendor is unknown or has no LiteModel,
//   - the primary model already IS the lite model, or
//   - baseURL points off the vendor's own endpoints — a custom endpoint is a
//     different backend wearing a compatible protocol, and its catalogue
//     won't include the vendor's lite model.
func ImplicitLiteModel(provider, model, baseURL string) string {
	v := vendorByID(provider)
	if v == nil || v.LiteModel == "" || v.LiteModel == model {
		return ""
	}
	if baseURL != "" && VendorByBaseURL(baseURL) != provider {
		return ""
	}
	return v.LiteModel
}

// VendorByBaseURL returns the vendor whose default endpoint or one of whose
// regional variants equals baseURL (ignoring a trailing slash), or "". The
// settings panel saves entries without naming a vendor, so the server infers
// it from the endpoint the user picked.
func VendorByBaseURL(baseURL string) string {
	u := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if u == "" {
		return ""
	}
	for i := range Registry {
		v := &Registry[i]
		if strings.TrimSuffix(v.DefaultBaseURL, "/") == u {
			return v.ID
		}
		for _, ev := range v.EndpointVariants {
			if strings.TrimSuffix(ev.BaseURL, "/") == u {
				return v.ID
			}
		}
	}
	return ""
}

// IsAnthropicProtocol reports whether the vendor speaks the Anthropic protocol.
func IsAnthropicProtocol(id string) bool {
	return VendorProtocol(id) == "anthropic"
}
