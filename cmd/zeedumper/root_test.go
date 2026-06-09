package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	cmd := newRootCmd()

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	if !strings.Contains(out.String(), "zeedumper") {
		t.Errorf("version output = %q", out.String())
	}
}

func TestRejectsPositionalArgs(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"unexpected-arg"})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error for positional args, got nil")
	}
}

func TestRejectsBadOutputFormat(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	// Point at a nonexistent kubeconfig so we never touch a real cluster; the
	// format is validated before the client is built.
	cmd.SetArgs([]string{"--output", "yaml", "--kubeconfig", "/nonexistent"})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error for bad output format, got nil")
	}
}
