// Package bot provides the replbot main functionality
package bot

import (
	"context"
	_ "embed" // go:embed requires this
	"errors"
	"fmt"
	"github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
	"heckel.io/replbot/config"
	"heckel.io/replbot/util"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	welcomeMessage = "Hi there 👋! "
	mentionMessage = "I'm a robot for running interactive REPLs and shells from right here. To start a new session, simply tag me " +
		"and name one of the available REPLs, like so: %s %s\n\nAvailable REPLs: %s.\n\nTo run the session in a `thread`, " +
		"the main `channel`, or in `split` mode, use the respective keywords (default: `%s`). To define the terminal size, use the words " +
		"`tiny`, `small`, `medium` or `large` (default: `%s`). Use `full` or `trim` to set the window mode (default: `%s`), and `everyone` " +
		"or `only-me` to define who can send commands (default: `%s`). Send `record` or `norecord` to define if your session should be " +
		"recorded (default: `%s`)."
	webMessage   = "Use the word `web` or `noweb` to enable a web-based terminal for this session (default: `%s`)."
	shareMessage = "Using the word `share` will allow you to share your own terminal here in the chat. Terminal sharing " +
		"sessions are always started in `only-me` mode, unless overridden."
	unknownCommandMessage           = "I am not quite sure what you mean by _%s_ ⁉"
	misconfiguredMessage            = "😭 Oh no. It looks like REPLbot is misconfigured. I couldn't find any scripts to run."
	maxTotalSessionsExceededMessage = "😭 There are too many active sessions. Please wait until another session is closed."
	maxUserSessionsExceededMessage  = "😭 You have too many active sessions. Please close a session to start a new one."
	helpRequestedCommand            = "help"
	recordCommand                   = "record"
	noRecordCommand                 = "norecord"
	webCommand                      = "web"
	noWebCommand                    = "noweb"
	shareCommand                    = "share"
	shareServerScriptFile           = "/tmp/replbot_share_server.sh"
)

// Key exchange algorithms, ciphers,and MACs (see `ssh-audit` output)
const (
	kexCurve25519SHA256 = "curve25519-sha256@libssh.org"
	macHMACSHA256ETM    = "hmac-sha2-256-etm@openssh.com"
	cipherAES128GCM     = "aes128-gcm@openssh.com"
	cipherSSHRSA        = "ssh-rsa"
	cipherED25519       = "ssh-ed25519"
	sshVersion          = "OpenSSH_7.6p1" // fake!
)

var (
	//go:embed share_server.sh
	shareServerScriptSource string
	errNoScript             = errors.New("no script defined")
	errHelpRequested        = errors.New("help requested")
)

// Bot is the main struct that provides REPLbot
type Bot struct {
	config    *config.Config
	conn      conn
	sessions  map[string]*session
	shareUser map[string]*session
	webPrefix map[string]*session
	cancelFn  context.CancelFunc
	mu        sync.RWMutex
	rl        *rateLimiter
}

// New creates a new REPLbot instance using the given configuration
func New(conf *config.Config) (*Bot, error) {
	if len(conf.Scripts()) == 0 {
		return nil, errors.New("no REPL scripts found in script dir")
	} else if err := util.Run("tmux", "-V"); err != nil {
		return nil, fmt.Errorf("tmux check failed: %s", err.Error())
	}
	var conn conn
	switch conf.Platform() {
	case config.Slack:
		conn = newSlackConn(conf)
	case config.Discord:
		conn = newDiscordConn(conf)
	case config.Mem:
		conn = newMemConn(conf)
	default:
		return nil, fmt.Errorf("invalid type: %s", conf.Platform())
	}
	return &Bot{
		config:    conf,
		conn:      conn,
		sessions:  make(map[string]*session),
		shareUser: make(map[string]*session),
		webPrefix: make(map[string]*session),
		rl:        newRateLimiter(conf),
	}, nil
}

// Run runs the bot in the foreground indefinitely or until Stop is called.
// This method does not return unless there is an error, or if gracefully shut down via Stop.
func (b *Bot) Run() error {
	var ctx context.Context
	ctx, b.cancelFn = context.WithCancel(context.Background())
	g, ctx := errgroup.WithContext(ctx)
	eventChan, err := b.conn.Connect(ctx)
	if err != nil {
		return err
	}
	g.Go(func() error {
		return b.handleEvents(ctx, eventChan)
	})
	if b.config.ShareHost != "" {
		g.Go(func() error {
			return b.runShareServer(ctx)
		})
	}
	if b.config.WebHost != "" {
		g.Go(func() error {
			return b.runWebServer(ctx)
		})
	}
	return g.Wait()
}

