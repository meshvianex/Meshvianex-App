// Package resources contains the persistent, Realm-independent subscription
// and normalized plugin configuration library.
package resources

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

type Subscription struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Source            string            `json:"source"`
	SourceType        string            `json:"source_type"`
	ParserPlugin      string            `json:"parser_plugin"`
	Enabled           bool              `json:"enabled"`
	RefreshInterval   string            `json:"refresh_interval,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	LastRefreshAt     time.Time         `json:"last_refresh_at,omitempty"`
	LastRefreshStatus string            `json:"last_refresh_status,omitempty"`
	LastError         string            `json:"last_error,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Tags              []string          `json:"tags,omitempty"`
}

type NormalizedConfig struct {
	Name       string          `json:"name,omitempty"`
	PluginID   string          `json:"plugin_id"`
	PluginType string          `json:"plugin_type"`
	Config     json.RawMessage `json:"config"`
}

type Config struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	SourceSubscriptionID string          `json:"source_subscription_id,omitempty"`
	Format               string          `json:"format"`
	PluginID             string          `json:"plugin_id"`
	PluginType           string          `json:"plugin_type"`
	Normalized           json.RawMessage `json:"normalized"`
	Enabled              bool            `json:"enabled"`
	Tags                 []string        `json:"tags,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
	ValidationStatus     string          `json:"validation_status"`
	ValidationError      string          `json:"validation_error,omitempty"`
	StableKey            string          `json:"stable_key,omitempty"`
}

type Diagnostic struct {
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

type ParseResult struct {
	Configs     []NormalizedConfig `json:"configs"`
	Diagnostics []Diagnostic       `json:"diagnostics,omitempty"`
	Format      string             `json:"format,omitempty"`
}

// Parser and SubscriptionFetcher are the typed extension points implemented
// by plugins. The store intentionally knows nothing about URI/protocol syntax.
type Parser interface {
	Parse(context.Context, string) (ParseResult, error)
}

type SubscriptionFetcher interface {
	Fetch(context.Context, Subscription) (string, error)
}

type Store struct {
	Root string
	mu   sync.Mutex
}

func NewStore(root string) *Store { return &Store{Root: root} }
