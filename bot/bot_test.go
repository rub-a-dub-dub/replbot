package bot

import (
	"archive/zip"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/rub-a-dub-dub/replbot/config"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	maxWaitTime = 5 * time.Second
)

var (
	testScripts = map[string]string{
		"enter-name": `#!/bin/bash
case "$1" in
  run)
    while true; do
      echo -n "Enter name: "
      read name
      echo "Hello $name!"
    done
    ;;
  *) ;;
esac
`,
		"bash": `#!/bin/bash
case "$1" in
  run)
    export PS1="$ "
    bash -i ;;
  *) ;;
esac
`,
	}
)

func TestBotIgnoreNonMentionsAndShowHelpMessage(t *testing.T) {
	conf := createConfig(t)
	robot, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = robot.Run() // Run robot in background, errors handled by test logic
	}()
	defer robot.Stop()
	conn := robot.conn.(*memConn)

	conn.Event(&messageEvent{
		ID:          "user-1",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "",
		User:        "phil",
		Message:     "This message should be ignored",
	})
	conn.Event(&messageEvent{
		ID:          "user-2",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "",
		User:        "phil",
		Message:     "@replbot",
	})
	assert.True(t, conn.MessageContainsWait("1", "Hi there"))
	assert.True(t, conn.MessageContainsWait("1", "I'm a robot for running interactive REPLs"))
	assert.NotContains(t, conn.Message("1").Message, "This message should be ignored")
}

func TestBotBashSplitMode(t *testing.T) {
	conf := createConfig(t)
	robot, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = robot.Run() // Run robot in background, errors handled by test logic
	}()
	defer robot.Stop()
	conn := robot.conn.(*memConn)

	conn.Event(&messageEvent{
		ID:          "user-1",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "",
		User:        "phil",
		Message:     "@replbot bash",
	})
	assert.True(t, conn.MessageContainsWait("1", "REPL session started, @phil"))
	// Accept either '$' or '#' as the initial shell prompt
	assert.True(t, conn.MessageContainsWait("2", "$") || conn.MessageContainsWait("2", "#"))

	conn.Event(&messageEvent{
		ID:          "user-2",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "user-1", // split mode!
		User:        "phil",
		Message:     "!e echo Phil\\bL was here",
	})
	assert.True(t, conn.MessageContainsWait("2", "PhiL was here"))
}

func TestBotBashDMChannelOnlyMeAllowDeny(t *testing.T) {
	conf := createConfig(t)
	robot, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = robot.Run() // Run robot in background, errors handled by test logic
	}()
	defer robot.Stop()
	conn := robot.conn.(*memConn)

	// Start in channel mode with "only-me"
	conn.Event(&messageEvent{
		ID:          "user-1",
		Channel:     "some-dm",
		ChannelType: channelTypeDM,
		Thread:      "",
		User:        "phil",
		Message:     "bash only-me channel", // no mention, because DM!
	})
	assert.True(t, conn.MessageContainsWait("1", "REPL session started, @phil"))
	assert.True(t, conn.MessageContainsWait("2", "$") || conn.MessageContainsWait("2", "#"))

	// Send message from someone that's not me to the channel
	conn.Event(&messageEvent{
		ID:          "user-2",
		Channel:     "some-dm",
		ChannelType: channelTypeChannel,
		Thread:      "",         // channel mode
		User:        "not-phil", // not phil!
		Message:     "echo i am not phil",
	})
	conn.Event(&messageEvent{
		ID:          "user-3",
		Channel:     "some-dm",
		ChannelType: channelTypeChannel,
		Thread:      "",     // channel mode
		User:        "phil", // phil
		Message:     "echo i am phil",
	})
	assert.True(t, conn.MessageContainsWait("2", "i am phil"))
	assert.NotContains(t, conn.Message("1").Message, "i am not phil")

	// Add "not-phil" to the allow list
	conn.Event(&messageEvent{
		ID:          "user-4",
		Channel:     "some-dm",
		ChannelType: channelTypeChannel,
		Thread:      "", // channel mode
		User:        "phil",
		Message:     "!allow @not-phil",
	})
	assert.True(t, conn.MessageContainsWait("3", "Okay, I added the user(s) to the allow list."))

	// Now "not-phil" can send commands
	conn.Event(&messageEvent{
		ID:          "user-5",
		Channel:     "some-dm",
		ChannelType: channelTypeChannel,
		Thread:      "",         // channel mode
		User:        "not-phil", // not phil!
		Message:     "echo i'm still not phil",
	})
	assert.True(t, conn.MessageContainsWait("2", "i'm still not phil"))
}