// Stop gracefully shuts down the bot, closing all active sessions gracefully
func (b *Bot) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sessionID, sess := range b.sessions {
		slog.Info("force-closing session", "session", sessionID)
		if err := sess.ForceClose(); err != nil {
			slog.Error("force-closing session failed", "session", sessionID, "error", err)
		}
		delete(b.sessions, sessionID)
		if sess.conf.share != nil {
			delete(b.shareUser, sess.conf.share.user)
		}
		if sess.webPrefix != "" {
			delete(b.webPrefix, sess.webPrefix)
		}
	}
	b.cancelFn() // This must be at the end, see app.go
}

func (b *Bot) handleEvents(ctx context.Context, eventChan <-chan event) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-eventChan:
			if err := b.handleEvent(ev); err != nil {
				return err
			}
		}
	}
}

func (b *Bot) handleEvent(e event) error {
	switch ev := e.(type) {
	case *messageEvent:
		if allowed, retry := b.rl.allow(ev.User, opMessage); !allowed {
			b.sendRateLimit(&channelID{Channel: ev.Channel, Thread: ev.Thread}, ev.User, retry)
			return nil
		}
		return b.handleMessageEvent(ev)
	case *errorEvent:
		return ev.Error
	default:
		return nil // Ignore other events
	}
}

func (b *Bot) handleMessageEvent(ev *messageEvent) error {
	if b.maybeForwardMessage(ev) {
		return nil // We forwarded the message
	} else if ev.ChannelType == channelTypeUnknown {
		return nil
	} else if ev.ChannelType == channelTypeChannel && !strings.Contains(ev.Message, b.conn.MentionBot()) {
		return nil
	}
	conf, err := b.parseSessionConfig(ev)
	if err != nil {
		return b.handleHelp(ev.Channel, ev.Thread, err)
	}
	if allowed, err := b.checkSessionAllowed(ev.Channel, ev.Thread, conf); err != nil || !allowed {
		return err
	}
	if allowed, retry := b.rl.allow(ev.User, opSession); !allowed {
		b.sendRateLimit(&channelID{Channel: ev.Channel, Thread: ev.Thread}, ev.User, retry)
		return nil
	}
	switch conf.controlMode {
	case config.Channel:
		return b.startSessionChannel(ev, conf)
	case config.Thread:
		return b.startSessionThread(ev, conf)
	case config.Split:
		return b.startSessionSplit(ev, conf)
	default:
		return fmt.Errorf("unexpected mode: %s", conf.controlMode)
	}
}

func (b *Bot) maybeForwardMessage(ev *messageEvent) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	sessionID := util.SanitizeNonAlphanumeric(fmt.Sprintf("%s_%s", ev.Channel, ev.Thread)) // Thread may be empty, that's ok
	if sess, ok := b.sessions[sessionID]; ok && sess.Active() {
		if allowed, retry := b.rl.allow(ev.User, opCommand); allowed {
			sess.UserInput(ev.User, ev.Message)
		} else {
			b.sendRateLimit(&channelID{Channel: ev.Channel, Thread: ev.Thread}, ev.User, retry)
		}
		return true
	}
	return false
}

func (b *Bot) sendRateLimit(ch *channelID, user string, d time.Duration) {
	secs := int(d.Seconds()) + 1
	msg := fmt.Sprintf("⏱️ Slow down! Try again in %d seconds.", secs)
	if err := b.conn.SendEphemeral(ch, user, msg); err != nil {
		slog.Error("unable to send rate limit message", "error", err)
	}
}

