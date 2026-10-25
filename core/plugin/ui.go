package plugin

// ConfigSchema describes the controls a host can use to edit a plugin
// instance configuration. It is deliberately limited to portable data so a
// plugin cannot execute code in the host UI.
type ConfigSchema struct {
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	Fields      []ConfigField `json:"fields,omitempty"`
}

type ConfigField struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Label       string         `json:"label,omitempty"`
	Description string         `json:"description,omitempty"`
	Placeholder string         `json:"placeholder,omitempty"`
	Required    bool           `json:"required,omitempty"`
	Default     any            `json:"default,omitempty"`
	Options     []ConfigOption `json:"options,omitempty"`
	Fields      []ConfigField  `json:"fields,omitempty"`
	ItemFields  []ConfigField  `json:"item_fields,omitempty"`
	Min         *float64       `json:"min,omitempty"`
	Max         *float64       `json:"max,omitempty"`
	Step        *float64       `json:"step,omitempty"`
}

type ConfigOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ConfigSchemaProvider is an optional extension for compiled-in plugins. A
// portable package can put the same schema in its manifest instead.
type ConfigSchemaProvider interface {
	ConfigSchema() ConfigSchema
}
