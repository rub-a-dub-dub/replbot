package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rub-a-dub-dub/replbot/config"
)

func TestValidateCommand(t *testing.T) {
	cases := []struct {
		cmd string
		ok  bool
	}{
		{"echo hello", true},
		{"ls && rm -rf /", false},
		{"`uname`", false},
		{"$(id)", false},
		{"ls | grep foo", false},
		{strings.Repeat("a", maxInputLength+1), false},
		{"../etc/passwd", false},
	}
	for _, c := range cases {
		_, err := sanitizeCommand(c.cmd)
		if c.ok && err != nil {
			t.Fatalf("expected command %q to be valid: %v", c.cmd, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("expected command %q to be invalid", c.cmd)
		}
	}
}

func TestValidateScriptName(t *testing.T) {
	whitelist := []string{"good.sh"}
	if err := validateScriptName("good.sh", whitelist); err != nil {
		t.Fatalf("expected script to be valid: %v", err)
	}
	if err := validateScriptName("../bad.sh", whitelist); err == nil {
		t.Fatalf("expected path traversal to be invalid")
	}
	if err := validateScriptName("bad.sh", whitelist); err == nil {
		t.Fatalf("expected script not in whitelist to be invalid")
	}
}

func TestValidateSessionConfig(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh"), 0700); err != nil {
		t.Fatalf("failed to create script: %v", err)
	}
	conf := &config.Config{ScriptDir: dir}
	sess := &sessionConfig{global: conf, script: script}
	if err := validateSessionConfig(sess); err != nil {
		t.Fatalf("expected session config to be valid: %v", err)
	}
	sess.script = filepath.Join(dir, "../evil.sh")
	if err := validateSessionConfig(sess); err == nil {
		t.Fatalf("expected session config with traversal to be invalid")
	}
}

func TestValidateMessageLength(t *testing.T) {
	if err := validateMessageLength(strings.Repeat("a", maxInputLength)); err != nil {
		t.Fatalf("expected message within limit to be valid: %v", err)
	}
	if err := validateMessageLength(strings.Repeat("a", maxInputLength+1)); err == nil {
		t.Fatalf("expected message exceeding limit to be invalid")
	}
}