func (b *Bot) parseSessionConfig(ev *messageEvent) (*sessionConfig, error) {
	conf := &sessionConfig{
		global:    b.config,
		user:      ev.User,
		record:    b.config.DefaultRecord,
		web:       b.config.DefaultWeb,
		notifyWeb: b.webUpdated,
	}
	fields := strings.Fields(ev.Message)
	for _, field := range fields {
		switch field {
		case b.conn.MentionBot():
			// Ignore
		case helpRequestedCommand:
			return nil, errHelpRequested
		case string(config.Thread), string(config.Channel), string(config.Split):
			conf.controlMode = config.ControlMode(field)
		case string(config.Full), string(config.Trim):
			conf.windowMode = config.WindowMode(field)
		case string(config.OnlyMe), string(config.Everyone):
			conf.authMode = config.AuthMode(field)
		case config.Tiny.Name, config.Small.Name, config.Medium.Name, config.Large.Name:
			conf.size = config.Sizes[field]
		case recordCommand, noRecordCommand:
			conf.record = field == recordCommand
		default:
			if b.config.ShareEnabled() && field == shareCommand {
				relayPort, err := util.RandomPort()
				if err != nil {
					return nil, err
				}
				hostKeyPair, err := util.GenerateSSHKeyPair()
				if err != nil {
					return nil, err
				}
				clientKeyPair, err := util.GenerateSSHKeyPair()
				if err != nil {
					return nil, err
				}
				conf.script = shareServerScriptFile
				user, err := util.RandomString(10)
				if err != nil {
					return nil, err
				}
				conf.share = &shareConfig{
					user:          user,
					relayPort:     relayPort,
					hostKeyPair:   hostKeyPair,
					clientKeyPair: clientKeyPair,
				}
			} else if b.config.WebHost != "" && (field == webCommand || field == noWebCommand) {
				conf.web = field == webCommand
			} else if s := b.config.Script(field); conf.script == "" && s != "" {
				conf.script = s
			} else {
				return nil, fmt.Errorf(unknownCommandMessage, field) //lint:ignore ST1005 we'll pass this to the client
			}
		}
	}
	if conf.script == "" {
		return nil, errNoScript
	}
	return b.applySessionConfigDefaults(ev, conf)
}

func (b *Bot) applySessionConfigDefaults(ev *messageEvent, conf *sessionConfig) (*sessionConfig, error) {
	if conf.share != nil { // sane defaults for terminal sharing
		if conf.authMode == "" {
			conf.authMode = config.OnlyMe
		}
	}
	if conf.controlMode == "" {
		if ev.Thread != "" {
			conf.controlMode = config.Thread // special handling, because it'd be weird otherwise
		} else {
			conf.controlMode = b.config.DefaultControlMode
		}
	}
	if b.config.Platform() == config.Discord && ev.ChannelType == channelTypeDM && conf.controlMode != config.Channel {
		conf.controlMode = config.Channel // special case: Discord does not support threads in direct messages
	}
	if conf.windowMode == "" {
		if conf.controlMode == config.Thread {
			conf.windowMode = config.Trim
		} else {
			conf.windowMode = b.config.DefaultWindowMode
		}
	}
	if conf.authMode == "" {
		conf.authMode = b.config.DefaultAuthMode
	}
	if conf.size == nil {
		if conf.controlMode == config.Thread {
			conf.size = config.Tiny // special case: make it tiny in a thread
		} else {
			conf.size = b.config.DefaultSize
		}
	}
	return conf, nil
}

func (b *Bot) startSessionChannel(ev *messageEvent, conf *sessionConfig) error {
	conf.id = util.SanitizeNonAlphanumeric(fmt.Sprintf("%s_%s", ev.Channel, ""))
	conf.control = &channelID{Channel: ev.Channel, Thread: ""}
	conf.terminal = conf.control
	return b.startSession(conf)
}

func (b *Bot) startSessionThread(ev *messageEvent, conf *sessionConfig) error {
	if ev.Thread == "" {
		conf.id = util.SanitizeNonAlphanumeric(fmt.Sprintf("%s_%s", ev.Channel, ev.ID))
		conf.control = &channelID{Channel: ev.Channel, Thread: ev.ID}
		conf.terminal = conf.control
		return b.startSession(conf)
	}
	conf.id = util.SanitizeNonAlphanumeric(fmt.Sprintf("%s_%s", ev.Channel, ev.Thread))
	conf.control = &channelID{Channel: ev.Channel, Thread: ev.Thread}
	conf.terminal = conf.control
	return b.startSession(conf)
}

func (b *Bot) startSessionSplit(ev *messageEvent, conf *sessionConfig) error {
	if ev.Thread == "" {
		conf.id = util.SanitizeNonAlphanumeric(fmt.Sprintf("%s_%s", ev.Channel, ev.ID))
		conf.control = &channelID{Channel: ev.Channel, Thread: ev.ID}
		conf.terminal = &channelID{Channel: ev.Channel, Thread: ""}
		return b.startSession(conf)
	}
	conf.id = util.SanitizeNonAlphanumeric(fmt.Sprintf("%s_%s", ev.Channel, ev.Thread))
	conf.control = &channelID{Channel: ev.Channel, Thread: ev.Thread}
	conf.terminal = &channelID{Channel: ev.Channel, Thread: ""}
	return b.startSession(conf)
}

