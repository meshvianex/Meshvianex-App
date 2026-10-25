package resources

import (
	"fmt"
	"sort"
	"time"
)

func (s *Store) ListSubscriptions() ([]Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Subscription
	if err := readJSON(s.path("subscriptions.json"), &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) GetSubscription(id string) (Subscription, error) {
	items, err := s.ListSubscriptions()
	if err != nil {
		return Subscription{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Subscription{}, fmt.Errorf("subscription not found: %s", id)
}

func (s *Store) SaveSubscription(item Subscription) error {
	if item.ID == "" || item.Name == "" || item.Source == "" {
		return fmt.Errorf("subscription id, name, and source are required")
	}
	now := time.Now().UTC()
	item.UpdatedAt = now
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []Subscription
	if err := readJSON(s.path("subscriptions.json"), &items); err != nil {
		return err
	}
	updated := false
	for i := range items {
		if items[i].ID == item.ID {
			if item.CreatedAt.IsZero() {
				item.CreatedAt = items[i].CreatedAt
			}
			items[i] = item
			updated = true
			break
		}
	}
	if !updated {
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		items = append(items, item)
	}
	return writeJSON(s.path("subscriptions.json"), items)
}

func (s *Store) DeleteSubscription(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := readSubscriptions(s.path("subscriptions.json"))
	if err != nil {
		return err
	}
	filtered := items[:0]
	found := false
	for _, item := range items {
		if item.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, item)
	}
	if !found {
		return fmt.Errorf("subscription not found: %s", id)
	}
	return writeJSON(s.path("subscriptions.json"), filtered)
}
