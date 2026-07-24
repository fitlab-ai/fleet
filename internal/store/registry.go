package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fitlab-ai/fleet/internal/model"
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)
var idRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

type RegistryStore struct {
	Path     string
	Registry model.Registry
}

func OpenRegistry(configDir string) (*RegistryStore, error) {
	s := &RegistryStore{Path: filepath.Join(configDir, "subscriptions.json")}
	s.Registry = model.Registry{Schema: 2, Subscriptions: []model.Subscription{}}
	if _, err := os.Stat(s.Path); err == nil {
		if err := ReadJSON(s.Path, &s.Registry); err != nil {
			return nil, model.NewError("registry", "Subscription registry is damaged", err)
		}
		if s.Registry.Schema != 2 || s.Registry.Revision < 0 {
			return nil, model.NewError("registry", "Subscription registry has an unsupported schema", nil)
		}
		seenNames, seenIDs := map[string]bool{}, map[string]bool{}
		for _, record := range s.Registry.Subscriptions {
			folded := strings.ToLower(record.Name)
			if !idRE.MatchString(record.ID) || !nameRE.MatchString(record.Name) ||
				(record.Status != "active" && record.Status != "removed") ||
				seenNames[folded] || seenIDs[record.ID] {
				return nil, model.NewError("registry", "Subscription registry contains an invalid record", nil)
			}
			seenNames[folded], seenIDs[record.ID] = true, true
		}
	}
	return s, nil
}

func (s *RegistryStore) Save() error {
	s.Registry.Revision++
	if err := AtomicJSON(s.Path, s.Registry); err != nil {
		s.Registry.Revision--
		return err
	}
	return nil
}

func (s *RegistryStore) AllocateName() string {
	used := map[string]bool{}
	for _, record := range s.Registry.Subscriptions {
		used[strings.ToLower(record.Name)] = true
	}
	for i := 1; ; i++ {
		name := fmt.Sprintf("subscription-%d", i)
		if !used[name] {
			return name
		}
	}
}

func (s *RegistryStore) Add(id, name string) (*model.Subscription, error) {
	if name == "" {
		name = s.AllocateName()
	}
	if !nameRE.MatchString(name) {
		return nil, model.NewError("registry", "Subscription name must use 1-32 letters, digits, '.', '_' or '-'", nil)
	}
	for _, record := range s.Registry.Subscriptions {
		if strings.EqualFold(record.Name, name) {
			return nil, model.NewError("registry", "Subscription name is already in use", nil)
		}
	}
	record := model.Subscription{ID: id, Name: name, Status: "active", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	s.Registry.Subscriptions = append(s.Registry.Subscriptions, record)
	return &s.Registry.Subscriptions[len(s.Registry.Subscriptions)-1], nil
}

func (s *RegistryStore) Resolve(selector string, activeOnly bool) (*model.Subscription, error) {
	var indexes []int
	for i, record := range s.Registry.Subscriptions {
		if activeOnly && record.Status != "active" {
			continue
		}
		if strings.EqualFold(record.Name, selector) || record.ID == selector || strings.HasPrefix(record.ID, strings.ToLower(selector)) {
			indexes = append(indexes, i)
		}
	}
	if len(indexes) != 1 {
		message := "Subscription was not found"
		if len(indexes) > 1 {
			message = "Subscription selector is ambiguous"
		}
		return nil, model.NewError("selector", message, nil)
	}
	return &s.Registry.Subscriptions[indexes[0]], nil
}

func (s *RegistryStore) MarkRemoved(selector string) (*model.Subscription, error) {
	record, err := s.Resolve(selector, true)
	if err != nil {
		return nil, err
	}
	record.Status = "removed"
	record.RemovedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return record, nil
}

func (s *RegistryStore) RestoreActive(id string) {
	if record, err := s.Resolve(id, false); err == nil {
		record.Status = "active"
		record.RemovedAt = ""
	}
}

func (s *RegistryStore) PurgeRemoved() []model.Subscription {
	var active, removed []model.Subscription
	for _, record := range s.Registry.Subscriptions {
		if record.Status == "removed" {
			removed = append(removed, record)
		} else {
			active = append(active, record)
		}
	}
	s.Registry.Subscriptions = active
	return removed
}
