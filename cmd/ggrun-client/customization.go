package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/raketenkater/ggrun/clientkit"
)

// References only: MCP credentials remain in the user's original MCP file.
type customization struct {
	PluginDirs         []string `json:"plugin_dirs,omitempty"`
	MCPConfig          string   `json:"mcp_config,omitempty"`
	AppendSystemPrompt string   `json:"append_system_prompt,omitempty"`
	PermissionMode     string   `json:"permission_mode,omitempty"`
}

func customizationPath(name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("invalid connection name")
	}
	root, err := configRoot()
	return filepath.Join(root, "customizations", name+".json"), err
}

func (c customization) validate() error {
	switch c.PermissionMode {
	case "", "auto", "default", "acceptEdits", "plan":
	default:
		return fmt.Errorf("permission_mode must be auto, default, acceptEdits or plan")
	}
	for _, p := range append(append([]string{}, c.PluginDirs...), c.MCPConfig) {
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			return fmt.Errorf("plugin_dirs and mcp_config must use absolute paths")
		}
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("customization path unavailable: %w", err)
		}
	}
	for _, p := range c.PluginDirs {
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			return fmt.Errorf("plugin_dirs entries must be directories")
		}
	}
	if c.MCPConfig != "" {
		st, err := os.Stat(c.MCPConfig)
		if err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("mcp_config must reference a regular file")
		}
	}
	return nil
}

func importCustomization(name, file string) error {
	if _, err := load(name); err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() > 1<<20 {
		return fmt.Errorf("customization file must be at most 1 MiB")
	}
	var c customization
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return fmt.Errorf("invalid customization JSON: %w", err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("customization must contain one JSON object")
	}
	if err = c.validate(); err != nil {
		return err
	}
	path, err := customizationPath(name)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return clientkit.ReplacePrivate(path, b)
}

func customizedArgs(name string, extra []string) ([]string, error) {
	path, err := customizationPath(name)
	if err != nil {
		return nil, err
	}
	b, err := clientkit.ReadPrivate(path)
	if os.IsNotExist(err) {
		return extra, nil
	}
	if err != nil {
		return nil, err
	}
	var c customization
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return nil, fmt.Errorf("invalid saved customization")
	}
	if err = c.validate(); err != nil {
		return nil, err
	}
	args := []string{}
	// Explicit values override saved singleton/list options. Plugin directories
	// extend the saved list, with Claude retaining its supported CLI semantics.
	seen := map[string]bool{}
	for _, p := range c.PluginDirs {
		if !seen[p] {
			args = append(args, "--plugin-dir", p)
			seen[p] = true
		}
	}
	for _, v := range []struct{ flag, value string }{{"--mcp-config", c.MCPConfig}, {"--append-system-prompt", c.AppendSystemPrompt}, {"--permission-mode", c.PermissionMode}} {
		if v.value != "" && !hasFlag(extra, v.flag) {
			args = append(args, v.flag, v.value)
		}
	}
	return append(args, extra...), nil
}
