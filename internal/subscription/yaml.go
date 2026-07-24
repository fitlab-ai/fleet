package subscription

import (
	"encoding/json"
	"fmt"

	"github.com/fitlab-ai/fleet/internal/model"
	"go.yaml.in/yaml/v3"
)

func normalize(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalize(item)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[fmt.Sprint(key)] = normalize(item)
		}
		return out
	case []any:
		for i := range typed {
			typed[i] = normalize(typed[i])
		}
	}
	return value
}

func ParseYAML(data []byte) ([]model.Node, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, model.NewError("yaml", "Subscription YAML is invalid", err)
	}
	top, ok := normalize(raw).(map[string]any)
	if !ok {
		return nil, model.NewError("format", "Subscription response is not supported Clash YAML; check format negotiation", nil)
	}
	proxies, ok := top["proxies"].([]any)
	if !ok {
		return nil, model.NewError("structure", "Subscription must contain a proxies list", nil)
	}
	nodes := make([]model.Node, 0, len(proxies))
	for _, value := range proxies {
		mapping, ok := value.(map[string]any)
		if !ok {
			return nil, model.NewError("node", "A proxy node is not an object", nil)
		}
		encoded, _ := json.Marshal(mapping)
		var node model.Node
		if err := json.Unmarshal(encoded, &node); err != nil {
			return nil, model.NewError("node", "A proxy node contains invalid fields", err)
		}
		node.Extra = mapping
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func RecoverURL(data []byte) string {
	var raw any
	if yaml.Unmarshal(data, &raw) != nil {
		return ""
	}
	top, ok := normalize(raw).(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"subscription-url", "subscription_url", "url"} {
		if value, ok := top[key].(string); ok {
			if validated, err := ValidateURL(value); err == nil {
				return validated
			}
		}
	}
	return ""
}
