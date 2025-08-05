package config_test

import (
	"github.com/stretchr/testify/assert"
	"heckel.io/replbot/bot"
	confpkg "heckel.io/replbot/config"
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	dir := t.TempDir()
	script1 := filepath.Join(dir, "script1")
	script2 := filepath.Join(dir, "script2")
	if err := os.WriteFile(script1, []byte{}, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script2, []byte{}, 0700); err != nil {
		t.Fatal(err)
	}
	conf := confpkg.New("xoxb-slack-token", "xapp-slack-app-token")
	conf.UserToken = "xoxp-slack-user-token"
	conf.ScriptDir = dir
	platform, err := conf.Platform()
	assert.NoError(t, err)
	assert.Equal(t, confpkg.Slack, platform)
	assert.ElementsMatch(t, []string{"script1", "script2"}, conf.Scripts())
	assert.False(t, conf.ShareEnabled())
	assert.Empty(t, conf.Script("does-not-exist"))
	assert.Equal(t, script1, conf.Script("script1"))
	assert.Equal(t, script2, conf.Script("script2"))
}

func TestNewDiscordShareHost(t *testing.T) {
	conf := confpkg.New("not-slack", "")
	conf.ShareHost = "localhost:2586"
	conf.ScriptDir = "/does-not-exist"
	platform, err := conf.Platform()
	assert.NoError(t, err)
	assert.Equal(t, confpkg.Discord, platform)
	assert.Empty(t, conf.Scripts())
	assert.True(t, conf.ShareEnabled())
}

func TestSlackUserTokenRequired(t *testing.T) {
	conf := confpkg.New("xoxb-slack-token", "xapp-slack-app-token")
	_, err := conf.Platform()
	assert.Error(t, err)
	if cfgErr, ok := err.(*bot.ConfigError); ok {
		assert.Equal(t, "INVALID_SLACK_USER_TOKEN", cfgErr.Code)
	} else if cfgErr, ok := err.(*confpkg.ConfigError); ok {
		assert.Equal(t, "INVALID_SLACK_USER_TOKEN", cfgErr.Code)
	} else {
		t.Fatalf("expected ConfigError, got %T", err)
	}
}
