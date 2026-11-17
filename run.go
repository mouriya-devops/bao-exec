package main

import (
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
)

// execCommand replaces the current process with the child command, so the
// child's exit code is bao-exec's exit code. The injected fields override
// same-named variables inherited from the parent environment.
func execCommand(command []string, fields map[string]string) (int, error) {
	binary, err := exec.LookPath(command[0])
	if err != nil {
		return 1, fail(catExec, "command %q not found", command[0])
	}

	environment := mergedEnvironment(fields)
	if err := syscall.Exec(binary, command, environment); err != nil {
		return 1, fail(catExec, "cannot execute %q: %v", command[0], err)
	}
	return 0, nil
}

// mergedEnvironment keeps the parent environment and replaces any variable
// whose name is provided by a field. The result is sorted for determinism.
func mergedEnvironment(fields map[string]string) []string {
	merged := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		merged[name] = value
	}
	for name, value := range fields {
		merged[name] = value
	}
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	environment := make([]string, 0, len(names))
	for _, name := range names {
		environment = append(environment, name+"="+merged[name])
	}
	return environment
}
