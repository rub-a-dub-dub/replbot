package bot

import (
	"archive/zip"
	"bufio"
	"context"
	_ "embed" // go:embed requires this
	"errors"
	"fmt"
	"github.com/rub-a-dub-dub/replbot/config"
	"github.com/rub-a-dub-dub/replbot/util"
	"github.com/tidwall/sjson"
	"golang.org/x/sync/errgroup"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"
	"unicode"
)

const (
	sessionStartedMessage = "🚀 REPL session started, %s. Type `!help` to see a list of available commands, or `!exit` to forcefully " +
		"exit the REPL."
	splitModeThreadMessage              = "Use this thread to enter your commands. Your output will appear in the main channel."
	onlyMeModeMessage                   = "*Only you as the session owner* can send commands. Use the `!allow` command to let other users control the session."
	everyoneModeMessage                 = "*Everyone in this channel* can send commands. Use the `!deny` command specifically revoke access from users."
	sessionExitedMessage                = "👋 REPL exited. See you later!"
	sessionExitedWithRecordingMessage   = "👋 REPL exited. You can find a recording of the session in the file below."
	sessionAsciinemaLinkMessage         = "Here's a link to the recording: %s"
	sessionAsciinemaExpiryMessage       = "(expires in %s)"
	timeoutWarningMessage               = "⏱️ Are you still there, %s? Your session will time out in one minute. Type `!alive` to keep your session active."
	forceCloseMessage                   = "🏃 REPLbot has to go. Urgent REPL-related business. Sorry about that!"
	resizeCommandHelpMessage            = "Use the `!resize` command to resize the terminal, like so: !resize medium.\n\nAllowed sizes are `tiny`, `small`, `medium` or `large`."
	messageLimitWarningMessage          = "Note that Discord has a message size limit of 2000 characters, so your messages may be truncated if they get to large."
	usersAddedToAllowList               = "👍 Okay, I added the user(s) to the allow list."
	usersAddedToDenyList                = "👍 Okay, I added the user(s) to the deny list."
	cannotAddOwnerToDenyList            = "🙁 I don't think adding the session owner to the deny list is a good idea. I must protest."
	recordingTooLargeMessage            = "🙁 I'm sorry, but you've produced too much output in this session. You may want to run a session with `norecord` to avoid this problem."
	shareStartCommandMessage            = "To start your terminal sharing session, please run the following command from your terminal:\n\n```bash -c \"$(ssh -T -p %s %s@%s $USER)\"```"
	sessionWithWebStartReadOnlyMessage  = "Everyone can also view the session via http://%s/%s. Use `!web rw` to switch the web terminal to read-write mode, or `!web off` to turn if off."
	sessionWithWebStartReadWriteMessage = "Everyone can also *view and control* the session via http://%s/%s. Use `!web ro` to switch the web terminal to read-only mode, or `!web off` to turn if off."
	allowCommandHelpMessage             = "To allow other users to interact with this session, use the `!allow` command like so: !allow %s\n\nYou may tag multiple users, or use the words " +
		"`everyone`/`all` to allow all users, or `nobody`/`only-me` to only yourself access."
	denyCommandHelpMessage = "To deny users from interacting with this session, use the `!deny` command like so: !deny %s\n\nYou may tag multiple users, or use the words " +
		"`everyone`/`all` to deny everyone (except yourself), like so: !deny all"
	noNewlineHelpMessage = "Use the `!n` command to send text without a newline character (`\\n`) at the end of the line, e.g. sending `!n ls`, will send `ls` and not `ls\\n`. " +
		"This is similar `echo -n` in a shell."
	escapeHelpMessage = "Use the `!e` command to interpret the escape sequences `\\n` (new line), `\\r` (carriage return), `\\t` (tab), `\\b` (backspace) and `\\x..` (hex " +
		"representation of any byte), e.g. `Hi\\bI` will show up as `HI`. This is is similar to `echo -e` in a shell."
	sendKeysHelpMessage = "Use any of the send-key commands (`!c`, `!esc`, ...) to send common keyboard shortcuts, e.g. `!d` to send Ctrl-D, or `!up` to send the up key.\n\n" +
		"You may also combine them in a sequence, like so: `!c-b d` (Ctrl-B + d), or `!up !up !down !down !left !right !left !right b a`."
	authModeChangeMessage   = "👍 Okay, I updated the auth mode: "
	sessionKeptAliveMessage = "I'm glad you're still here 😀"
	webStoppedMessage       = "👍 Okay, I stopped the web terminal."
	webIsReadOnlyMessage    = "The terminal is *read-only*. Use `!web rw` to change it to read-write, and `!web off` to turn if off completely."
	webIsWritableMessage    = "*Everyone in this channel* can write to this terminal. Use `!web ro` to change it to read-only, and `!web off` to turn if off completely."
	webEnabledMessage       = "The web terminal is available at http://%s/%s"
	webDisabledMessage      = "The web terminal is disabled."
	webHelpMessage          = "To enable it, simply type `!web rw` (read-write) or `!web ro` (read-only). Type `!web off` to turn if back off."
	webNotWorkingMessage    = "🙁 I'm sorry, but I can't start the web terminal for you."
	webNotSupportedMessage  = "🙁 I'm sorry, but the web terminal feature is not enabled."
	helpMessage             = "Alright, buckle up. Here's a list of all the things you can do in this REPL session.\n\n" +
		"Sending text:\n" +
		"  `TEXT` - Sends _TEXT\\n_\n" +
		"  `!n TEXT` - Sends _TEXT_ (no new line)\n" +
		"  `!e TEXT` - Sends _TEXT_ (interprets _\\n_, _\\r_, _\\t_, _\\b_ & _\\x.._)\n\n" +
		"Sending keys (can be combined):\n" +
		"  `!r` - Return key\n" +
		"  `!t`, `!tt` - Tab / double-tab\n" +
		"  `!up`, `!down`, `!left`, `!right` - Cursor\n" +
		"  `!pu`, `!pd` - Page up / page down\n" +
		"  `!a`, `!b`, `!c`, `!d`, `!c-..` - Ctrl-..\n" +
		"  `!esc`, `!space` - Escape/Space\n\n" +
		"  `!f1`, `!f2`, ... - F1, F2, ...\n\n" +
		"Other commands:\n" +
		"  `!! ..` - Comment, ignored entirely\n" +
		"  `!allow ..`, `!deny ..` - Allow/deny users\n" +
		"  `!web` - Start/stop web terminal\n" +
		"  `!resize ..` - Resize window\n" +
		"  `!screen`, `!s` - Re-send terminal\n" +
		"  `!alive` - Reset session timeout\n" +
		"  `!help`, `!h` - Show this help screen\n" +
		"  `!exit`, `!q` - Exit REPL"

	// updateMessageUserInputCountLimit is the max number of input messages before re-sending a new screen
	updateMessageUserInputCountLimit = 5

	recordingFileName    = "REPLbot session.zip"
	recordingFileType    = "application/zip"
	recordingFileSizeMax = 50 * 1024 * 1024

	scriptRunCommand  = "run"
	scriptKillCommand = "kill"
)

