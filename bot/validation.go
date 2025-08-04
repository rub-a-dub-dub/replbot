package bot

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const maxInputLength = 1000

var (
	dangerousPattern = regexp.MustCompile(`(\$\(|` + "`" + `|&&|\|\||;|\|)`)
)

// validateMessageLength ensures the message does not exceed the maximum length.
func validateMessageLength(msg string) error {
	if len(msg) > maxInputLength {
		return fmt.Errorf("input too long: %d characters", len(msg))
	}
	return nil
}

// validateCommand checks for dangerous command patterns and path traversal attempts.
func validateCommand(cmd string) error {
	if err := validateMessageLength(cmd); err != nil {
		return err
	}
	if dangerousPattern.MatchString(cmd) {
		return errors.New("dangerous command pattern detected")
	}
	if strings.Contains(cmd, "../") {
		return errors.New("path traversal detected")
	}
	return nil
}

// sanitizeCommand escapes special characters for shell execution.
func sanitizeCommand(cmd string) (string, error) {
	if err := validateCommand(cmd); err != nil {
		return "", err
	}
	replacer := strings.NewReplacer(
		"&", `\&`,
		";", `\;`,
		"|", `\|`,
		"$", `\$`,
		"`", "\\`",
	)
	return replacer.Replace(cmd), nil
}

// validateScriptName ensures the script name is allowed and does not contain path separators.
func validateScriptName(name string, whitelist []string) error {
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return errors.New("invalid script name")
	}
	for _, w := range whitelist {
		if name == w {
			return nil
		}
	}
	return fmt.Errorf("script %s not allowed", name)
}

// sanitizeFilePath cleans a file path and ensures it stays within the base directory.
func sanitizeFilePath(base, path string) (string, error) {
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return "", errors.New("path traversal detected")
	}
	if filepath.IsAbs(cleaned) {
		baseClean := filepath.Clean(base) + string(filepath.Separator)
		if !strings.HasPrefix(cleaned, baseClean) {
			return "", errors.New("path outside allowed directory")
		}
	}
	return cleaned, nil
}

// validateSessionConfig ensures the session configuration is safe.
func validateSessionConfig(conf *sessionConfig) error {
	if conf == nil {
		return errors.New("nil session config")
	}
	scriptName := filepath.Base(conf.script)
	if err := validateScriptName(scriptName, conf.global.Scripts()); err != nil {
		return err
	}
	sanitized, err := sanitizeFilePath(conf.global.ScriptDir, conf.script)
	if err != nil {
		return err
	}
	conf.script = sanitized
	return nil
}
