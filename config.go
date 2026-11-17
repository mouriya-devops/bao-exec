package main

import (
	"os"
	"path/filepath"
	"strings"
)

type authMethod string

const (
	authToken         authMethod = "token"
	authApprole       authMethod = "approle"
	authGitHubActions authMethod = "github-actions"
)

type config struct {
	addresses []string
	auth      authMethod

	token        string
	tokenPath    string
	roleIDFile   string
	secretIDFile string

	actionsTokenURL   string
	actionsRequestTok string
	actionsRole       string
}

// loadConfig reads the environment described by decision §8. Missing
// configuration is reported as such, never defaulted silently.
func loadConfig() (config, error) {
	var cfg config

	rawAddr := os.Getenv("BAO_ADDR")
	if strings.TrimSpace(rawAddr) == "" {
		return cfg, fail(catConfig, "BAO_ADDR is not set")
	}
	for _, part := range strings.Split(rawAddr, ",") {
		addr := strings.TrimSpace(part)
		if addr == "" {
			continue
		}
		cfg.addresses = append(cfg.addresses, strings.TrimRight(addr, "/"))
	}
	if len(cfg.addresses) == 0 {
		return cfg, fail(catConfig, "BAO_ADDR contains no usable address")
	}

	cfg.auth = authToken
	switch strings.TrimSpace(os.Getenv("BAO_EXEC_AUTH")) {
	case "", "token":
		cfg.auth = authToken
	case "approle":
		cfg.auth = authApprole
	case "github-actions":
		cfg.auth = authGitHubActions
	default:
		return cfg, fail(catConfig, "BAO_EXEC_AUTH must be token, approle or github-actions")
	}

	switch cfg.auth {
	case authToken:
		cfg.token = strings.TrimSpace(os.Getenv("BAO_TOKEN"))
		cfg.tokenPath = strings.TrimSpace(os.Getenv("BAO_TOKEN_PATH"))
		if cfg.token == "" && cfg.tokenPath == "" {
			if home, err := os.UserHomeDir(); err == nil {
				candidate := filepath.Join(home, ".vault-token")
				if _, statErr := os.Stat(candidate); statErr == nil {
					cfg.tokenPath = candidate
				}
			}
		}
		if cfg.token == "" && cfg.tokenPath == "" {
			return cfg, fail(catConfig, "token auth needs BAO_TOKEN, BAO_TOKEN_PATH or ~/.vault-token")
		}
		if cfg.token == "" {
			value, err := readFileValue(cfg.tokenPath)
			if err != nil {
				return cfg, fail(catConfig, "cannot read BAO_TOKEN_PATH: %v", err)
			}
			if value == "" {
				return cfg, fail(catConfig, "BAO_TOKEN_PATH is empty")
			}
			cfg.token = value
		}
	case authApprole:
		cfg.roleIDFile = strings.TrimSpace(os.Getenv("BAO_ROLE_ID_FILE"))
		cfg.secretIDFile = strings.TrimSpace(os.Getenv("BAO_SECRET_ID_FILE"))
		if cfg.roleIDFile == "" || cfg.secretIDFile == "" {
			return cfg, fail(catConfig, "approle auth needs BAO_ROLE_ID_FILE and BAO_SECRET_ID_FILE")
		}
	case authGitHubActions:
		cfg.actionsTokenURL = strings.TrimSpace(os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"))
		cfg.actionsRequestTok = strings.TrimSpace(os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
		cfg.actionsRole = strings.TrimSpace(os.Getenv("BAO_EXEC_ROLE"))
		if cfg.actionsTokenURL == "" || cfg.actionsRequestTok == "" {
			return cfg, fail(catConfig, "github-actions auth needs ACTIONS_ID_TOKEN_REQUEST_URL and ACTIONS_ID_TOKEN_REQUEST_TOKEN")
		}
		if cfg.actionsRole == "" {
			return cfg, fail(catConfig, "github-actions auth needs BAO_EXEC_ROLE")
		}
	}
	return cfg, nil
}

func readFileValue(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