var (
	// sendKeysMapping is a translation table that translates input commands "!<command>" to something that can be
	// send via tmux's send-keys command, see https://man7.org/linux/man-pages/man1/tmux.1.html#KEY_BINDINGS
	sendKeysMapping = map[string]string{
		"!r":     "^M",
		"!a":     "^A",
		"!b":     "^B",
		"!c":     "^C",
		"!d":     "^D",
		"!t":     "\t",
		"!tt":    "\t\t",
		"!esc":   "escape", // ESC
		"!up":    "up",     // Cursor up
		"!down":  "down",   // Cursor down
		"!right": "right",  // Cursor right
		"!left":  "left",   // Cursor left
		"!space": "space",  // Space
		"!pu":    "ppage",  // Page up
		"!pd":    "npage",  // Page down
	}
	ctrlCommandRegex         = regexp.MustCompile(`^!c-([a-z])$`)
	fKeysRegex               = regexp.MustCompile(`^!f([0-9][012]?)$`)
	alphanumericRegex        = regexp.MustCompile(`^([a-zA-Z0-9])$`)
	asciinemaUploadURLRegex  = regexp.MustCompile(`(https?://\S+)`)
	asciinemaUploadDaysRegex = regexp.MustCompile(`(\d+) days?`)
	errExit                  = NewSessionError("EXITED_REPL", "exited REPL", nil)

	//go:embed share_client.sh.gotmpl
	shareClientScriptSource   string
	shareClientScriptTemplate = template.Must(template.New("share_client").Parse(shareClientScriptSource))

	//go:embed recording.md
	recordingReadmeSource string
)

// session represents a REPL session
//
// Slack:
//
//	Channels and DMs have an ID (fields: Channel, Timestamp), and may have a ThreadTimestamp field
//	to identify if they belong to a thread.
//
// Discord:
//
//	Channels, DMs and Threads are all channels with an ID
type session struct {
	conf           *sessionConfig
	conn           conn
	commands       []*sessionCommand
	userInputChan  chan [2]string // user, message
	userInputCount int32
	forceResend    chan bool
	g              *errgroup.Group
	ctx            context.Context
	cancelFn       context.CancelFunc
	active         bool
	warnTimer      *time.Timer
	closeTimer     *time.Timer
	scriptID       string
	authUsers      map[string]bool // true = allow, false = deny, n/a = default
	tmux           *util.Tmux
	cursorOn       bool
	cursorUpdated  time.Time
	maxSize        *config.Size
	shareConn      io.Closer
	webCmd         *exec.Cmd
	webWritable    bool
	webPort        int
	webPrefix      string
	mu             sync.RWMutex
}

type sessionConfig struct {
	global      *config.Config
	id          string
	user        string
	control     *channelID
	terminal    *channelID
	script      string
	controlMode config.ControlMode
	windowMode  config.WindowMode
	authMode    config.AuthMode
	size        *config.Size
	share       *shareConfig
	record      bool
	web         bool
	notifyWeb   func(s *session, enabled bool, prefix string)
}

type shareConfig struct {
	user          string
	relayPort     int
	hostKeyPair   *util.SSHKeyPair
	clientKeyPair *util.SSHKeyPair
}

