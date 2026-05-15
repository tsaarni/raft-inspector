//go:build e2e

package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// Doc writes a markdown document as test cases execute commands.
type Doc struct {
	t   *testing.T
	f   *os.File
	env []string // extra env vars for commands
}

// NewDoc creates a new markdown document at path.
func NewDoc(t *testing.T, path string) *Doc {
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating doc: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return &Doc{t: t, f: f}
}

// SetEnv sets environment variables that apply to subsequent Run calls.
func (d *Doc) SetEnv(env ...string) {
	d.env = env
}

// H1 writes a level-1 heading.
func (d *Doc) H1(text string) {
	fmt.Fprintf(d.f, "# %s\n\n", text)
}

// H2 writes a level-2 heading.
func (d *Doc) H2(text string) {
	fmt.Fprintf(d.f, "## %s\n\n", text)
}

// H3 writes a level-3 heading.
func (d *Doc) H3(text string) {
	fmt.Fprintf(d.f, "### %s\n\n", text)
}

// Text writes a prose paragraph.
func (d *Doc) Text(text string) {
	fmt.Fprintf(d.f, "%s\n\n", text)
}

// Run executes a shell command string via sh -c, writes the command and its output to the doc.
func (d *Doc) Run(command string) string {
	d.t.Helper()
	return d.run(command, true)
}

// RunMatch executes a command, writes to doc, and asserts output matches all patterns.
func (d *Doc) RunMatch(command string, patterns []string) string {
	d.t.Helper()
	out := d.Run(command)
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		if !re.MatchString(out) {
			d.t.Errorf("output of %q does not match pattern %q", command, p)
		}
	}
	return out
}

// RunFile executes a command and writes its output to a file.
// Rendered in the doc as: $ command > file
func (d *Doc) RunFile(command, path string) string {
	d.t.Helper()

	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), d.env...)

	out, err := cmd.CombinedOutput()
	output := stripANSI(string(out))

	if err != nil {
		d.t.Fatalf("command %q failed: %v\n%s", command, err, output)
	}

	if err := os.WriteFile(path, []byte(output), 0o600); err != nil {
		d.t.Fatalf("writing %s: %v", path, err)
	}

	fmt.Fprintf(d.f, "```console\n$ %s > %s\n```\n\n", command, path)

	return output
}

func (d *Doc) run(command string, showOutput bool) string {
	d.t.Helper()

	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), d.env...)

	out, err := cmd.CombinedOutput()
	output := stripANSI(string(out))

	if err != nil {
		d.t.Fatalf("command %q failed: %v\n%s", command, err, output)
	}

	if showOutput && output != "" {
		if !strings.HasSuffix(output, "\n") {
			output += "\n"
		}
		fmt.Fprintf(d.f, "```console\n$ %s\n%s```\n\n", command, output)
	} else {
		fmt.Fprintf(d.f, "```console\n$ %s\n```\n\n", command)
	}

	return output
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}
