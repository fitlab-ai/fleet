package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/store"
)

type memoryCredentials struct{ values map[string]string }

func (m *memoryCredentials) SetURL(id, value string) error {
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[id] = value
	return nil
}
func (m *memoryCredentials) GetURL(id string) (string, error) {
	value, ok := m.values[id]
	if !ok {
		return "", model.NewError("credential", "Subscription is not configured", nil)
	}
	return value, nil
}
func (m *memoryCredentials) DeleteURL(id string) error   { delete(m.values, id); return nil }
func (m *memoryCredentials) IsConfigured(id string) bool { _, ok := m.values[id]; return ok }

func TestSubscriptionAddStoresNoSecretOnDisk(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	credentials := &memoryCredentials{}
	a := App{Config: Config{Dir: root}, Credentials: credentials, Out: &out}
	if code := a.SubscriptionAdd("airport", "https://example.com/sub?token=SECRET"); code != 0 {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
	data, err := storeRead(root + "/subscriptions.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data, "SECRET") || strings.Contains(out.String(), "SECRET") {
		t.Fatal("subscription URL leaked")
	}
}

func storeRead(path string) (string, error) {
	var value model.Registry
	if err := store.ReadJSON(path, &value); err != nil {
		return "", err
	}
	return strings.Join([]string{value.Subscriptions[0].Name, value.Subscriptions[0].ID}, ":"), nil
}