type sessionCommand struct {
	prefix  string
	execute func(input string) error
}

type sshSession struct {
	SessionID     string
	ServerHost    string
	ServerPort    string
	User          string
	HostKeyPair   *util.SSHKeyPair
	ClientKeyPair *util.SSHKeyPair
	RelayPort     int
}

func newSession(conf *sessionConfig, conn conn) *session {
	ctx, cancel := context.WithCancel(context.Background())
	g, ctx := errgroup.WithContext(ctx)
	s := &session{
		conf:           conf,
		conn:           conn,
		scriptID:       fmt.Sprintf("replbot_%s", conf.id),
		authUsers:      make(map[string]bool),
		tmux:           util.NewTmux(conf.id, conf.size.Width, conf.size.Height, conf.global.TmuxPath),
		userInputChan:  make(chan [2]string, 10), // buffered!
		userInputCount: 0,
		forceResend:    make(chan bool),
		g:              g,
		ctx:            ctx,
		cancelFn:       cancel,
		active:         true,
		warnTimer:      time.NewTimer(conf.global.IdleTimeout - time.Minute),
		closeTimer:     time.NewTimer(conf.global.IdleTimeout),
		maxSize:        conf.size,
	}
	return initSessionCommands(s)
}

func initSessionCommands(s *session) *session {
	s.commands = []*sessionCommand{
		{"!h", s.handleHelpCommand},
		{"!help", s.handleHelpCommand},
		{"!n", s.handleNoNewlineCommand},
		{"!e", s.handleEscapeCommand},
		{"!alive", s.handleKeepaliveCommand},
		{"!allow", s.handleAllowCommand},
		{"!deny", s.handleDenyCommand},
		{"!!", s.handleCommentCommand},
		{"!screen", s.handleScreenCommand},
		{"!s", s.handleScreenCommand},
		{"!resize", s.handleResizeCommand},
		{"!web", s.handleWebCommand},
		{"!c-", s.handleSendKeysCommand}, // more see below!
		{"!f", s.handleSendKeysCommand},  // more see below!
		{"!q", s.handleExitCommand},
		{"!exit", s.handleExitCommand},
	}
	for prefix := range sendKeysMapping {
		s.commands = append(s.commands, &sessionCommand{prefix, s.handleSendKeysCommand})
	}
	sort.Slice(s.commands, func(i, j int) bool {
		return len(s.commands[i].prefix) > len(s.commands[j].prefix)
	})
	return s
}

// Run executes a REPL session. This function only returns on error or when gracefully exiting the session.
func (s *session) Run() error {
	slog.Info("started REPL session", "session", s.conf.id, "script", s.conf.script)
	defer slog.Info("closed REPL session", "session", s.conf.id)
	env, err := s.getEnv()
	if err != nil {
		slog.Error("failed to get env", "session", s.conf.id, "error", err)
		return err
	}
	command := s.createCommand()
	slog.Info("starting tmux", "session", s.conf.id, "command", command)
	if err := s.tmux.Start(env, command...); err != nil {
		slog.Error("failed to start tmux", "session", s.conf.id, "error", err)
		return err
	}
	slog.Debug("tmux started successfully", "session", s.conf.id)

	// Give tmux a moment to set up the session properly
	time.Sleep(500 * time.Millisecond)

	// Check if tmux is actually running
	if !s.tmux.Active() {
		slog.Error("tmux session not active after start", "session", s.conf.id)
		return fmt.Errorf("tmux session failed to start")
	}

	if err := s.maybeStartWeb(); err != nil {
		slog.Error("cannot start ttyd", "session", s.conf.id, "error", err)
		// We just disabled it, so we continue here
	}
	if err := s.conn.Send(s.conf.control, s.sessionStartedMessage()); err != nil {
		slog.Error("failed to send session started message", "session", s.conf.id, "error", err)
		return err
	}
	if err := s.maybeSendStartShareMessage(); err != nil {
		return err
	}

	slog.Debug("launching goroutines", "session", s.conf.id)
	s.g.Go(func() error {
		slog.Debug("userInputLoop started", "session", s.conf.id)
		err := s.userInputLoop()
		slog.Debug("userInputLoop ended", "session", s.conf.id, "error", err)
		return err
	})
	s.g.Go(func() error {
		slog.Debug("commandOutputLoop started", "session", s.conf.id)
		err := s.commandOutputLoop()
		slog.Debug("commandOutputLoop ended", "session", s.conf.id, "error", err)
		return err
	})
	s.g.Go(func() error {
		slog.Debug("activityMonitor started", "session", s.conf.id)
		err := s.activityMonitor()
		slog.Debug("activityMonitor ended", "session", s.conf.id, "error", err)
		return err
	})
	s.g.Go(func() error {
		slog.Debug("shutdownHandler started", "session", s.conf.id)
		err := s.shutdownHandler()
		slog.Debug("shutdownHandler ended", "session", s.conf.id, "error", err)
		return err
	})
	if s.conf.record {
		s.g.Go(s.monitorRecording)
	}

	slog.Debug("waiting for goroutines", "session", s.conf.id)
	if err := s.g.Wait(); err != nil && !errors.Is(err, errExit) {
		slog.Error("session error", "session", s.conf.id, "error", err)
		return err
	}
	return nil
}

