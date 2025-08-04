// Package config provides the main configuration for REPLbot
package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultIdleTimeout defines the default time after which a session is terminated
	DefaultIdleTimeout = 10 * time.Minute

	// DefaultMaxTotalSessions is the default number of sessions all users are allowed to run concurrently
	DefaultMaxTotalSessions = 6

	// DefaultMaxUserSessions is the default number of sessions a user is allowed to run concurrently
	DefaultMaxUserSessions = 2

	// DefaultRecord defines if sessions are recorded by default
	DefaultRecord = false

	// DefaultUploadRecording defines if session recording are uploaded to asciinema
	DefaultUploadRecording = false

	// DefaultWeb defines if sessions have a web terminal by default
	DefaultWeb = false

	// defaultRefreshInterval defines the interval at which the terminal refreshed
	defaultRefreshInterval = 200 * time.Millisecond

	// DefaultRateLimitCleanupInterval defines how often inactive rate limiters are cleaned up
	DefaultRateLimitCleanupInterval = 10 * time.Minute
)

// RateLimit defines a simple rate limit configuration
type RateLimit struct {
	Requests int
	Burst    int
	Interval time.Duration
}

// Config is the main config struct for the application. Use New to instantiate a default config struct.
type Config struct {
	Token                    string
	ScriptDir                string
	IdleTimeout              time.Duration
	MaxTotalSessions         int
	MaxUserSessions          int
	DefaultControlMode       ControlMode
	DefaultWindowMode        WindowMode
	DefaultAuthMode          AuthMode
	DefaultSize              *Size
	DefaultWeb               bool
	WebHost                  string
	ShareHost                string
	ShareKeyFile             string
	DefaultRecord            bool
	UploadRecording          bool
	Cursor                   time.Duration
	RefreshInterval          time.Duration
	Debug                    bool
	MessageRateLimit         RateLimit
	SessionRateLimit         RateLimit
	CommandRateLimit         RateLimit
	RateLimitCleanupInterval time.Duration
}

// New instantiates a default new config
func New(token string) *Config {
	return &Config{
		Token:                    token,
		IdleTimeout:              DefaultIdleTimeout,
		MaxTotalSessions:         DefaultMaxTotalSessions,
		MaxUserSessions:          DefaultMaxUserSessions,
		DefaultControlMode:       DefaultControlMode,
		DefaultWindowMode:        DefaultWindowMode,
		DefaultAuthMode:          DefaultAuthMode,
		DefaultSize:              DefaultSize,
		DefaultRecord:            DefaultRecord,
		DefaultWeb:               DefaultWeb,
		UploadRecording:          DefaultUploadRecording,
		RefreshInterval:          defaultRefreshInterval,
		RateLimitCleanupInterval: DefaultRateLimitCleanupInterval,
	}
}

// Platform returns the target connection type, based on the token
func (c *Config) Platform() Platform {
	if strings.HasPrefix(c.Token, "mem") {
		return Mem
	} else if strings.HasPrefix(c.Token, "xoxb-") {
		return Slack
	}
	return Discord
}

// ShareEnabled returns true if the share features is enabled
func (c *Config) ShareEnabled() bool {
	return c.ShareHost != ""
}

// Scripts returns the names of all available scripts
func (c *Config) Scripts() []string {
	scripts := make([]string, 0)
	for script := range c.scripts() {
		scripts = append(scripts, script)
	}
	return scripts
}

// Script returns the path to the script with the given name.
// If a script with the given name does not exist, the result may be empty.
func (c *Config) Script(name string) string {
	scripts := c.scripts()
	return scripts[name]
}

func (c *Config) scripts() map[string]string {
	scripts := make(map[string]string)
	entries, err := os.ReadDir(c.ScriptDir)
	if err != nil {
		return scripts
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			scripts[entry.Name()] = filepath.Join(c.ScriptDir, entry.Name())
		}
	}
	return scripts
}
