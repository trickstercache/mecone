/*
 * Copyright 2026 The Trickster Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package cli implements the mecone command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

const (
	exitOK = iota
	exitFailure
	exitUsage
)

const (
	commandIndex = iota
	commandArgsIndex
)

const (
	usageHeader = "Mecone tests reverse proxies and caches for HTTP specification compliance.\n\n" +
		"usage: mecone <command> [flags]\n\ncommands:\n"
	usageFooter = "\nRun 'mecone <command> -h' for the flags of a command.\n"
)

var errUsage = errors.New("usage error")

type env struct {
	stdout io.Writer
	stderr io.Writer
}

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string, e *env) error
}

func commands() []command {
	return []command{
		{"origin", "run the origin role, behind the proxy under test", runOrigin},
		{"client", "run tests through the proxy under test against a remote origin", runClient},
		{"run", "run an origin and the client together in one process", runBoth},
		{"list", "list the available tests", runList},
		{"report", "score a results file and render a report", runReport},
		{"version", "print version information", runVersion},
	}
}

// Run executes the command line and returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) <= commandIndex {
		return status(exitUsage, write(stderr, appendUsage(nil)))
	}
	name := args[commandIndex]
	if isHelp(name) {
		return status(exitOK, write(stdout, appendUsage(nil)))
	}
	c, ok := lookup(name)
	if !ok {
		msg := fmt.Appendf(nil, "mecone: unknown command %q\n\n", name)
		return status(exitUsage, write(stderr, appendUsage(msg)))
	}
	err := c.run(ctx, args[commandArgsIndex:], &env{stdout: stdout, stderr: stderr})
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.Is(err, errUsage):
		// the mistake has already been explained to the user, so only the exit code is left to report
		return exitUsage
	default:
		return status(exitFailure, write(stderr, fmt.Appendf(nil, "mecone %s: %v\n", c.name, err)))
	}
}

func status(code int, writeErr error) int {
	// output that cannot be written fails an otherwise successful run; an existing failure code is kept
	if writeErr != nil && code == exitOK {
		return exitFailure
	}
	return code
}

func isHelp(arg string) bool {
	switch arg {
	case "help", "-h", "-help", "--help":
		return true
	default:
		return false
	}
}

func lookup(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func appendUsage(b []byte) []byte {
	b = append(b, usageHeader...)
	for _, c := range commands() {
		b = fmt.Appendf(b, "  %-8s %s\n", c.name, c.summary)
	}
	return append(b, usageFooter...)
}

func write(w io.Writer, b []byte) error {
	_, err := w.Write(b)
	return err
}

func newFlagSet(name string, e *env) *flag.FlagSet {
	flags := flag.NewFlagSet("mecone "+name, flag.ContinueOnError)
	flags.SetOutput(e.stderr)
	return flags
}

func parse(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		// the flag package has already printed the error and the usage
		return errUsage
	}
	return nil
}