func (b *Bot) startSession(conf *sessionConfig) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	sess := newSession(conf, b.conn)
	b.sessions[conf.id] = sess
	if conf.share != nil {
		b.shareUser[conf.share.user] = sess
	}
	slog.Info("starting session", "session", conf.id, "user", conf.user)
	go func() {
		if err := sess.Run(); err != nil {
			slog.Error("session exited with error", "session", conf.id, "error", err)
		} else {
			slog.Info("session exited successfully", "session", conf.id)
		}
		b.mu.Lock()
		delete(b.sessions, conf.id)
		if conf.share != nil {
			delete(b.shareUser, conf.share.user)
		}
		if sess.webPrefix != "" {
			delete(b.webPrefix, sess.webPrefix)
		}
		b.mu.Unlock()
	}()
	return nil
}

func (b *Bot) handleHelp(channel, thread string, err error) error {
	target := &channelID{Channel: channel, Thread: thread}
	scripts := b.config.Scripts()
	if len(scripts) == 0 {
		return b.conn.Send(target, misconfiguredMessage)
	}
	var messageTemplate string
	if err == nil || err == errNoScript || err == errHelpRequested {
		messageTemplate = welcomeMessage + mentionMessage
	} else {
		messageTemplate = err.Error() + "\n\n" + mentionMessage
	}
	if b.config.WebHost != "" {
		defaultWebCommand := webCommand
		if !b.config.DefaultWeb {
			defaultWebCommand = noWebCommand
		}
		messageTemplate += " " + fmt.Sprintf(webMessage, defaultWebCommand)
	}
	if b.config.ShareEnabled() {
		messageTemplate += "\n\n" + shareMessage
		scripts = append(scripts, shareCommand)
	}
	replList := fmt.Sprintf("`%s`", strings.Join(scripts, "`, `"))
	defaultRecordCommand := recordCommand
	if !b.config.DefaultRecord {
		defaultRecordCommand = noRecordCommand
	}
	message := fmt.Sprintf(messageTemplate, b.conn.MentionBot(), scripts[0], replList, b.config.DefaultControlMode, b.config.DefaultSize.Name, b.config.DefaultWindowMode, b.config.DefaultAuthMode, defaultRecordCommand)
	return b.conn.Send(target, message)
}

func (b *Bot) runWebServer(ctx context.Context) error {
	_, port, err := net.SplitHostPort(b.config.WebHost)
	if err != nil {
		return err
	}
	if port == "" {
		port = "80"
	}
	errChan := make(chan error)
	go func() {
		http.HandleFunc("/", b.webHandler)
		errChan <- http.ListenAndServe(":"+port, nil)
	}()
	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return nil
	}
}

func (b *Bot) webHandler(w http.ResponseWriter, r *http.Request) {
	if err := b.webHandlerInternal(w, r); err != nil {
		slog.Error("web handler error; returning 404", "error", err)
		http.NotFound(w, r)
	}
}

func (b *Bot) webHandlerInternal(w http.ResponseWriter, r *http.Request) error {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 2 {
		return errors.New("invalid prefix")
	}
	prefix := parts[1]
	b.mu.RLock()
	var webPort int
	session := b.webPrefix[prefix]
	if session != nil {
		webPort = session.webPort
	}
	b.mu.RUnlock()
	if session == nil || webPort == 0 {
		return fmt.Errorf("session with prefix %s not found", prefix)
	}
	if len(parts) < 3 { // must be /prefix/, not just /prefix
		http.Redirect(w, r, r.URL.String()+"/", http.StatusTemporaryRedirect)
		return nil
	}
	proxy := &httputil.ReverseProxy{Director: func(r *http.Request) {
		r.URL.Scheme = "http"
		r.URL.Host = fmt.Sprintf("127.0.0.1:%d", webPort)
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/"+prefix)
		r.URL.RawPath = strings.TrimPrefix(r.URL.Path, "/"+prefix)
		r.Header.Set("Origin", fmt.Sprintf("http://%s", b.config.WebHost))
	}}
	proxy.ServeHTTP(w, r)
	return nil
}

func (b *Bot) runShareServer(ctx context.Context) error {
	if err := os.WriteFile(shareServerScriptFile, []byte(shareServerScriptSource), 0700); err != nil {
		return err
	}
	_, port, err := net.SplitHostPort(b.config.ShareHost)
	if err != nil {
		return err
	}
	server, err := b.sshServer(port)
	if err != nil {
		return err
	}
	errChan := make(chan error)
	go func() {
		errChan <- server.ListenAndServe()
	}()
	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return nil
	}
}

