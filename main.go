// Command bao-exec reads OpenBao KV v2 secrets and executes a child process
// with the fields injected as environment variables.
//
// Contract: mouriya-devops/homelab-tf#641 decision 6897333750 §8. Values and
// tokens never appear in argv, stdout or stderr.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const usage = `usage: bao-exec run --path <kv path> [--path <kv path>...] -- <command> [args...]`

// errorCategory is the class of failure; it decides the message prefix and
// nothing else. Keeping it explicit prevents a generic error string from
// hiding which stage failed.
type errorCategory string

const (
	catConfig   errorCategory = "config"
	catAuth     errorCategory = "auth"
	catPath     errorCategory = "path"
	catField    errorCategory = "field"
	catAddress  errorCategory = "addresses"
	catExec     errorCategory = "exec"
	catUsage    errorCategory = "usage"
	catInternal errorCategory = "internal"
)

type baoError struct {
	category errorCategory
	detail   string
}

func (e *baoError) Error() string { return string(e.category) + ": " + e.detail }

func fail(category errorCategory, format string, args ...any) *baoError {
	return &baoError{category: category, detail: fmt.Sprintf(format, args...)}
}

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		var be *baoError
		if errors.As(err, &be) {
			fmt.Fprintf(os.Stderr, "bao-exec: %s\n", be.Error())
		} else {
			fmt.Fprintf(os.Stderr, "bao-exec: internal: %v\n", err)
		}
		os.Exit(code)
	}
}

// run parses the command line, reads the requested paths and replaces the
// process with the child command. It returns only on failure.
func run(args []string) (int, error) {
	if len(args) == 0 {
		return 1, fail(catUsage, "%s", usage)
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, usage)
		return 0, nil
	}
	if args[0] != "run" {
		return 1, fail(catUsage, "unknown command %q; %s", args[0], usage)
	}

	paths, command, err := parseRunArgs(args[1:])
	if err != nil {
		return 1, err
	}

	cfg, err := loadConfig()
	if err != nil {
		return 1, err
	}

	client := newClient(cfg.addresses)
	token, err := authenticate(cfg, client)
	if err != nil {
		return 1, err
	}

	fields, err := readFields(client, token, paths)
	if err != nil {
		return 1, err
	}

	return execCommand(command, fields)
}

// parseRunArgs splits "--path <value>" repetitions from the child command that
// follows the "--" terminator.
func parseRunArgs(args []string) ([]string, []string, error) {
	var paths []string
	var command []string
	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "--":
			command = args[i+1:]
			i = len(args)
		case arg == "--path":
			if i+1 >= len(args) {
				return nil, nil, fail(catUsage, "--path needs a value")
			}
			paths = append(paths, args[i+1])
			i += 2
		case strings.HasPrefix(arg, "--path="):
			paths = append(paths, strings.TrimPrefix(arg, "--path="))
			i++
		default:
			return nil, nil, fail(catUsage, "unexpected argument %q before the command; %s", arg, usage)
		}
	}
	if len(paths) == 0 {
		return nil, nil, fail(catUsage, "at least one --path is required; %s", usage)
	}
	if len(command) == 0 {
		return nil, nil, fail(catUsage, "no command after --; %s", usage)
	}
	return paths, command, nil
}
