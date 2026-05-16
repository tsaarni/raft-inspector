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
	t *testing.T
	f *os.File
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

// H1 writes a level-1 heading.
func (d *Doc) H1(text string) { fmt.Fprintf(d.f, "# %s\n\n", text) }

// H2 writes a level-2 heading.
func (d *Doc) H2(text string) { fmt.Fprintf(d.f, "## %s\n\n", text) }

// H3 writes a level-3 heading.
func (d *Doc) H3(text string) { fmt.Fprintf(d.f, "### %s\n\n", text) }

// Text writes a prose paragraph.
func (d *Doc) Text(text string) { fmt.Fprintf(d.f, "%s\n\n", text) }

// Run executes one or more shell commands, writes them and their output as a single console block, and returns the combined output.
func (d *Doc) Run(commands ...string) string {
	d.t.Helper()

	var block strings.Builder
	var combined strings.Builder

	for _, command := range commands {
		cmd := exec.Command("sh", "-c", command)
		cmd.Env = os.Environ()

		out, err := cmd.CombinedOutput()
		output := stripANSI(string(out))

		if err != nil {
			d.t.Fatalf("command %q failed: %v\n%s", command, err, output)
		}

		block.WriteString("$ " + command + "\n")
		if output != "" {
			if !strings.HasSuffix(output, "\n") {
				output += "\n"
			}
			block.WriteString(output)
		}
		combined.WriteString(output)
	}

	fmt.Fprintf(d.f, "```console\n%s```\n\n", block.String())

	return combined.String()
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}