// UserInput handles user input by forwarding to the underlying shell
func (s *session) UserInput(user, message string) {
	if !s.Active() || !s.allowUser(user) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Reset timeout timers
	s.warnTimer.Reset(s.conf.global.IdleTimeout - time.Minute)
	s.closeTimer.Reset(s.conf.global.IdleTimeout)

	// Forward to input channel
	s.userInputChan <- [2]string{user, message}
}

func (s *session) Active() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

func (s *session) ForceClose() error {
	_ = s.conn.Send(s.conf.control, forceCloseMessage)
	s.cancelFn()
	if err := s.g.Wait(); err != nil && !errors.Is(err, errExit) {
		return err
	}
	return nil
}

func (s *session) WriteShareClientScript(w io.Writer) error {
	if s.conf.share == nil {
		return NewSessionError("NOT_SHARE_SESSION", "not a share session", nil)
	}
	host, port, err := net.SplitHostPort(s.conf.global.ShareHost)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	shareConf := s.conf.share
	sessionInfo := &sshSession{
		SessionID:     s.conf.id,
		ServerHost:    host,
		ServerPort:    port,
		User:          shareConf.user,
		RelayPort:     shareConf.relayPort,
		HostKeyPair:   shareConf.hostKeyPair,
		ClientKeyPair: shareConf.clientKeyPair,
	}
	return shareClientScriptTemplate.Execute(w, sessionInfo)
}

func (s *session) WriteShareUserFile(user string) error {
	return os.WriteFile(s.sshUserFile(), []byte(user), 0600)
}

func (s *session) RegisterShareConn(conn io.Closer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shareConn != nil {
		_ = s.shareConn.Close()
	}
	s.shareConn = conn
}

func (s *session) userInputLoop() error {
	for {
		select {
		case m := <-s.userInputChan:
			if err := s.handleUserInput(m[0], m[1]); err != nil {
				return err
			}
		case <-s.ctx.Done():
			return errExit
		}
	}
}

func (s *session) handleUserInput(user, message string) error {
	slog.Debug("user input", "session", s.conf.id, "user", user, "message", message)
	if err := validateMessageLength(message); err != nil {
		return s.conn.Send(s.conf.control, err.Error())
	}
	atomic.AddInt32(&s.userInputCount, 1)
	for _, c := range s.commands {
		if strings.HasPrefix(message, c.prefix) {
			return c.execute(message)
		}
	}
	return s.handlePassthrough(message)
}

func (s *session) commandOutputLoop() error {
	var last, lastID string
	var err error
	for {
		select {
		case <-s.ctx.Done():
			if lastID != "" {
				_ = s.conn.Update(s.conf.terminal, lastID, util.FormatMarkdownCode(addExitedMessage(sanitizeWindow(removeTmuxBorder(last))))) // Show "(REPL exited.)" in terminal
			}
			return errExit
		case <-s.forceResend:
			last, lastID, err = s.maybeRefreshTerminal("", "") // Force re-send!
			if err != nil {
				return err
			}
		case <-time.After(s.conf.global.RefreshInterval):
			last, lastID, err = s.maybeRefreshTerminal(last, lastID)
			if err != nil {
				return err
			}
		}
	}
}

func (s *session) maybeRefreshTerminal(last, lastID string) (string, string, error) {
	current, err := s.tmux.Capture()
	if err != nil {
		tmuxActive := s.tmux.Active()
		slog.Error("tmux capture failed", "session", s.conf.id, "error", err, "tmux_active", tmuxActive, "lastID", lastID)

		// Add a small retry mechanism for the first capture
		if lastID == "" && tmuxActive {
			slog.Debug("retrying first capture after delay", "session", s.conf.id)
			time.Sleep(100 * time.Millisecond)
			current, err = s.tmux.Capture()
			if err == nil {
				slog.Debug("retry successful", "session", s.conf.id)
				goto captureSuccess
			}
			slog.Error("retry failed", "session", s.conf.id, "error", err)
		}

		if lastID != "" {
			_ = s.conn.Update(s.conf.terminal, lastID, util.FormatMarkdownCode(addExitedMessage(sanitizeWindow(removeTmuxBorder(last))))) // Show "(REPL exited.)" in terminal
		}
		return "", "", errExit // The command may have ended, gracefully exit
	}

captureSuccess:
	current = s.maybeAddCursor(s.maybeTrimWindow(sanitizeWindow(removeTmuxBorder(current))))
	if current == last {
		return last, lastID, nil
	}
	if s.shouldUpdateTerminal(lastID) {
		if err := s.conn.Update(s.conf.terminal, lastID, util.FormatMarkdownCode(current)); err == nil {
			return current, lastID, nil
		}
	}
	if lastID, err = s.conn.SendWithID(s.conf.terminal, util.FormatMarkdownCode(current)); err != nil {
		return "", "", err
	}
	atomic.StoreInt32(&s.userInputCount, 0)
	return current, lastID, nil
}

