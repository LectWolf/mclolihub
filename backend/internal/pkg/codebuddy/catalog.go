package codebuddy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const (
	CreditPolicyAll      = "all"
	CreditPolicyZeroOnly = "zero_only"
	ResourceProductCode  = "p_tcaca"
	ResourcePath         = "/v2/billing/meter/get-user-resource"
	RequestUsagePath     = "/billing/meter/get-user-request-usage"
)

var multiplierRE = regexp.MustCompile(`(?i)^x\s*([0-9]+(?:\.[0-9]+)?)\s*(?:credits?)?$`)

type CatalogModel struct {
	ID           string   `json:"id"`
	Name         string   `json:"name,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
	Credits      string   `json:"credits,omitempty"`
	CreditsValue *float64 `json:"credits_value,omitempty"`
	Disabled     bool     `json:"disabled,omitempty"`
}

func CatalogHeaders(profile, accessToken, domain, uid, enterpriseID string) map[string]string {
	headers := CredentialHeaders(profile, accessToken, domain, uid, enterpriseID)
	headers["Connection"] = "close"
	headers["Accept"] = "application/json"
	if ProfileProduct(profile) == ProductCLI {
		headers["x-client-platform"] = "cli"
	}
	return headers
}

func ConfigURL(profile string) string {
	return EndpointForProfile(profile) + ConfigPath
}

func ParseCatalog(payload []byte, product string) ([]CatalogModel, error) {
	var root struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	data := root.Data
	if len(data) == 0 {
		data = payload
	}
	var body struct {
		Models          []map[string]any `json:"models"`
		AvailableModels []string         `json:"availableModels"`
		Agents          []map[string]any `json:"agents"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("decode catalog data: %w", err)
	}
	if len(body.Models) == 0 {
		return nil, fmt.Errorf("catalog has no models")
	}

	byID := map[string]map[string]any{}
	byName := map[string]map[string]any{}
	byAlias := map[string]map[string]any{}
	for _, model := range body.Models {
		id := strings.TrimSpace(asString(model["id"]))
		if id == "" {
			continue
		}
		byID[id] = model
		if name := strings.TrimSpace(asString(model["name"])); name != "" {
			byName[name] = model
		}
		for _, alias := range stringSlice(model["aliases"]) {
			byAlias[alias] = model
		}
	}

	selected := body.Models
	if refs := agentModelRefs(body.Agents, product); len(refs) > 0 {
		picked := make([]map[string]any, 0, len(refs))
		seen := map[string]struct{}{}
		for _, ref := range refs {
			model := byID[ref]
			if model == nil {
				model = byName[ref]
			}
			if model == nil {
				model = byAlias[ref]
			}
			if model == nil {
				continue
			}
			id := strings.TrimSpace(asString(model["id"]))
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			picked = append(picked, model)
		}
		if len(picked) > 0 {
			selected = picked
		}
	}

	available := map[string]struct{}{}
	for _, id := range body.AvailableModels {
		id = strings.TrimSpace(id)
		if id != "" {
			available[id] = struct{}{}
		}
	}

	out := make([]CatalogModel, 0, len(selected))
	for _, model := range selected {
		id := strings.TrimSpace(asString(model["id"]))
		if id == "" {
			continue
		}
		if asBool(model["disabled"]) {
			continue
		}
		if len(available) > 0 {
			if _, ok := available[id]; !ok {
				continue
			}
		}
		credits := strings.TrimSpace(asString(model["credits"]))
		entry := CatalogModel{
			ID:           id,
			Name:         strings.TrimSpace(asString(model["name"])),
			Aliases:      stringSlice(model["aliases"]),
			Credits:      credits,
			CreditsValue: ParseMultiplier(credits),
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("catalog produced no usable models")
	}
	return out, nil
}

func agentModelRefs(agents []map[string]any, product string) []string {
	if product == "" {
		product = ProductCLI
	}
	var cli, tagged, fallback map[string]any
	for _, agent := range agents {
		name := strings.TrimSpace(asString(agent["name"]))
		tags := stringSlice(agent["tags"])
		if name == "cli" {
			cli = agent
		}
		if contains(tags, "default") {
			tagged = agent
		}
		if _, ok := agent["models"]; ok && fallback == nil {
			fallback = agent
		}
	}
	chosen := cli
	if product == ProductWorkBuddy {
		if tagged != nil {
			chosen = tagged
		} else if chosen == nil {
			chosen = fallback
		}
	}
	if chosen == nil {
		return nil
	}
	raw, ok := chosen["models"]
	if !ok {
		return nil
	}
	switch models := raw.(type) {
	case []any:
		refs := make([]string, 0, len(models))
		for _, item := range models {
			switch v := item.(type) {
			case string:
				if s := strings.TrimSpace(v); s != "" {
					refs = append(refs, s)
				}
			case map[string]any:
				if id := strings.TrimSpace(asString(v["id"])); id != "" {
					refs = append(refs, id)
					continue
				}
				if name := strings.TrimSpace(asString(v["name"])); name != "" {
					refs = append(refs, name)
				}
			}
		}
		return refs
	default:
		return nil
	}
}

func ParseMultiplier(credits string) *float64 {
	credits = strings.TrimSpace(credits)
	if credits == "" {
		return nil
	}
	match := multiplierRE.FindStringSubmatch(credits)
	if match == nil {
		return nil
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return nil
	}
	return &value
}

func IsZeroCredit(credits string) bool {
	value := ParseMultiplier(credits)
	return value != nil && *value == 0
}

// FindModel resolves a catalog entry by canonical ID, falling back to aliases.
// IDs win over aliases across the whole catalog so an alias on one model can
// never shadow another model's real ID.
func FindModel(models []CatalogModel, modelID string) (CatalogModel, bool) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return CatalogModel{}, false
	}
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model.ID), modelID) {
			return model, true
		}
	}
	for _, model := range models {
		for _, alias := range model.Aliases {
			if strings.EqualFold(strings.TrimSpace(alias), modelID) {
				return model, true
			}
		}
	}
	return CatalogModel{}, false
}

// ModelMultiplier resolves how many credits one request against modelID costs.
// The second return reports whether the catalog priced the model at all, which
// callers need to distinguish a genuinely free model from an unknown one.
func ModelMultiplier(models []CatalogModel, modelID string) (float64, bool) {
	model, ok := FindModel(models, modelID)
	if !ok {
		return 0, false
	}
	if model.CreditsValue != nil {
		return *model.CreditsValue, true
	}
	if value := ParseMultiplier(model.Credits); value != nil {
		return *value, true
	}
	return 0, false
}

func FilterCatalog(models []CatalogModel, policy string) []CatalogModel {
	if NormalizeCreditPolicy(policy) != CreditPolicyZeroOnly {
		return models
	}
	out := make([]CatalogModel, 0, len(models))
	for _, model := range models {
		if IsZeroCredit(model.Credits) {
			out = append(out, model)
		}
	}
	return out
}

func NormalizeCreditPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case CreditPolicyZeroOnly, "zero", "free":
		return CreditPolicyZeroOnly
	default:
		return CreditPolicyAll
	}
}

func CatalogIDs(models []CatalogModel) []string {
	ids := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func ApplyHeadersToRequest(req *http.Request, headers map[string]string) {
	if req == nil {
		return
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func stringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s := strings.TrimSpace(asString(item))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
