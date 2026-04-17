package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// SubprocessInfo describes an external model provider subprocess.
type SubprocessInfo struct {
	Command string
	Models  []string
}

// PluginManager holds all loaded plugin artefacts.
type PluginManager struct {
	Playbooks []PluginPlaybook
	Prompts   map[string]string
	QAChecks  []PluginQACheck
	Providers map[string]*SubprocessInfo
}

// EmptyManager returns a PluginManager with no plugins loaded.
func EmptyManager() *PluginManager {
	return &PluginManager{
		Playbooks: nil,
		Prompts:   make(map[string]string),
		QAChecks:  nil,
		Providers: make(map[string]*SubprocessInfo),
	}
}

// LoadPlugins reads plugin artefacts from disk according to cfg and returns a
// populated PluginManager. It returns an error when a referenced file is
// missing or empty.
func LoadPlugins(cfg config.PluginConfig, pluginDir string) (*PluginManager, error) {
	mgr := EmptyManager()

	if err := loadPlaybooks(mgr, cfg.Playbooks, pluginDir); err != nil {
		return nil, err
	}
	if err := loadPrompts(mgr, cfg.Prompts, pluginDir); err != nil {
		return nil, err
	}
	if err := loadQAChecks(mgr, cfg.QA, pluginDir); err != nil {
		return nil, err
	}
	loadProviders(mgr, cfg.Providers)

	return mgr, nil
}

// loadPlaybooks reads each playbook's markdown file from the playbooks subdir.
func loadPlaybooks(mgr *PluginManager, cfgs []config.PluginPlaybookConfig, pluginDir string) error {
	for _, pc := range cfgs {
		resolved, err := resolvePath(pluginDir, "playbooks", pc.File)
		if err != nil {
			return fmt.Errorf("playbook %q: %w", pc.Name, err)
		}
		content, err := readNonEmptyFile(resolved)
		if err != nil {
			return fmt.Errorf("playbook %q: %w", pc.Name, err)
		}
		pb := PluginPlaybook{
			Name:       pc.Name,
			Content:    content,
			InjectWhen: pc.InjectWhen,
			Roles:      copyStrings(pc.Roles),
		}
		mgr.Playbooks = append(mgr.Playbooks, pb)
	}
	return nil
}

// loadPrompts reads each prompt override file from the prompts subdir.
func loadPrompts(mgr *PluginManager, prompts map[string]string, pluginDir string) error {
	for key, file := range prompts {
		resolved, err := resolvePath(pluginDir, "prompts", file)
		if err != nil {
			return fmt.Errorf("prompt %q: %w", key, err)
		}
		content, err := readNonEmptyFile(resolved)
		if err != nil {
			return fmt.Errorf("prompt %q: %w", key, err)
		}
		mgr.Prompts[key] = content
	}
	return nil
}

// loadQAChecks resolves each QA check script path and validates it exists.
func loadQAChecks(mgr *PluginManager, cfgs []config.PluginQAConfig, pluginDir string) error {
	for _, qc := range cfgs {
		resolved, err := resolvePath(pluginDir, "qa", qc.File)
		if err != nil {
			return fmt.Errorf("qa check %q: %w", qc.Name, err)
		}
		if _, err := os.Stat(resolved); err != nil {
			return fmt.Errorf("qa check %q: %w", qc.Name, err)
		}
		check := PluginQACheck{
			Name:       qc.Name,
			ScriptPath: resolved,
			After:      qc.After,
		}
		mgr.QAChecks = append(mgr.QAChecks, check)
	}
	return nil
}

// loadProviders converts provider config entries into SubprocessInfo values.
func loadProviders(mgr *PluginManager, providers map[string]config.PluginProviderConfig) {
	for name, pc := range providers {
		mgr.Providers[name] = &SubprocessInfo{
			Command: pc.Command,
			Models:  copyStrings(pc.Models),
		}
	}
}

// resolvePath builds an absolute path for a plugin file within the plugin
// directory. Absolute paths are rejected to prevent loading files from
// arbitrary locations. The resolved path must remain under pluginDir.
func resolvePath(pluginDir, subdir, file string) (string, error) {
	if filepath.IsAbs(file) {
		return "", fmt.Errorf("absolute plugin paths are not allowed: %s", file)
	}

	resolved := filepath.Clean(filepath.Join(pluginDir, subdir, file))
	base := filepath.Clean(pluginDir)

	// Ensure resolved path stays within the plugin directory.
	if !strings.HasPrefix(resolved, base+string(filepath.Separator)) && resolved != base {
		return "", fmt.Errorf("plugin path traversal blocked: %s resolves outside plugin directory", file)
	}

	return resolved, nil
}

// readNonEmptyFile reads the file at path and returns an error if the file
// does not exist or is empty (after trimming whitespace).
func readNonEmptyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", fmt.Errorf("file is empty: %s", path)
	}
	return content, nil
}

// copyStrings returns a new copy of the given string slice. Returns nil for nil/empty input.
func copyStrings(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}