func (s *session) shouldUpdateTerminal(lastID string) bool {
	if s.conf.controlMode == config.Split {
		return lastID != ""
	}
	return lastID != "" && atomic.LoadInt32(&s.userInputCount) < updateMessageUserInputCountLimit
}

func (s *session) maybeAddCursor(window string) string {
	switch s.conf.global.Cursor {
	case config.CursorOff:
		return window
	case config.CursorOn:
		show, x, y, err := s.tmux.Cursor()
		if !show || err != nil {
			return window
		}
		return addCursor(window, x, y)
	default:
		show, x, y, err := s.tmux.Cursor()
		if !show || err != nil {
			return window
		}
		if time.Since(s.cursorUpdated) > s.conf.global.Cursor {
			s.cursorOn = !s.cursorOn
			s.cursorUpdated = time.Now()
		}
		if !s.cursorOn {
			return window
		}
		return addCursor(window, x, y)
	}
}

func (s *session) shutdownHandler() error {
	<-s.ctx.Done()
	if err := s.tmux.Stop(); err != nil {
		slog.Error("unable to stop tmux", "session", s.conf.id, "error", err)
	}
	cmd := exec.Command(s.conf.script, scriptKillCommand, s.scriptID)
	if output, err := cmd.CombinedOutput(); err != nil {
		slog.Error("unable to kill command", "session", s.conf.id, "error", err, "output", string(output))
	}
	if err := s.sendExitedMessage(); err != nil {
		slog.Error("unable to send exit message", "session", s.conf.id, "error", err)
	}
	if err := s.conn.Archive(s.conf.control); err != nil {
		slog.Error("unable to archive thread", "session", s.conf.id, "error", err)
	}
	_ = os.Remove(s.sshUserFile())
	_ = os.Remove(s.sshClientKeyFile())
	_ = os.Remove(s.tmux.RecordingFile())
	s.mu.Lock()
	s.active = false
	if s.shareConn != nil {
		_ = s.shareConn.Close()
	}
	if s.webCmd != nil && s.webCmd.Process != nil {
		_ = s.webCmd.Process.Kill()
	}
	s.mu.Unlock()
	return nil
}

func (s *session) activityMonitor() error {
	defer func() {
		s.warnTimer.Stop()
		s.closeTimer.Stop()
	}()
	for {
		select {
		case <-s.ctx.Done():
			return errExit
		case <-s.warnTimer.C:
			_ = s.conn.Send(s.conf.control, fmt.Sprintf(timeoutWarningMessage, s.conn.Mention(s.conf.user)))
			slog.Info("session idle warning sent to user", "session", s.conf.id)
		case <-s.closeTimer.C:
			slog.Info("idle timeout reached; closing session", "session", s.conf.id)
			return errExit
		}
	}
}

func (s *session) sessionStartedMessage() string {
	message := fmt.Sprintf(sessionStartedMessage, s.conn.Mention(s.conf.user))
	if s.conf.controlMode == config.Split {
		message += "\n\n" + splitModeThreadMessage
	}
	switch s.conf.authMode {
	case config.OnlyMe:
		message += "\n\n" + onlyMeModeMessage
	case config.Everyone:
		message += "\n\n" + everyoneModeMessage
	}
	if s.webCmd != nil {
		if s.webWritable {
			message += "\n\n" + fmt.Sprintf(sessionWithWebStartReadWriteMessage, s.conf.global.WebHost, s.webPrefix)
		} else {
			message += "\n\n" + fmt.Sprintf(sessionWithWebStartReadOnlyMessage, s.conf.global.WebHost, s.webPrefix)
		}
	}
	if s.shouldWarnMessageLength(s.conf.size) {
		message += "\n\n" + messageLimitWarningMessage
	}
	return message
}

func (s *session) maybeTrimWindow(window string) string {
	switch s.conf.windowMode {
	case config.Full:
		if p, _ := s.conf.global.Platform(); p == config.Discord {
			return expandWindow(window)
		}
		return window
	case config.Trim:
		return strings.TrimRightFunc(window, unicode.IsSpace)
	default:
		return window
	}
}

func (s *session) getEnv() (map[string]string, error) {
	var sshUserFile, sshKeyFile, relayPort string
	if s.conf.share != nil {
		shareConf := s.conf.share
		relayPort = strconv.Itoa(shareConf.relayPort)
		sshUserFile = s.sshUserFile()
		sshKeyFile = s.sshClientKeyFile()
		if err := os.WriteFile(sshKeyFile, []byte(shareConf.clientKeyPair.PrivateKey), 0600); err != nil {
			return nil, err
		}
	}
	return map[string]string{
		"REPLBOT_SSH_KEY_FILE":       sshKeyFile,
		"REPLBOT_SSH_USER_FILE":      sshUserFile,
		"REPLBOT_SSH_RELAY_PORT":     relayPort,
		"REPLBOT_MAX_TOTAL_SESSIONS": strconv.Itoa(s.conf.global.MaxUserSessions),
	}, nil
}

