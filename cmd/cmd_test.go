package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// resetCmdFlags resets all flags in the command tree to their default values
// and clears the "Changed" state. This is necessary because cobra uses global
// command state that leaks between tests — e.g. a --help test leaves the help
// flag marked "changed", and flag values from one test carry into the next.
func resetCmdFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, child := range cmd.Commands() {
		resetCmdFlags(child)
	}
}

func executeCmd(args []string) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	resetCmdFlags(rootCmd)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf, err
}

func TestRootHelp(t *testing.T) {
	buf, err := executeCmd([]string{"--help"})
	if err != nil {
		t.Fatalf("root --help: %v", err)
	}
	out := buf.String()
	for _, sub := range []string{"master", "agent", "version"} {
		if !strings.Contains(out, sub) {
			t.Errorf("root help missing subcommand %q in output:\n%s", sub, out)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	// version uses fmt.Println which writes to os.Stdout, not cmd.OutOrStdout()
	// We just verify it doesn't error
	_, err := executeCmd([]string{"version"})
	if err != nil {
		t.Fatalf("version: %v", err)
	}
}

func TestMasterHelp(t *testing.T) {
	buf, err := executeCmd([]string{"master", "--help"})
	if err != nil {
		t.Fatalf("master --help: %v", err)
	}
	out := buf.String()
	for _, flag := range []string{"--config", "--listen", "--log-level"} {
		if !strings.Contains(out, flag) {
			t.Errorf("master help missing flag %q", flag)
		}
	}
}

func TestAgentHelp(t *testing.T) {
	buf, err := executeCmd([]string{"agent", "--help"})
	if err != nil {
		t.Fatalf("agent --help: %v", err)
	}
	out := buf.String()
	for _, flag := range []string{"--config", "--listen", "--master", "--enrollment-token"} {
		if !strings.Contains(out, flag) {
			t.Errorf("agent help missing flag %q", flag)
		}
	}
}

func TestInvalidCommand(t *testing.T) {
	_, err := executeCmd([]string{"nonexistent-command"})
	if err == nil {
		t.Error("expected error for invalid command")
	}
}

func TestMasterBadConfig(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bad.yaml"
	os.WriteFile(path, []byte("{{invalid yaml"), 0o644)

	_, err := executeCmd([]string{"master", "--config", path})
	if err == nil {
		t.Error("expected error for invalid config YAML")
	}
}

func TestMasterRejectsLegacyTopLevelListen(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/legacy-master.yaml"
	os.WriteFile(path, []byte("listen: ':9000'\nmaster:\n  jwt_secret: 'test-secret'\n"), 0o644)

	_, err := executeCmd([]string{"master", "--config", path})
	if err == nil {
		t.Fatal("expected error for legacy top-level listen")
	}
	if !strings.Contains(err.Error(), "use master.listen") {
		t.Fatalf("expected migration hint for master.listen, got %v", err)
	}
}

func TestAgentBadConfig(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bad.yaml"
	os.WriteFile(path, []byte("{{invalid yaml"), 0o644)

	_, err := executeCmd([]string{"agent", "--config", path})
	if err == nil {
		t.Error("expected error for invalid config YAML")
	}
}
