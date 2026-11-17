package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRunArgs(t *testing.T) {
	paths, command, err := parseRunArgs([]string{"--path", "kv/a/b/runtime", "--path=kv/a/b/ci", "--", "sh", "-c", "true"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 2 || paths[0] != "kv/a/b/runtime" || paths[1] != "kv/a/b/ci" {
		t.Fatalf("paths = %v", paths)
	}
	if strings.Join(command, " ") != "sh -c true" {
		t.Fatalf("command = %v", command)
	}
}

func TestParseRunArgsRejectsMissingCommand(t *testing.T) {
	_, _, err := parseRunArgs([]string{"--path", "kv/a/b/runtime", "--"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "no command") {
		t.Fatalf("error = %v", err)
	}
}

func TestMergedEnvironmentOverridesParent(t *testing.T) {
	t.Setenv("BAO_EXEC_TEST_VAR", "old")
	environment := mergedEnvironment(map[string]string{"BAO_EXEC_TEST_VAR": "new", "ADDED": "1"})
	values := make(map[string]string)
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
	}
	if values["BAO_EXEC_TEST_VAR"] != "new" {
		t.Fatalf("override = %q", values["BAO_EXEC_TEST_VAR"])
	}
	if values["ADDED"] != "1" {
		t.Fatalf("added = %q", values["ADDED"])
	}
}

// fakeKV serves the KV v2 read API for the given paths.
func fakeKV(t *testing.T, payloads map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := payloads[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":["no such path"]}`))
			return
		}
		body, err := json.Marshal(map[string]any{
			"data": map[string]any{
				"data":     secret,
				"metadata": map[string]any{"version": 1, "deletion_time": ""},
			},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestReadFieldsMergesAndRejectsDuplicates(t *testing.T) {
	server := fakeKV(t, map[string]any{
		"/v1/kv/data/t/a/runtime": map[string]any{"A": "alpha", "B": "two words"},
		"/v1/kv/data/t/a/ci":      map[string]any{"A": "second-value"},
	})
	client := newClient([]string{server.URL})

	fields, err := readFields(client, "token", []string{"kv/t/a/runtime"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fields["A"] != "alpha" || fields["B"] != "two words" {
		t.Fatalf("fields = %v", fields)
	}

	_, err = readFields(client, "token", []string{"kv/t/a/runtime", "kv/t/a/ci"})
	if err == nil || !strings.Contains(err.Error(), `"A" is defined by more than one path`) {
		t.Fatalf("overlap error = %v", err)
	}
	if strings.Contains(err.Error(), "alpha") || strings.Contains(err.Error(), "second-value") {
		t.Fatalf("error leaked a value: %v", err)
	}
}

func TestReadFieldsRejectsInvalidAndNonString(t *testing.T) {
	server := fakeKV(t, map[string]any{
		"/v1/kv/data/t/a/bad": map[string]any{"1X": "v"},
		"/v1/kv/data/t/a/num": map[string]any{"N": 3},
	})
	client := newClient([]string{server.URL})

	_, err := readFields(client, "token", []string{"kv/t/a/bad"})
	if err == nil || !strings.Contains(err.Error(), "invalid field name") {
		t.Fatalf("invalid name error = %v", err)
	}
	_, err = readFields(client, "token", []string{"kv/t/a/num"})
	if err == nil || !strings.Contains(err.Error(), "is not a string") {
		t.Fatalf("non-string error = %v", err)
	}
}

func TestReadFieldsReportsMissingPath(t *testing.T) {
	server := fakeKV(t, nil)
	client := newClient([]string{server.URL})
	_, err := readFields(client, "token", []string{"kv/t/a/none"})
	if err == nil || !strings.Contains(err.Error(), "kv/t/a/none: not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestAddressRotation(t *testing.T) {
	good := fakeKV(t, map[string]any{"/v1/kv/data/t/a/runtime": map[string]any{"A": "alpha"}})
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()

	// Connection failure: an unused port on loopback.
	client := newClient([]string{"http://127.0.0.1:1", good.URL})
	if _, err := readFields(client, "token", []string{"kv/t/a/runtime"}); err != nil {
		t.Fatalf("connection failure should rotate: %v", err)
	}

	// 5xx: a node answering 503 must hand over to the next address.
	client = newClient([]string{failing.URL, good.URL})
	if _, err := readFields(client, "token", []string{"kv/t/a/runtime"}); err != nil {
		t.Fatalf("5xx should rotate: %v", err)
	}

	// 4xx: a denial must not rotate to a node that would accept it.
	denying := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer denying.Close()
	client = newClient([]string{denying.URL, good.URL})
	if _, err := readFields(client, "token", []string{"kv/t/a/runtime"}); err == nil {
		t.Fatal("a 4xx response must fail without rotating")
	}
}

func TestAddressRotationAllUnavailable(t *testing.T) {
	client := newClient([]string{"http://127.0.0.1:1", "http://127.0.0.1:2"})
	_, err := readFields(client, "token", []string{"kv/t/a/runtime"})
	if err == nil || !strings.Contains(err.Error(), "addresses:") {
		t.Fatalf("error = %v", err)
	}
}

func TestTokenAuthFallsBackToFile(t *testing.T) {
	t.Setenv("BAO_ADDR", "http://127.0.0.1:1")
	t.Setenv("BAO_EXEC_AUTH", "")
	t.Setenv("BAO_TOKEN", "")
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BAO_TOKEN_PATH", tokenPath)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.token != "file-token" {
		t.Fatalf("token = %q", cfg.token)
	}
}

func TestApproleLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/approle/login" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["role_id"] != "role" || body["secret_id"] != "secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"auth":{"client_token":"client-token"}}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	roleID := filepath.Join(dir, "role_id")
	secretID := filepath.Join(dir, "secret_id")
	_ = os.WriteFile(roleID, []byte("role\n"), 0o600)
	_ = os.WriteFile(secretID, []byte("secret\n"), 0o600)

	t.Setenv("BAO_ADDR", server.URL)
	t.Setenv("BAO_EXEC_AUTH", "approle")
	t.Setenv("BAO_ROLE_ID_FILE", roleID)
	t.Setenv("BAO_SECRET_ID_FILE", secretID)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	token, err := authenticate(cfg, newClient(cfg.addresses))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if token != "client-token" {
		t.Fatalf("token = %q", token)
	}

	_ = os.WriteFile(secretID, []byte("wrong\n"), 0o600)
	cfg, _ = loadConfig()
	_, err = authenticate(cfg, newClient(cfg.addresses))
	if err == nil || !strings.Contains(err.Error(), "auth:") {
		t.Fatalf("error = %v", err)
	}
}

func TestGitHubActionsLogin(t *testing.T) {
	var sawAudience string
	actions := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAudience = r.URL.Query().Get("audience")
		if r.Header.Get("Authorization") != "bearer request-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"value":"test-jwt"}`))
	}))
	defer actions.Close()

	bao := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["role"] != "ci-t-a" || body["jwt"] != "test-jwt" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"auth":{"client_token":"ci-token"}}`))
	}))
	defer bao.Close()

	t.Setenv("BAO_ADDR", bao.URL)
	t.Setenv("BAO_EXEC_AUTH", "github-actions")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", actions.URL+"/token?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")
	t.Setenv("BAO_EXEC_ROLE", "ci-t-a")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	token, err := authenticate(cfg, newClient(cfg.addresses))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if token != "ci-token" {
		t.Fatalf("token = %q", token)
	}
	if sawAudience != "openbao" {
		t.Fatalf("audience = %q", sawAudience)
	}
}

func TestConfigRejectsUnknownAuth(t *testing.T) {
	t.Setenv("BAO_ADDR", "http://127.0.0.1:1")
	t.Setenv("BAO_EXEC_AUTH", "kerberos")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "BAO_EXEC_AUTH") {
		t.Fatalf("error = %v", err)
	}
}

func TestErrorsNeverContainValues(t *testing.T) {
	server := fakeKV(t, map[string]any{
		"/v1/kv/data/t/a/bad": map[string]any{"1X": "secret-value"},
	})
	client := newClient([]string{server.URL})
	_, err := readFields(client, "token", []string{"kv/t/a/bad"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(fmt.Sprint(err), "secret-value") {
		t.Fatalf("error leaked a value: %v", err)
	}
}