func (s *session) parseUsers(usersList []string) ([]string, error) {
	users := make([]string, 0)
	for _, field := range usersList {
		user, err := s.conn.ParseMention(field)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}

func (s *session) allowUser(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == s.conf.user {
		return true // Always allow session owner!
	}
	if allow, ok := s.authUsers[user]; ok {
		return allow
	}
	return s.conf.authMode == config.Everyone
}

func (s *session) createCommand() []string {
	// Log the absolute path of the script
	absPath, err := filepath.Abs(s.conf.script)
	if err != nil {
		slog.Error("failed to get absolute path", "script", s.conf.script, "error", err)
		absPath = s.conf.script
	}

	// Check if script exists and is executable
	if info, err := os.Stat(absPath); err != nil {
		slog.Error("script file error", "script", absPath, "error", err)
	} else {
		slog.Info("script file info", "script", absPath, "mode", info.Mode(), "executable", info.Mode()&0111 != 0)
	}

	command := []string{s.conf.script, scriptRunCommand, s.scriptID}
	if s.conf.record {
		command = s.maybeWrapAsciinemaCommand(command)
	}
	return command
}

func (s *session) maybeWrapAsciinemaCommand(command []string) []string {
	if err := util.Run("asciinema", "--version"); err != nil {
		slog.Info("cannot record session; 'asciinema' command is missing", "session", s.conf.id)
		s.conf.record = false
		return command
	}
	return []string{
		"asciinema", "rec",
		"--quiet",
		"--idle-time-limit", "5",
		"--title", "REPLbot session",
		"--command", strings.Join(command, " "),
		s.asciinemaFile(),
	}
}

func (s *session) asciinemaFile() string {
	return filepath.Join(os.TempDir(), "replbot_"+s.conf.id+".asciinema")
}

func (s *session) sshClientKeyFile() string {
	return filepath.Join(os.TempDir(), "replbot_"+s.conf.id+".ssh-client-key")
}

func (s *session) sshUserFile() string {
	return filepath.Join(os.TempDir(), "replbot_"+s.conf.id+".ssh-user")
}

func (s *session) createRecordingArchive(filename string) (*os.File, error) {
	recordingFile := s.tmux.RecordingFile()
	asciinemaFile := s.asciinemaFile()
	defer func() {
		os.Remove(recordingFile)
		os.Remove(asciinemaFile)
	}()
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	zw := zip.NewWriter(file)
	if err := zipAppendEntry(zw, "REPLbot session/README.md", recordingReadmeSource); err != nil {
		return nil, err
	}
	if err := zipAppendFile(zw, "REPLbot session/terminal.txt", recordingFile); err != nil {
		return nil, err
	}
	if err := zipAppendFile(zw, "REPLbot session/replay.asciinema", asciinemaFile); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}
	return file, nil
}

func (s *session) monitorRecording() error {
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case <-time.After(time.Second):
			stat, err := os.Stat(s.asciinemaFile())
			if err != nil {
				continue
			} else if stat.Size() > recordingFileSizeMax {
				if err := s.conn.Send(s.conf.control, recordingTooLargeMessage); err != nil {
					return err
				}
				return errExit
			}
		}
	}
}

func (s *session) sendExitedMessage() error {
	if s.conf.record {
		if err := s.sendExitedMessageWithRecording(); err != nil {
			slog.Error("unable to upload recording", "session", s.conf.id, "error", err)
			return s.sendExitedMessageWithoutRecording()
		}
		return nil
	}
	return s.sendExitedMessageWithoutRecording()
}

func (s *session) sendExitedMessageWithoutRecording() error {
	return s.conn.Send(s.conf.control, sessionExitedMessage)
}

func (s *session) sendExitedMessageWithRecording() error {
	if err := s.maybePatchAsciinemaRecordingFile(); err != nil {
		slog.Error("cannot patch asciinema session file", "session", s.conf.id, "error", err)
	}
	url, expiry, err := s.maybeUploadAsciinemaRecording()
	if err != nil {
		slog.Error("cannot upload recorded asciinema session", "session", s.conf.id, "error", err)
	}
	filename := filepath.Join(os.TempDir(), "replbot_"+s.conf.id+".recording.zip")
	file, err := s.createRecordingArchive(filename)
	if err != nil {
		return err
	}
	defer func() {
		file.Close()
		os.Remove(filename)
	}()
	message := sessionExitedWithRecordingMessage
	if url != "" {
		message += " " + fmt.Sprintf(sessionAsciinemaLinkMessage, url)
	}
	if expiry != "" {
		message += " " + fmt.Sprintf(sessionAsciinemaExpiryMessage, expiry)
	}
	return s.conn.UploadFile(s.conf.control, message, recordingFileName, recordingFileType, file)
}

func (s *session) maybeStartWeb() error {
	if !s.conf.web {
		return nil
	}
	permitWrite := s.conf.authMode == config.Everyone
	return s.startWeb(permitWrite)
}

func (s *session) maybeSendStartShareMessage() error {
	if s.conf.share == nil {
		return nil
	}
	host, port, err := net.SplitHostPort(s.conf.global.ShareHost)
	if err != nil {
		return err
	}
	message := fmt.Sprintf(shareStartCommandMessage, port, s.conf.share.user, host)
	if err := s.conn.SendEphemeral(s.conf.control, s.conf.user, message); err != nil {
		return err
	}
	return nil
}

