package config

// PluginConfig holds all plugin-related configuration sections.
type PluginConfig struct {
	Playbooks []PluginPlaybookConfig         `yaml:"playbooks"`
	Prompts   map[string]string              `yaml:"prompts"`
	QA        []PluginQAConfig               `yaml:"qa"`
	Providers map[string]PluginProviderConfig `yaml:"providers"`
}

// PluginPlaybookConfig describes a single playbook injected by a plugin.
type PluginPlaybookConfig struct {
	Name       string   `yaml:"name"`
	File       string   `yaml:"file"`
	InjectWhen string   `yaml:"inject_when"`
	Roles      []string `yaml:"roles"`
}

// PluginQAConfig describes a QA check contributed by a plugin.
type PluginQAConfig struct {
	Name  string `yaml:"name"`
	File  string `yaml:"file"`
	After string `yaml:"after"`
}

// PluginProviderConfig describes an external model provider contributed by a plugin.
type PluginProviderConfig struct {
	Command string   `yaml:"command"`
	Models  []string `yaml:"models"`
}
