package bot

import (
	"errors"
	"github.com/stretchr/testify/assert"
	"heckel.io/replbot/config"
	"heckel.io/replbot/util"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionCustomShell(t *testing.T) {
	sess, conn := createSession(t, "enter-name")
	defer sess.ForceClose()
	assert.True(t, conn.MessageContainsWait("1", "REPL session started, @phil"))
	assert.True(t, conn.MessageContainsWait("2", "Enter name:"))

	sess.UserInput("phil", "Phil")
	assert.True(t, conn.MessageContainsWait("2", "Hello Phil!"))

	sess.UserInput("phil", "!help")
	assert.True(t, conn.MessageContainsWait("3", "Return key"))

	sess.UserInput("phil", "!c")
	assert.True(t, util.WaitUntilNot(sess.Active, maxWaitTime))
}

func TestBashShell(t *testing.T) {
	sess, conn := createSession(t, "bash")
	defer sess.ForceClose()

	dir := t.TempDir()
	sess.UserInput("phil", "cd "+dir)
	// Give bash some time to change directories in slower environments.
	time.Sleep(100 * time.Millisecond)

	sess.UserInput("phil", "vi hi.txt")
	assert.True(t, conn.MessageContainsWait("2", "~\n~\n~\n~"))
	// Allow vim to fully start before sending further commands. In some
	// containers, vim may need a moment to draw the initial screen.
	time.Sleep(200 * time.Millisecond)

	sess.UserInput("phil", "!n i")
	// Give vim a moment to enter insert mode before checking.
	time.Sleep(100 * time.Millisecond)
	// Vim's insert mode indicator varies slightly across versions, so we
	// check a couple of common patterns instead of relying on a single one.
	found := conn.MessageContainsWait("2", "-- INSERT --") ||
		conn.MessageContainsWait("2", "INSERT") ||
		conn.MessageContainsWait("2", "-- INSERT")
	assert.True(t, found, "Should detect vim insert mode")

	sess.UserInput("phil", "!n I'm writing this in vim.")
	sess.UserInput("phil", "!esc")
	// Give vim time to switch back to command mode after ESC.
	time.Sleep(100 * time.Millisecond)
	sess.UserInput("phil", "!n yyp")
	sess.UserInput("phil", ":wq\n")
	success := util.WaitUntil(func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "hi.txt"))
		if err != nil {
			t.Logf("Error reading file: %v", err)
			return false
		}
		content := string(b)
		expected := "I'm writing this in vim.\nI'm writing this in vim.\n"
		if content != expected {
			t.Logf("File content mismatch. Expected: %q, Got: %q", expected, content)
		}
		return content == expected
	}, 3*time.Second)
	assert.True(t, success)

	sess.UserInput("phil", "!q")
	assert.True(t, util.WaitUntilNot(sess.Active, maxWaitTime))
}

func TestSessionCommands(t *testing.T) {
	sess, conn := createSession(t, "bash")
	defer sess.ForceClose()

	dir := t.TempDir()
	sess.UserInput("phil", "!e echo \"Phil\\bL\\nwas here\\ris here\"\\n")
	assert.True(t, conn.MessageContainsWait("2", "PhiL\n> was here\n> is here")) // The terminal converts \r to \n, whoa!

	sess.UserInput("phil", "!n echo this")
	sess.UserInput("phil", "is it")
	assert.True(t, conn.MessageContainsWait("2", "thisis it"))

	sess.UserInput("phil", "!s")
	sess.UserInput("phil", "echo hi there")
	assert.True(t, conn.MessageContainsWait("3", "hi there"))

	sess.UserInput("phil", "cd "+dir)
	sess.UserInput("phil", "touch test-b test-a test-c test-d")
	sess.UserInput("phil", "!n ls test-")
	sess.UserInput("phil", "!tt")
	assert.True(t, conn.MessageContainsWait("3", "test-a  test-b  test-c  test-d"))

	sess.UserInput("phil", "!c")
	sess.UserInput("phil", "!d")
	assert.True(t, util.WaitUntilNot(sess.Active, maxWaitTime))
}

func TestWriteShareClientScriptNotShare(t *testing.T) {
	sess, _ := createSession(t, "bash")
	defer sess.ForceClose()
	err := sess.WriteShareClientScript(io.Discard)
	var se *SessionError
	assert.True(t, errors.As(err, &se))
	assert.Equal(t, "NOT_SHARE_SESSION", se.Code)
}

func TestSessionResize(t *testing.T) {
	// FIXME stty size reports 39 99, why??

	/*
		sess, conn := createSession(t, testBashREPL)
		defer sess.ForceClose()

		sess.UserInput("phil", "stty size")
		assert.True(t, conn.MessageContainsWait("3", "24 80"))
		conn.LogMessages()

		time.Sleep(time.Second)
		sess.UserInput("phil", "!resize large")
		sess.UserInput("phil", "stty size")
		assert.True(t, conn.MessageContainsWait("3", "100 30"))

		sess.UserInput("phil", "!d")
		assert.True(t, util.WaitUntilNot(sess.Active, time.Second))
	*/
}

func createSession(t *testing.T, script string) (*session, *memConn) {
	conf := createConfig(t)
	conn := newMemConn(conf)
	randID, err := util.RandomString(5)
	if err != nil {
		t.Fatal(err)
	}
	sconfig := &sessionConfig{
		global:      conf,
		id:          "sess_" + randID,
		user:        "phil",
		control:     &channelID{"channel", "thread"},
		terminal:    &channelID{"channel", ""},
		script:      conf.Script(script),
		controlMode: config.Split,
		windowMode:  config.Full,
		authMode:    config.Everyone,
		size:        config.Small,
	}
	sess := newSession(sconfig, conn)
	go sess.Run()
	return sess, conn
}
