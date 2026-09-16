// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package config manages ~/.dxctl/config, a kubeconfig-shaped file: named
// clusters (server addresses), named contexts (a cluster + the business id
// to operate on — the --namespace analog), and a current context.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	APIVersion     string         `yaml:"apiVersion"`
	Kind           string         `yaml:"kind"`
	CurrentContext string         `yaml:"current-context"`
	Clusters       []NamedCluster `yaml:"clusters"`
	Users          []NamedUser    `yaml:"users"`
	Contexts       []NamedContext `yaml:"contexts"`
}

type NamedCluster struct {
	Name    string  `yaml:"name"`
	Cluster Cluster `yaml:"cluster"`
}

type Cluster struct {
	Server string `yaml:"server"`
	// Insecure disables TLS — for a local dev server with no certificate.
	// A real deployment, behind a TLS-terminating reverse proxy, leaves
	// this false.
	Insecure bool `yaml:"insecure,omitempty"`
}

// NamedUser holds one login's tokens. Plaintext-in-file-with-0600-perms is
// an acceptable V1/localhost shortcut; OS-keychain storage (the
// github.com/99designs/keyring library, what `gh` uses) is a flagged
// future upgrade once this ships somewhere credential theft is a real risk.
type NamedUser struct {
	Name string          `yaml:"name"`
	User UserCredentials `yaml:"user"`
}

type UserCredentials struct {
	RefreshToken      string    `yaml:"refresh-token,omitempty"`
	AccessToken       string    `yaml:"access-token,omitempty"`
	AccessTokenExpiry time.Time `yaml:"access-token-expiry,omitempty"`
}

type NamedContext struct {
	Name    string          `yaml:"name"`
	Context ContextSelector `yaml:"context"`
}

type ContextSelector struct {
	Cluster string `yaml:"cluster"`
	// User is empty for a context that never logged in — e.g. one running
	// against a dev-bypass server, where no token is needed at all.
	User     string `yaml:"user,omitempty"`
	Business int64  `yaml:"business"`
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".dxctl", "config"), nil
}

// Load reads ~/.dxctl/config, returning an empty (but valid) Config if the
// file doesn't exist yet.
func Load() (*Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{APIVersion: "dxctl/v1", Kind: "Config"}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

// Save writes the config back to ~/.dxctl/config with 0600 permissions.
func (c *Config) Save() error {
	path, err := DefaultPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// Current resolves the current context to a server address, whether that
// server should be dialed without TLS, and the business id to operate on.
func (c *Config) Current() (server string, insecure bool, businessID int64, err error) {
	if c.CurrentContext == "" {
		return "", false, 0, fmt.Errorf("no current context set; run `dxctl config set-context` and `dxctl config use-context`")
	}
	for _, nc := range c.Contexts {
		if nc.Name != c.CurrentContext {
			continue
		}
		for _, ncl := range c.Clusters {
			if ncl.Name == nc.Context.Cluster {
				return ncl.Cluster.Server, ncl.Cluster.Insecure, nc.Context.Business, nil
			}
		}
		return "", false, 0, fmt.Errorf("context %q references unknown cluster %q", nc.Name, nc.Context.Cluster)
	}
	return "", false, 0, fmt.Errorf("current context %q not found", c.CurrentContext)
}

// SetContext creates or replaces a named cluster+context pair in one step.
func (c *Config) SetContext(name, server string, insecure bool, businessID int64) {
	c.upsertCluster(name, server, insecure)

	for i, nc := range c.Contexts {
		if nc.Name == name {
			// Preserve an existing User link (set by `dxctl login`) —
			// re-running set-context to change the business shouldn't
			// silently log the context out.
			c.Contexts[i].Context.Cluster = name
			c.Contexts[i].Context.Business = businessID
			return
		}
	}
	c.Contexts = append(c.Contexts, NamedContext{
		Name:    name,
		Context: ContextSelector{Cluster: name, Business: businessID},
	})
}

func (c *Config) upsertCluster(name, server string, insecure bool) {
	for i, nc := range c.Clusters {
		if nc.Name == name {
			c.Clusters[i].Cluster.Server = server
			c.Clusters[i].Cluster.Insecure = insecure
			return
		}
	}
	c.Clusters = append(c.Clusters, NamedCluster{Name: name, Cluster: Cluster{Server: server, Insecure: insecure}})
}

// CurrentUserCredentials resolves the current context's user credentials.
// ok is false (not an error) if the context has no user set — e.g. one
// talking to a dev-bypass server, where no token is needed at all.
func (c *Config) CurrentUserCredentials() (creds UserCredentials, userName string, ok bool, err error) {
	if c.CurrentContext == "" {
		return UserCredentials{}, "", false, fmt.Errorf("no current context set; run `dxctl config set-context` and `dxctl config use-context`")
	}
	for _, nc := range c.Contexts {
		if nc.Name != c.CurrentContext {
			continue
		}
		if nc.Context.User == "" {
			return UserCredentials{}, "", false, nil
		}
		for _, nu := range c.Users {
			if nu.Name == nc.Context.User {
				return nu.User, nu.Name, true, nil
			}
		}
		return UserCredentials{}, "", false, fmt.Errorf("context %q references unknown user %q", nc.Name, nc.Context.User)
	}
	return UserCredentials{}, "", false, fmt.Errorf("current context %q not found", c.CurrentContext)
}

// SetUserCredentials creates or replaces a named user's stored tokens.
func (c *Config) SetUserCredentials(name string, creds UserCredentials) {
	for i, nu := range c.Users {
		if nu.Name == name {
			c.Users[i].User = creds
			return
		}
	}
	c.Users = append(c.Users, NamedUser{Name: name, User: creds})
}

// SetContextUser links a context to a named user (set by `dxctl login`).
func (c *Config) SetContextUser(contextName, userName string) error {
	for i, nc := range c.Contexts {
		if nc.Name == contextName {
			c.Contexts[i].Context.User = userName
			return nil
		}
	}
	return fmt.Errorf("context %q not found", contextName)
}

// UseContext sets the current context, failing if it doesn't exist.
func (c *Config) UseContext(name string) error {
	for _, nc := range c.Contexts {
		if nc.Name == name {
			c.CurrentContext = name
			return nil
		}
	}
	return fmt.Errorf("context %q not found", name)
}
