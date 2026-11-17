package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var fieldNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// readFields reads every path at its current KV v2 version and merges the
// fields. A field appearing in two paths, an invalid name or a non-string
// value stops the run before any command starts.
func readFields(c *client, token string, paths []string) (map[string]string, error) {
	fields := make(map[string]string)
	origin := make(map[string]string)
	for _, path := range paths {
		pathFields, err := readPath(c, token, path)
		if err != nil {
			return nil, err
		}
		for name, value := range pathFields {
			if !fieldNamePattern.MatchString(name) {
				return nil, fail(catField, "%s: invalid field name %q", path, name)
			}
			if previous, seen := origin[name]; seen {
				return nil, fail(catField, "%q is defined by more than one path (%s and %s)", name, previous, path)
			}
			origin[name] = path
			fields[name] = value
		}
	}
	return fields, nil
}

// readPath fetches one KV v2 path. The first segment of the path is the mount
// and the remainder is the secret path inside it.
func readPath(c *client, token, path string) (map[string]string, error) {
	trimmed := strings.Trim(path, "/")
	segments := strings.Split(trimmed, "/")
	if len(trimmed) == 0 || len(segments) < 3 {
		return nil, fail(catConfig, "path %q must look like <mount>/<owner>/<repo>/<group>", path)
	}
	var escaped []string
	for _, segment := range segments[1:] {
		escaped = append(escaped, url.PathEscape(segment))
	}
	apiPath := "/v1/" + segments[0] + "/data/" + strings.Join(escaped, "/")

	resp, err := c.do(http.MethodGet, apiPath, nil, token)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.status == http.StatusNotFound:
		return nil, fail(catPath, "%s: not found", path)
	case resp.status == http.StatusForbidden || resp.status == http.StatusUnauthorized:
		return nil, fail(catPath, "%s: not permitted", path)
	case resp.status >= 400:
		return nil, fail(catPath, "%s: HTTP %d", path, resp.status)
	}

	var parsed struct {
		Data struct {
			Data     map[string]any `json:"data"`
			Metadata struct {
				DeletionTime string `json:"deletion_time"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.body, &parsed); err != nil {
		return nil, fail(catPath, "%s: response is not valid JSON", path)
	}
	if parsed.Data.Data == nil || parsed.Data.Metadata.DeletionTime != "" {
		return nil, fail(catPath, "%s: not found", path)
	}

	fields := make(map[string]string, len(parsed.Data.Data))
	for name, raw := range parsed.Data.Data {
		value, ok := raw.(string)
		if !ok {
			return nil, fail(catField, "%s: field %q is not a string", path, name)
		}
		fields[name] = value
	}
	return fields, nil
}
