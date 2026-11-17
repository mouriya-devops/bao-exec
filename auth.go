package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// authenticate returns a token for the configured auth method. A failure here
// is always an auth-category error: no command has been started yet.
func authenticate(cfg config, c *client) (string, error) {
	switch cfg.auth {
	case authToken:
		return cfg.token, nil
	case authApprole:
		roleID, err := readFileValue(cfg.roleIDFile)
		if err != nil {
			return "", fail(catConfig, "cannot read BAO_ROLE_ID_FILE: %v", err)
		}
		secretID, err := readFileValue(cfg.secretIDFile)
		if err != nil {
			return "", fail(catConfig, "cannot read BAO_SECRET_ID_FILE: %v", err)
		}
		if roleID == "" || secretID == "" {
			return "", fail(catConfig, "approle credential files are empty")
		}
		payload, err := json.Marshal(map[string]string{"role_id": roleID, "secret_id": secretID})
		if err != nil {
			return "", fail(catInternal, "cannot encode approle login request")
		}
		return login(c, "/v1/auth/approle/login", payload, "approle")
	case authGitHubActions:
		jwt, err := actionsJWT(cfg.actionsTokenURL, cfg.actionsRequestTok)
		if err != nil {
			return "", err
		}
		payload, err := json.Marshal(map[string]string{"role": cfg.actionsRole, "jwt": jwt})
		if err != nil {
			return "", fail(catInternal, "cannot encode jwt-github login request")
		}
		return login(c, "/v1/auth/jwt-github/login", payload, "jwt-github")
	default:
		return "", fail(catConfig, "unsupported auth method")
	}
}

// login posts the method-specific login payload and extracts the client token.
func login(c *client, path string, payload []byte, method string) (string, error) {
	resp, err := c.do(http.MethodPost, path, payload, "")
	if err != nil {
		return "", err
	}
	if resp.status >= 400 {
		return "", fail(catAuth, "%s login rejected with HTTP %d", method, resp.status)
	}
	var parsed struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(resp.body, &parsed); err != nil || parsed.Auth.ClientToken == "" {
		return "", fail(catAuth, "%s login returned no client token", method)
	}
	return parsed.Auth.ClientToken, nil
}

// actionsJWT asks the Actions token endpoint for a JWT with the audience the
// OpenBao jwt-github mount expects.
func actionsJWT(endpoint, requestToken string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fail(catConfig, "ACTIONS_ID_TOKEN_REQUEST_URL is not a valid URL")
	}
	query := parsed.Query()
	query.Set("audience", "openbao")
	parsed.RawQuery = query.Encode()

	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fail(catConfig, "ACTIONS_ID_TOKEN_REQUEST_URL is not a valid URL")
	}
	req.Header.Set("Authorization", "bearer "+requestToken)
	req.Header.Set("Accept", "application/json; api-version=2.0")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", fail(catAuth, "cannot reach the Actions token endpoint")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fail(catAuth, "Actions token endpoint returned HTTP %d", resp.StatusCode)
	}
	var parsedBody struct {
		Value string `json:"value"`
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&parsedBody); err != nil || strings.TrimSpace(parsedBody.Value) == "" {
		return "", fail(catAuth, "Actions token endpoint returned no token")
	}
	return parsedBody.Value, nil
}
