package model

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type SafeError struct {
	Category string
	Message  string
	Cause    any
}

func NewError(category, message string, cause any) *SafeError {
	return &SafeError{Category: category, Message: message, Cause: cause}
}

func (e *SafeError) Error() string { return e.Message }
func (e *SafeError) Unwrap() error {
	if err, ok := e.Cause.(error); ok {
		return err
	}
	return nil
}

type Metadata struct {
	NodeKey            string `json:"node_key,omitempty"`
	SubscriptionID     string `json:"subscription_id,omitempty"`
	SubscriptionName   string `json:"subscription_name,omitempty"`
	SubscriptionStatus string `json:"subscription_status,omitempty"`
}

type Node struct {
	Name           string         `json:"name" yaml:"name"`
	Type           string         `json:"type" yaml:"type"`
	Server         string         `json:"server" yaml:"server"`
	Port           int            `json:"port" yaml:"port"`
	UUID           string         `json:"uuid,omitempty" yaml:"uuid,omitempty"`
	Password       string         `json:"password,omitempty" yaml:"password,omitempty"`
	Cipher         string         `json:"cipher,omitempty" yaml:"cipher,omitempty"`
	AlterID        *int           `json:"alterId,omitempty" yaml:"alterId,omitempty"`
	Network        string         `json:"network,omitempty" yaml:"network,omitempty"`
	SNI            string         `json:"sni,omitempty" yaml:"sni,omitempty"`
	TLS            any            `json:"tls,omitempty" yaml:"tls,omitempty"`
	SkipCertVerify any            `json:"skip-cert-verify,omitempty" yaml:"skip-cert-verify,omitempty"`
	ALPN           any            `json:"alpn,omitempty" yaml:"alpn,omitempty"`
	Up             any            `json:"up,omitempty" yaml:"up,omitempty"`
	Down           any            `json:"down,omitempty" yaml:"down,omitempty"`
	Ports          any            `json:"ports,omitempty" yaml:"ports,omitempty"`
	HopInterval    any            `json:"hop-interval,omitempty" yaml:"hop-interval,omitempty"`
	Obfs           any            `json:"obfs,omitempty" yaml:"obfs,omitempty"`
	ObfsPassword   any            `json:"obfs-password,omitempty" yaml:"obfs-password,omitempty"`
	WSOpts         map[string]any `json:"ws-opts,omitempty" yaml:"ws-opts,omitempty"`
	Extra          map[string]any `json:"-" yaml:"-"`
	Fleet          Metadata       `json:"_fleet,omitempty" yaml:"-"`
}

func (n Node) MarshalJSON() ([]byte, error) {
	type alias Node
	base, err := json.Marshal(alias(n))
	if err != nil {
		return nil, err
	}
	var merged map[string]any
	_ = json.Unmarshal(base, &merged)
	for key, value := range n.Extra {
		if key != "_fleet_hysteria2" {
			if _, exists := merged[key]; !exists || merged[key] == nil || merged[key] == "" {
				merged[key] = value
			}
		}
	}
	delete(merged, "Extra")
	return json.Marshal(merged)
}

func (n *Node) UnmarshalJSON(data []byte) error {
	type alias Node
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var extra map[string]any
	if err := json.Unmarshal(data, &extra); err != nil {
		return err
	}
	*n = Node(decoded)
	n.Extra = extra
	return nil
}

type Subscription struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	RemovedAt string `json:"removed_at,omitempty"`
}

type Registry struct {
	Schema        int            `json:"schema"`
	Revision      int            `json:"revision"`
	Subscriptions []Subscription `json:"subscriptions"`
}

type RuntimeState struct {
	Mode              string         `json:"mode"`
	Node              string         `json:"node"`
	NodeName          string         `json:"node_name,omitempty"`
	NodeKey           string         `json:"node_key,omitempty"`
	SubscriptionID    string         `json:"subscription_id,omitempty"`
	SubscriptionName  string         `json:"subscription_name,omitempty"`
	PID               int            `json:"pid,omitempty"`
	Port              int            `json:"port,omitempty"`
	SystemProxyBefore map[string]any `json:"system_proxy_before,omitempty"`
}

func ResolveNode(nodes []Node, selector string) (Node, error) {
	if selector == "" {
		return Node{}, NewError("selector", "Node selector is required", nil)
	}
	if index, err := strconv.Atoi(selector); err == nil {
		if index >= 0 && index < len(nodes) {
			return nodes[index], nil
		}
		return Node{}, NewError("selector", "Node was not found", nil)
	}
	var matches []Node
	if strings.HasPrefix(selector, "@") && strings.Contains(selector, "/") {
		parts := strings.SplitN(strings.TrimPrefix(selector, "@"), "/", 2)
		for _, node := range nodes {
			if strings.EqualFold(node.Fleet.SubscriptionName, parts[0]) && node.Name == parts[1] {
				matches = append(matches, node)
			}
		}
	} else {
		for _, node := range nodes {
			if node.Name == selector {
				matches = append(matches, node)
			}
		}
		if len(matches) == 0 {
			for _, node := range nodes {
				if strings.Contains(strings.ToLower(node.Name), strings.ToLower(selector)) {
					matches = append(matches, node)
				}
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return Node{}, NewError("selector", fmt.Sprintf("Node name is ambiguous: %s", selector), nil)
	}
	return Node{}, NewError("selector", "Node was not found", nil)
}

var _ error = (*SafeError)(nil)
var _ io.Reader = (*strings.Reader)(nil)