func TestBotBashWebTerminal(t *testing.T) {
	conf := createConfig(t)
	conf.WebHost = "localhost:12123"
	conf.DefaultWeb = true
	robot, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = robot.Run() // Run robot in background, errors handled by test logic
	}()
	defer robot.Stop()
	conn := robot.conn.(*memConn)

	// Start in channel mode with web terminal
	conn.Event(&messageEvent{
		ID:          "user-1",
		Channel:     "some-channel",
		ChannelType: channelTypeChannel,
		Thread:      "",
		User:        "phil",
		Message:     "@replbot bash", // 'web' is not mentioned, it's set by default
	})
	assert.True(t, conn.MessageContainsWait("1", "REPL session started, @phil"))
	assert.True(t, conn.MessageContainsWait("1", "Everyone can also *view and control*"))
	assert.True(t, conn.MessageContainsWait("1", "http://localhost:12123/")) // web terminal URL
	assert.True(t, conn.MessageContainsWait("2", "$") || conn.MessageContainsWait("2", "#"))

	// Check that web terminal actually returns HTML
	for i := 0; ; i++ {
		urlRegex := regexp.MustCompile(`(http://[/:\w]+)`)
		matches := urlRegex.FindStringSubmatch(conn.Message("1").Message)
		webTerminalURL := matches[1]
		resp, err := http.Get(webTerminalURL)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "<html ") {
			break
		}
		if i == 5 {
			t.Fatal("unexpected response: '<html ' not contained in: " + string(body))
		}
		time.Sleep(time.Second)
	}
}

func TestBotBashRecording(t *testing.T) {
	conf := createConfig(t)
	conf.DefaultRecord = false
	robot, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = robot.Run() // Run robot in background, errors handled by test logic
	}()
	defer robot.Stop()
	conn := robot.conn.(*memConn)

	// Start in channel mode with 'record'
	conn.Event(&messageEvent{
		ID:          "msg-1",
		Channel:     "some-channel",
		ChannelType: channelTypeChannel,
		Thread:      "",
		User:        "phil",
		Message:     "@replbot bash record",
	})
	assert.True(t, conn.MessageContainsWait("1", "REPL session started, @phil"))
	assert.True(t, conn.MessageContainsWait("2", "$") || conn.MessageContainsWait("2", "#"))

	// Send a super hard math problem
	conn.Event(&messageEvent{
		ID:          "msg-2",
		Channel:     "some-channel",
		ChannelType: channelTypeChannel,
		Thread:      "msg-1", // split mode
		User:        "phil",
		Message:     "echo 860",
	})
	assert.True(t, conn.MessageContainsWait("2", "echo 860"))
	assert.True(t, conn.MessageContainsWait("2", "860"))

	// Quit session
	conn.Event(&messageEvent{
		ID:          "msg-3",
		Channel:     "some-channel",
		ChannelType: channelTypeChannel,
		Thread:      "msg-1", // split mode
		User:        "phil",
		Message:     "exit",
	})
	assert.True(t, conn.MessageContainsWait("3", "REPL exited. You can find a recording of the session in the file below."))
	assert.NotNil(t, conn.Message("3").File)

	file := conn.Message("3").File
	zipFilename := filepath.Join(t.TempDir(), "recording.zip")
	if err := os.WriteFile(zipFilename, file, 0700); err != nil {
		t.Fatal(err)
	}

	targetDir := t.TempDir()
	if err := unzip(zipFilename, targetDir); err != nil {
		t.Fatal(err)
	}
	readme, _ := os.ReadFile(filepath.Join(targetDir, "REPLbot session", "README.md"))
	terminal, _ := os.ReadFile(filepath.Join(targetDir, "REPLbot session", "terminal.txt"))
	replay, _ := os.ReadFile(filepath.Join(targetDir, "REPLbot session", "replay.asciinema"))
	assert.Contains(t, string(readme), "This ZIP archive contains")
	assert.Contains(t, string(terminal), "echo 860")
	assert.Contains(t, string(terminal), "860")
	assert.Contains(t, string(replay), "echo 860")
	assert.Contains(t, string(replay), "860")
}