func (b *Bot) sshServer(port string) (*ssh.Server, error) {
	forwardHandler := &ssh.ForwardedTCPHandler{}
	server := &ssh.Server{
		Addr:                          fmt.Sprintf(":%s", port),
		Version:                       sshVersion,
		PasswordHandler:               nil,
		PublicKeyHandler:              nil,
		KeyboardInteractiveHandler:    nil,
		PtyCallback:                   b.sshPtyCallback,
		ReversePortForwardingCallback: b.sshReversePortForwardingCallback,
		Handler:                       b.sshSessionHandler,
		ServerConfigCallback:          b.sshServerConfigCallback,
		RequestHandlers: map[string]ssh.RequestHandler{
			"tcpip-forward":        forwardHandler.HandleSSHRequest,
			"cancel-tcpip-forward": forwardHandler.HandleSSHRequest,
		},
		ChannelHandlers: map[string]ssh.ChannelHandler{
			"session": ssh.DefaultSessionHandler,
		},
	}
	if err := server.SetOption(ssh.HostKeyFile(b.config.ShareKeyFile)); err != nil {
		return nil, err
	}
	return server, nil
}

// sshSessionHandler is the main SSH session handler. It returns the share script which creates a local tmux session
// and opens the reverse tunnel. The session is identified by the SSH user name. If the session is not found, the
// handler exits immediately.
func (b *Bot) sshSessionHandler(s ssh.Session) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sess, ok := b.shareUser[s.User()]
	if !ok {
		return
	}
	if len(s.Command()) != 1 {
		return
	}
	clientUser := s.Command()[0]
	if err := sess.WriteShareUserFile(clientUser); err != nil {
		slog.Error("cannot write share user file", "error", err)
		return
	}
	if err := sess.WriteShareClientScript(s); err != nil {
		slog.Error("cannot write session script", "error", err)
		return
	}
}

// sshReversePortForwardingCallback checks if the requested reverse tunnel host/port (ssh -R) matches the one
// that was assigned in the REPL share session and rejects/closes the connection if it doesn't
func (b *Bot) sshReversePortForwardingCallback(ctx ssh.Context, host string, port uint32) (allow bool) {
	conn, ok := ctx.Value(ssh.ContextKeyConn).(*gossh.ServerConn)
	if !ok {
		return false
	}
	defer func() {
		if !allow {
			slog.Info("rejecting connection", "remote", conn.RemoteAddr())
			conn.Close()
		}
	}()
	if port < 1024 || (host != "localhost" && host != "127.0.0.1") {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	sess, ok := b.shareUser[ctx.User()]
	if !ok || sess.conf.share == nil || sess.conf.share.relayPort != int(port) {
		return
	}
	sess.RegisterShareConn(conn)
	return true
}

// sshPtyCallback always returns false, thereby refusing any SSH client attempt to request a TTY
func (b *Bot) sshPtyCallback(ctx ssh.Context, pty ssh.Pty) bool {
	return false
}

// sshServerConfigCallback configures the SSH server algorithms to be more secure (see `ssh-audit` output)
func (b *Bot) sshServerConfigCallback(ctx ssh.Context) *gossh.ServerConfig {
	conf := &gossh.ServerConfig{}
	conf.KeyExchanges = []string{kexCurve25519SHA256}
	conf.Ciphers = []string{cipherAES128GCM, cipherED25519, cipherSSHRSA}
	conf.MACs = []string{macHMACSHA256ETM}
	return conf
}

func (b *Bot) checkSessionAllowed(channel, thread string, conf *sessionConfig) (allowed bool, err error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.sessions) >= b.config.MaxTotalSessions {
		ch := &channelID{Channel: channel, Thread: thread}
		return false, b.conn.Send(ch, maxTotalSessionsExceededMessage)
	}
	var userSessions int
	for _, sess := range b.sessions {
		if sess.conf.user == conf.user {
			userSessions++
		}
	}
	if userSessions >= b.config.MaxUserSessions {
		ch := &channelID{Channel: channel, Thread: thread}
		return false, b.conn.Send(ch, maxUserSessionsExceededMessage)
	}
	return true, nil
}

func (b *Bot) webUpdated(s *session, enabled bool, prefix string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if enabled {
		b.webPrefix[prefix] = s
	} else {
		delete(b.webPrefix, prefix)
	}
}
