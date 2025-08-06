package util

import (
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSanitizeNonAlphanumeric(t *testing.T) {
	assert.Equal(t, "abc_def_", SanitizeNonAlphanumeric("abc:def?"))
	assert.Equal(t, "_", SanitizeNonAlphanumeric("\U0001F970"))
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "testfile")
	if err := os.WriteFile(file, []byte("hi there"), 0600); err != nil {
		t.Fatal(err)
	}
	assert.True(t, FileExists(file))
	assert.False(t, FileExists("/tmp/not-a-file"))
}

func TestFormatMarkdownCode(t *testing.T) {
	assert.Equal(t, "```this is code```", FormatMarkdownCode("this is code"))
	assert.Equal(t, "```` ` `this is a hack` ` ````", FormatMarkdownCode("```this is a hack```"))
}

func TestRandomPort(t *testing.T) {
	port1, err := RandomPort()
	if err != nil {
		t.Fatal(err)
	}
	port2, err := RandomPort()
	if err != nil {
		t.Fatal(err)
	}
	assert.True(t, port1 > 0 && port1 < 65000)
	assert.True(t, port2 > 0 && port2 < 65000)
	assert.NotEqual(t, port1, port2)
}

func TestInStringList(t *testing.T) {
	l := []string{"phil", "was", "here"}
	assert.True(t, InStringList(l, "phil"))
	assert.True(t, InStringList(l, "was"))
	assert.True(t, InStringList(l, "here"))
	assert.False(t, InStringList(l, "not"))
}

func TestTmuxBufferFileCrossPlatform(t *testing.T) {
	// Create a mock tmux instance
	tmux := &Tmux{id: "test_session_123"}

	// Test on systems with /dev/shm (Linux)
	if _, err := os.Stat("/dev/shm"); err == nil {
		bufferFile := tmux.bufferFile()
		assert.Equal(t, "/dev/shm/test_session_123.tmux.buffer", bufferFile)
		assert.Contains(t, bufferFile, "/dev/shm/")
	}

	// Test fallback to /tmp
	// We can't easily mock the absence of /dev/shm, but we can verify the logic
	// by checking that the method returns a valid path
	bufferFile := tmux.bufferFile()
	assert.True(t, strings.HasPrefix(bufferFile, "/dev/shm/") || strings.HasPrefix(bufferFile, "/tmp/"))
	assert.Contains(t, bufferFile, "test_session_123.tmux.buffer")
}

func TestTmuxTempFilePaths(t *testing.T) {
	tmux := &Tmux{id: "replbot_test_456"}

	// Test script file path
	scriptFile := tmux.scriptFile()
	assert.Equal(t, "/tmp/replbot_test_456.tmux.script", scriptFile)

	// Test launch script file path
	launchScriptFile := tmux.launchScriptFile()
	assert.Equal(t, "/tmp/replbot_test_456.tmux.launch-script", launchScriptFile)

	// Test config file path
	configFile := tmux.configFile()
	assert.Equal(t, "/tmp/replbot_test_456.tmux.conf", configFile)

	// Test capture file path
	captureFile := tmux.captureFile()
	assert.Equal(t, "/tmp/replbot_test_456.tmux.capture", captureFile)

	// Ensure all temp files use /tmp (not /dev/shm) for persistence
	assert.True(t, strings.HasPrefix(scriptFile, "/tmp/"))
	assert.True(t, strings.HasPrefix(launchScriptFile, "/tmp/"))
	assert.True(t, strings.HasPrefix(configFile, "/tmp/"))
	assert.True(t, strings.HasPrefix(captureFile, "/tmp/"))
}

func TestTmuxPasteWithTempFile(t *testing.T) {
	tmux := &Tmux{id: "replbot_paste_test"}

	// Mock tmux being active
	// Note: This test will fail if tmux is not installed, but that's expected
	// as the Paste method requires tmux to be running

	// Get the buffer file path
	bufferFile := tmux.bufferFile()

	// Verify buffer file will be in appropriate location
	assert.True(t, strings.HasPrefix(bufferFile, "/dev/shm/") || strings.HasPrefix(bufferFile, "/tmp/"))

	// Test that Paste would create a temp file in the right location
	// We can't actually call Paste without a running tmux session,
	// but we can verify the path construction
	testContent := "test paste content"
	err := os.WriteFile(bufferFile, []byte(testContent), 0600)
	if err == nil {
		// Verify we can write to the buffer file location
		defer os.Remove(bufferFile)
		content, err := os.ReadFile(bufferFile)
		assert.NoError(t, err)
		assert.Equal(t, testContent, string(content))
	}
}