func createConfig(t *testing.T) *config.Config {
	tempDir := t.TempDir()
	for name, script := range testScripts {
		scriptFile := filepath.Join(tempDir, name)
		if err := os.WriteFile(scriptFile, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	conf := config.New("mem", "")
	conf.RefreshInterval = 30 * time.Millisecond
	conf.ScriptDir = tempDir
	conf.Debug = testing.Verbose() // Enable debug logging in verbose mode
	return conf
}

// unzip extract a zip archive
// from: https://stackoverflow.com/a/24792688/1440785
func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer func() {
		if err := r.Close(); err != nil {
			panic(err)
		}
	}()

	if err := os.MkdirAll(dest, 0755); err != nil {
		panic(err)
	}

	// Closure to address file descriptors issue with all the deferred .Close() methods
	extractAndWriteFile := func(f *zip.File) error {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer func() {
			if err := rc.Close(); err != nil {
				panic(err)
			}
		}()

		path := filepath.Join(dest, f.Name)

		// Check for ZipSlip (Directory traversal)
		if !strings.HasPrefix(path, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", path)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			defer func() {
				if err := f.Close(); err != nil {
					panic(err)
				}
			}()

			_, err = io.Copy(f, rc)
			if err != nil {
				return err
			}
		}
		return nil
	}

	for _, f := range r.File {
		err := extractAndWriteFile(f)
		if err != nil {
			return err
		}
	}

	return nil
}

func TestBotThreadMessageHandling(t *testing.T) {
	conf := createConfig(t)
	robot, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = robot.Run() // Run robot in background, errors handled by test logic
	}()
	defer robot.Stop()
	conn := robot.conn.(*memConn)

	// Start a bash session in split mode
	conn.Event(&messageEvent{
		ID:          "msg-1",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "",
		User:        "phil",
		Message:     "@replbot bash",
	})
	assert.True(t, conn.MessageContainsWait("1", "REPL session started, @phil"))
	assert.True(t, conn.MessageContainsWait("2", "$") || conn.MessageContainsWait("2", "#"))

	// Send a thread message with @replbot mention (simulating app_mention event)
	conn.Event(&messageEvent{
		ID:          "msg-2",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "msg-1",
		User:        "phil",
		Message:     "@replbot echo hello from thread",
	})
	assert.True(t, conn.MessageContainsWait("2", "echo hello from thread"))
	assert.True(t, conn.MessageContainsWait("2", "hello from thread"))

	// Test that more commands work in the thread
	conn.Event(&messageEvent{
		ID:          "msg-3",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "msg-1",
		User:        "phil",
		Message:     "@replbot pwd",
	})
	// Should execute the command
	assert.True(t, conn.MessageContainsWait("2", "pwd"))
}

func TestBotThreadMessageHandlingWithoutMention(t *testing.T) {
	conf := createConfig(t)
	robot, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = robot.Run() // Run robot in background, errors handled by test logic
	}()
	defer robot.Stop()
	conn := robot.conn.(*memConn)

	// Start a bash session in split mode
	conn.Event(&messageEvent{
		ID:          "msg-1",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "",
		User:        "phil",
		Message:     "@replbot bash",
	})
	assert.True(t, conn.MessageContainsWait("1", "REPL session started, @phil"))

	// Send a thread message without @replbot mention
	// This should still work in split mode when sent to the thread
	conn.Event(&messageEvent{
		ID:          "msg-2",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "msg-1",
		User:        "phil",
		Message:     "echo no mention needed",
	})
	assert.True(t, conn.MessageContainsWait("2", "echo no mention needed"))
	assert.True(t, conn.MessageContainsWait("2", "no mention needed"))
}

func TestBotChannelModeIgnoresThreadMessages(t *testing.T) {
	conf := createConfig(t)
	robot, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = robot.Run() // Run robot in background, errors handled by test logic
	}()
	defer robot.Stop()
	conn := robot.conn.(*memConn)

	// Start a bash session in channel mode
	conn.Event(&messageEvent{
		ID:          "msg-1",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "",
		User:        "phil",
		Message:     "@replbot bash channel", // Explicitly set channel mode
	})
	assert.True(t, conn.MessageContainsWait("1", "REPL session started, @phil"))
	assert.True(t, conn.MessageContainsWait("2", "$") || conn.MessageContainsWait("2", "#"))

	// Send a thread message - should be ignored in channel mode
	conn.Event(&messageEvent{
		ID:          "msg-2",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "msg-1", // Thread message
		User:        "phil",
		Message:     "@replbot echo this should be ignored",
	})

	// Send a channel message - should work
	conn.Event(&messageEvent{
		ID:          "msg-3",
		Channel:     "channel",
		ChannelType: channelTypeChannel,
		Thread:      "", // Channel message
		User:        "phil",
		Message:     "echo this should work",
	})

	// Verify only the channel message was processed
	assert.True(t, conn.MessageContainsWait("2", "this should work"))
	assert.NotContains(t, conn.Message("2").Message, "this should be ignored")
}
