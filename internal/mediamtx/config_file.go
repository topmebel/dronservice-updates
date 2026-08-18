package mediamtx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

const maxConfigFileSize = 2 << 20

var managedPathNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

type ConfigFile struct {
	mu   sync.Mutex
	path string
}

func NewConfigFile(path string) *ConfigFile { return &ConfigFile{path: path} }

func (f *ConfigFile) Read() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.read()
}

func (f *ConfigFile) Write(content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := validateConfigText(content); err != nil {
		return err
	}
	return writeConfigFile(f.path, content)
}

func (f *ConfigFile) SetPath(name string, update PathConfigUpdate) error {
	if !managedPathNamePattern.MatchString(name) {
		return errors.New("invalid MediaMTX path name")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	content, err := f.read()
	if err != nil {
		return err
	}
	updated, err := replacePathBlock(content, name, renderPathBlock(name, update))
	if err != nil {
		return err
	}
	return writeConfigFile(f.path, updated)
}

func (f *ConfigFile) DeletePath(name string) error {
	if !managedPathNamePattern.MatchString(name) {
		return errors.New("invalid MediaMTX path name")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	content, err := f.read()
	if err != nil {
		return err
	}
	updated, found, err := removePathBlock(content, name)
	if err != nil {
		return err
	}
	if !found {
		return os.ErrNotExist
	}
	return writeConfigFile(f.path, updated)
}

func (f *ConfigFile) read() (string, error) {
	file, err := os.Open(f.path)
	if err != nil {
		return "", fmt.Errorf("open MediaMTX configuration: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat MediaMTX configuration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxConfigFileSize {
		return "", errors.New("MediaMTX configuration is not a supported regular file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read MediaMTX configuration: %w", err)
	}
	return string(data), nil
}

func validateConfigText(content string) error {
	if len(content) == 0 || len(content) > maxConfigFileSize || strings.ContainsRune(content, 0) {
		return errors.New("invalid MediaMTX configuration size or content")
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return fmt.Errorf("invalid MediaMTX YAML: %w", err)
	}
	_, _, err := pathsSection(strings.SplitAfter(content, "\n"))
	if err != nil {
		return err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("MediaMTX configuration root must be a YAML mapping")
	}
	root := document.Content[0]
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == "paths" {
			if root.Content[index+1].Kind != yaml.MappingNode {
				return errors.New("MediaMTX paths must be a YAML mapping")
			}
			return nil
		}
	}
	return errors.New("MediaMTX configuration has no paths mapping")
}

func replacePathBlock(content, name, block string) (string, error) {
	without, found, err := removePathBlock(content, name)
	if err != nil {
		return "", err
	}
	lines := strings.SplitAfter(without, "\n")
	start, end, err := pathsSection(lines)
	if err != nil {
		return "", err
	}
	insert := end
	for index := start + 1; index < end; index++ {
		if strings.HasPrefix(strings.TrimSuffix(lines[index], "\n"), "  all_others:") {
			insert = index
			break
		}
	}
	if !found && insert > start+1 && !strings.HasSuffix(strings.Join(lines[:insert], ""), "\n") {
		block = "\n" + block
	}
	lines = append(lines[:insert], append([]string{block}, lines[insert:]...)...)
	return strings.Join(lines, ""), nil
}

func removePathBlock(content, name string) (string, bool, error) {
	lines := strings.SplitAfter(content, "\n")
	start, end, err := pathsSection(lines)
	if err != nil {
		return "", false, err
	}
	for index := start + 1; index < end; index++ {
		if strings.TrimSpace(strings.TrimSuffix(lines[index], "\n")) == "" || strings.TrimSpace(strings.TrimSuffix(lines[index], "\n")) != name+":" {
			continue
		}
		blockStart := index
		if index > start+1 && strings.TrimSpace(lines[index-1]) == "# DronService managed path: "+name {
			blockStart--
		}
		blockEnd := index + 1
		for blockEnd < end {
			line := strings.TrimSuffix(lines[blockEnd], "\n")
			if line == "" || !strings.HasPrefix(line, " ") {
				break
			}
			if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
				break
			}
			blockEnd++
		}
		lines = append(lines[:blockStart], lines[blockEnd:]...)
		return strings.Join(lines, ""), true, nil
	}
	return content, false, nil
}

func pathsSection(lines []string) (int, int, error) {
	start := -1
	for index, raw := range lines {
		line := strings.TrimSuffix(raw, "\n")
		if strings.TrimSpace(line) == "paths:" && !strings.HasPrefix(line, " ") {
			if start != -1 {
				return 0, 0, errors.New("MediaMTX configuration contains multiple paths sections")
			}
			start = index
		}
	}
	if start == -1 {
		return 0, 0, errors.New("MediaMTX configuration has no top-level paths section")
	}
	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		line := strings.TrimSuffix(lines[index], "\n")
		if line != "" && line[0] != ' ' && line[0] != '#' {
			end = index
			break
		}
	}
	return start, end, nil
}

func renderPathBlock(name string, update PathConfigUpdate) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "  # DronService managed path: %s\n  %s:\n", name, name)
	if update.Source != "" {
		fmt.Fprintf(&builder, "    source: %s\n", yamlScalar(update.Source))
	}
	if update.SourceOnDemand {
		builder.WriteString("    sourceOnDemand: yes\n")
	}
	if update.RunOnDemand != "" {
		fmt.Fprintf(&builder, "    runOnDemand: %s\n", yamlScalar(update.RunOnDemand))
	}
	if update.RunOnDemandRestart {
		builder.WriteString("    runOnDemandRestart: yes\n")
	}
	if update.RunOnDemandStartTimeout != "" {
		fmt.Fprintf(&builder, "    runOnDemandStartTimeout: %s\n", update.RunOnDemandStartTimeout)
	}
	if update.RunOnDemandCloseAfter != "" {
		fmt.Fprintf(&builder, "    runOnDemandCloseAfter: %s\n", update.RunOnDemandCloseAfter)
	}
	return builder.String()
}

func yamlScalar(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func writeConfigFile(path, content string) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".mediamtx-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary MediaMTX configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o660); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(content); err != nil {
		temporary.Close()
		return fmt.Errorf("write MediaMTX configuration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace MediaMTX configuration: %w", err)
	}
	return nil
}