func (s *session) maybeSendMessageLengthWarning(size *config.Size) error {
	if s.shouldWarnMessageLength(size) {
		return s.conn.Send(s.conf.control, messageLimitWarningMessage)
	}
	return nil
}

func (s *session) shouldWarnMessageLength(size *config.Size) bool {
	p, _ := s.conf.global.Platform()
	return p == config.Discord && (size == config.Medium || size == config.Large)
}

func (s *session) handlePassthrough(input string) error {
	cmd := s.conn.Unescape(input)
	sanitized, err := sanitizeCommand(cmd)
	if err != nil {
		return s.conn.Send(s.conf.control, err.Error())
	}
	slog.Debug("handlePassthrough: pasting to tmux", "sessionID", s.conf.id, "original", input, "sanitized", sanitized)
	return s.tmux.Paste(fmt.Sprintf("%s\n", sanitized))
}

func (s *session) handleHelpCommand(_ string) error {
	atomic.AddInt32(&s.userInputCount, updateMessageUserInputCountLimit)
	return s.conn.Send(s.conf.control, helpMessage)
}

func (s *session) handleNoNewlineCommand(input string) error {
	input = s.conn.Unescape(strings.TrimSpace(strings.TrimPrefix(input, "!n")))
	if input == "" {
		return s.conn.Send(s.conf.control, noNewlineHelpMessage)
	}
	return s.tmux.Paste(input)
}

func (s *session) handleEscapeCommand(input string) error {
	input = unquote(s.conn.Unescape(strings.TrimSpace(strings.TrimPrefix(input, "!e"))))
	if input == "" {
		return s.conn.Send(s.conf.control, escapeHelpMessage)
	}
	return s.tmux.Paste(input)
}

func (s *session) handleKeepaliveCommand(_ string) error {
	return s.conn.Send(s.conf.control, sessionKeptAliveMessage)
}

func (s *session) handleAllowCommand(input string) error {
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(input, "!allow")))
	if util.InStringList(fields, "all") || util.InStringList(fields, "everyone") {
		return s.resetAuthMode(config.Everyone)
	}
	if util.InStringList(fields, "nobody") || util.InStringList(fields, "only-me") {
		return s.resetAuthMode(config.OnlyMe)
	}
	users, err := s.parseUsers(fields)
	if err != nil || len(users) == 0 {
		return s.conn.Send(s.conf.control, fmt.Sprintf(allowCommandHelpMessage, s.conn.MentionBot()))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range users {
		s.authUsers[user] = true
	}
	message := usersAddedToAllowList
	return s.conn.Send(s.conf.control, message)
}

func (s *session) handleDenyCommand(input string) error {
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(input, "!deny")))
	if util.InStringList(fields, "all") || util.InStringList(fields, "everyone") {
		return s.resetAuthMode(config.OnlyMe)
	}
	users, err := s.parseUsers(fields)
	if err != nil || len(users) == 0 {
		return s.conn.Send(s.conf.control, fmt.Sprintf(denyCommandHelpMessage, s.conn.MentionBot()))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range users {
		if s.conf.user == user {
			return s.conn.Send(s.conf.control, cannotAddOwnerToDenyList)
		}
		s.authUsers[user] = false
	}
	message := usersAddedToDenyList
	return s.conn.Send(s.conf.control, message)
}

func (s *session) resetAuthMode(authMode config.AuthMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conf.authMode = authMode
	s.authUsers = make(map[string]bool)
	if authMode == config.Everyone {
		return s.conn.Send(s.conf.control, authModeChangeMessage+everyoneModeMessage)
	}
	return s.conn.Send(s.conf.control, authModeChangeMessage+onlyMeModeMessage)
}

func (s *session) handleSendKeysCommand(input string) error {
	fields := strings.Fields(strings.TrimSpace(input))
	keys := make([]string, 0)
	for _, field := range fields {
		if matches := ctrlCommandRegex.FindStringSubmatch(field); len(matches) > 0 {
			keys = append(keys, "^"+strings.ToUpper(matches[1]))
		} else if matches := fKeysRegex.FindStringSubmatch(field); len(matches) > 0 {
			keys = append(keys, "F"+strings.ToUpper(matches[1]))
		} else if matches := alphanumericRegex.FindStringSubmatch(field); len(matches) > 0 {
			keys = append(keys, matches[1])
		} else if controlChar, ok := sendKeysMapping[field]; ok {
			keys = append(keys, controlChar)
		} else {
			return s.conn.Send(s.conf.control, sendKeysHelpMessage)
		}
	}
	return s.tmux.SendKeys(keys...)
}

func (s *session) handleCommentCommand(_ string) error {
	return nil // Ignore comments
}

func (s *session) handleScreenCommand(_ string) error {
	s.forceResend <- true
	return nil
}