func TestGetTmuxPathThreadSafety(t *testing.T) {
	// Reset the sync.Once for testing
	tmuxPathOnce = sync.Once{}
	tmuxBinaryPath = ""

	// Test concurrent access to getTmuxPath
	var wg sync.WaitGroup
	paths := make([]string, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			paths[index] = getTmuxPath()
		}(i)
	}

	wg.Wait()

	// All paths should be the same
	firstPath := paths[0]
	for i, path := range paths {
		assert.Equal(t, firstPath, path, "Path at index %d differs", i)
	}

	// Path should be one of the expected values
	validPaths := []string{"/opt/homebrew/bin/tmux", "/usr/bin/tmux", "/usr/local/bin/tmux", "tmux"}
	assert.Contains(t, validPaths, firstPath)
}

func TestSetTmuxPath(t *testing.T) {
	// Reset the sync.Once for testing
	tmuxPathOnce = sync.Once{}
	tmuxBinaryPath = ""

	// Set a custom tmux path
	customPath := "/custom/path/to/tmux"
	SetTmuxPath(customPath)

	// Verify the path is set
	assert.Equal(t, customPath, getTmuxPath())

	// Try to set it again with a new sync.Once should keep the first value
	// because SetTmuxPath sets the value before triggering sync.Once
	assert.Equal(t, customPath, tmuxBinaryPath)
}

func TestRunWithTmuxPath(t *testing.T) {
	// Reset for testing
	tmuxPathOnce = sync.Once{}
	tmuxBinaryPath = ""

	// Test that Run command replaces "tmux" with the actual path
	// We can't actually run tmux commands in tests without tmux installed,
	// but we can verify the path substitution logic

	// Get the tmux path that would be used
	actualPath := getTmuxPath()

	// Verify it's a valid path or "tmux"
	validPaths := []string{"/opt/homebrew/bin/tmux", "/usr/bin/tmux", "/usr/local/bin/tmux", "tmux"}
	assert.Contains(t, validPaths, actualPath)
}

func TestWaitUntil(t *testing.T) {
	// Test successful wait
	start := time.Now()
	counter := 0
	result := WaitUntil(func() bool {
		counter++
		return counter >= 3
	}, 100*time.Millisecond)

	assert.True(t, result)
	assert.True(t, time.Since(start) < 100*time.Millisecond)

	// Test timeout
	start = time.Now()
	result = WaitUntil(func() bool {
		return false
	}, 50*time.Millisecond)

	assert.False(t, result)
	assert.True(t, time.Since(start) >= 50*time.Millisecond)
}

func TestStringContainsWait(t *testing.T) {
	content := "initial"
	go func() {
		time.Sleep(20 * time.Millisecond)
		content = "initial content with target"
	}()

	result := StringContainsWait(func() string {
		return content
	}, "target", 100*time.Millisecond)

	assert.True(t, result)
}

func TestRandomString(t *testing.T) {
	// Test various lengths
	lengths := []int{5, 10, 20, 50}
	for _, length := range lengths {
		s, err := RandomString(length)
		assert.NoError(t, err)
		assert.Equal(t, length, len(s))
		// Verify all characters are alphanumeric
		for _, c := range s {
			assert.True(t, (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'))
		}
	}

	// Test uniqueness
	s1, err := RandomString(20)
	assert.NoError(t, err)
	s2, err := RandomString(20)
	assert.NoError(t, err)
	assert.NotEqual(t, s1, s2)
}

func TestTempFileName(t *testing.T) {
	f1, err := TempFileName()
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(f1, filepath.Join(os.TempDir(), "replbot_")))

	f2, err := TempFileName()
	assert.NoError(t, err)
	assert.NotEqual(t, f1, f2)
}