func (s *session) handleWebCommand(input string) error {
	if s.conf.global.WebHost == "" {
		return s.conn.Send(s.conf.control, webNotSupportedMessage)
	}
	toggle := strings.TrimSpace(strings.TrimPrefix(input, "!web"))
	s.mu.RLock()
	enabled := s.webCmd != nil
	writable := s.webWritable
	s.mu.RUnlock()
	switch toggle {
	case "rw", "ro":
		shouldBeWritable := toggle == "rw"
		if enabled && writable == shouldBeWritable {
			return s.sendWebHelpMessage(enabled, writable)
		}
		if err := s.startWeb(shouldBeWritable); err != nil {
			return s.conn.Send(s.conf.control, webNotWorkingMessage)
		}
		return s.sendWebHelpMessage(true, shouldBeWritable)
	case "off":
		if !enabled {
			return s.conn.Send(s.conf.control, webDisabledMessage)
		}
		if err := s.stopWeb(); err != nil {
			return err
		}
		return s.conn.Send(s.conf.control, webStoppedMessage+"\n\n"+webHelpMessage)
	default:
		return s.sendWebHelpMessage(enabled, writable)
	}
}

func (s *session) sendWebHelpMessage(enabled, writable bool) error {
	if enabled {
		message := fmt.Sprintf(webEnabledMessage, s.conf.global.WebHost, s.webPrefix)
		if writable {
			message += "\n\n" + webIsWritableMessage
		} else {
			message += "\n\n" + webIsReadOnlyMessage
		}
		return s.conn.Send(s.conf.control, message)
	}
	return s.conn.Send(s.conf.control, webDisabledMessage+"\n\n"+webHelpMessage)
}

func (s *session) startWeb(writable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.webCmd != nil && s.webCmd.Process != nil {
		_ = s.webCmd.Process.Kill()
	}
	s.webCmd = nil // Disable, in case we fail below
	if s.webPort == 0 {
		webPort, err := util.RandomPort()
		if err != nil {
			return err
		}
		s.webPort = webPort
	}
	if s.webPrefix == "" {
		p, err := util.RandomString(10)
		if err != nil {
			return err
		}
		s.webPrefix = p
	}
	s.webWritable = writable
	var args []string
	if s.webWritable {
		args = []string{
			"--interface", "lo",
			"--port", strconv.Itoa(s.webPort),
			"--check-origin",
			"tmux", "attach", "-t", s.tmux.MainID(),
		}
	} else {
		args = []string{
			"--interface", "lo",
			"--port", strconv.Itoa(s.webPort),
			"--check-origin",
			"--readonly",                                  // ttyd is read-only
			"tmux", "attach", "-r", "-t", s.tmux.MainID(), // tmux is also read-only
		}
	}
	s.webCmd = exec.Command("ttyd", args...)
	if err := s.webCmd.Start(); err != nil {
		s.webCmd = nil // Disable web!
		return err
	}
	s.conf.notifyWeb(s, true, s.webPrefix)
	return nil
}

func (s *session) stopWeb() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.webCmd == nil {
		return nil
	}
	if err := s.webCmd.Process.Kill(); err != nil {
		return err
	}
	s.webCmd = nil
	s.conf.notifyWeb(s, false, s.webPrefix)
	return nil
}

func (s *session) handleResizeCommand(input string) error {
	size, err := config.ParseSize(strings.TrimSpace(strings.TrimPrefix(input, "!resize")))
	if err != nil {
		return s.conn.Send(s.conf.control, resizeCommandHelpMessage)
	}
	if err := s.maybeSendMessageLengthWarning(size); err != nil {
		return err
	}
	if s.maxSize.Max(size) == size {
		s.mu.Lock()
		s.maxSize = size
		s.mu.Unlock()
	}
	return s.tmux.Resize(size.Width, size.Height)
}

func (s *session) handleExitCommand(_ string) error {
	return errExit
}

func (s *session) maybeUploadAsciinemaRecording() (url string, expiry string, err error) {
	if !s.conf.record || !s.conf.global.UploadRecording {
		return "", "", nil
	}
	cmd := exec.Command("asciinema", "upload", s.asciinemaFile())
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(string(output), "\n") {
		if matches := asciinemaUploadURLRegex.FindStringSubmatch(line); url == "" && len(matches) > 0 {
			url = matches[0]
		}
		if matches := asciinemaUploadDaysRegex.FindStringSubmatch(line); expiry == "" && len(matches) > 0 {
			expiry = matches[0]
		}
	}
	if url == "" {
		return "", "", NewSessionError("NO_ASCIINEMA_URL", "no asciinema URL found", nil)
	}
	return url, expiry, nil
}

func (s *session) maybePatchAsciinemaRecordingFile() error {
	replayFile := s.asciinemaFile()
	tempReplayFile := fmt.Sprintf("%s.tmp", replayFile)
	defer os.Remove(tempReplayFile)
	in, err := os.Open(s.asciinemaFile())
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tempReplayFile, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	rd := bufio.NewReader(in)
	header, err := rd.ReadString('\n')
	if err != nil {
		return err
	}
	if header, err = sjson.Set(header, "width", s.maxSize.Width); err != nil {
		return err
	}
	if header, err = sjson.Set(header, "height", s.maxSize.Height); err != nil {
		return err
	}
	if _, err := out.WriteString(header); err != nil {
		return err
	}
	if _, err := io.Copy(out, rd); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := in.Close(); err != nil {
		return err
	}
	return os.Rename(tempReplayFile, replayFile)
}
