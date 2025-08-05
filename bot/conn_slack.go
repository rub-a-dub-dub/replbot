package bot

import (
	"context"
	"fmt"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"golang.org/x/sync/errgroup"
	"heckel.io/replbot/config"
	"io"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	slackLinkWithTextRegex = regexp.MustCompile(`<https?://[^|\s]+\|([^>]+)>`)
	slackRawLinkRegex      = regexp.MustCompile(`<(https?://[^|\s]+)>`)
	slackCodeBlockRegex    = regexp.MustCompile("```([^`]+)```")
	slackCodeRegex         = regexp.MustCompile("`([^`]+)`")
	slackUserLinkRegex     = regexp.MustCompile(`<@(U[^>]+)>`)
	slackMacQuotesRegex    = regexp.MustCompile(`[“”]`)
	slackReplacer          = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">") // see slackutilsx.go, EscapeMessage
)

const (
	additionalRateLimitDuration = 500 * time.Millisecond

	// Connection retry configuration
	maxReconnectAttempts = 10
	baseRetryDelay       = 1 * time.Second
	maxRetryDelay        = 60 * time.Second

	// Event channel buffer size for better performance
	eventChannelBuffer = 100
)

type slackConn struct {
	client       *slack.Client
	socketClient *socketmode.Client
	cancel       context.CancelFunc
	userID       string
	config       *config.Config
	mu           sync.RWMutex

	// Connection state management
	connected      bool
	reconnectCount int
	lastError      error
}

func newSlackConn(conf *config.Config) *slackConn {
	return &slackConn{
		config: conf,
	}
}

func (c *slackConn) Connect(ctx context.Context) (<-chan event, error) {
	// Use buffered channel for better performance
	eventChan := make(chan event, eventChannelBuffer)

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	// Initialize connection with retry logic
	if err := c.initializeConnection(ctx); err != nil {
		cancel()
		close(eventChan)
		return nil, fmt.Errorf("failed to initialize connection: %w", err)
	}

	// Use errgroup for coordinated goroutine management
	g, gctx := errgroup.WithContext(ctx)

	// Start event processing goroutine
	g.Go(func() error {
		defer close(eventChan)
		return c.processEvents(gctx, eventChan)
	})

	// Start connection management goroutine
	g.Go(func() error {
		return c.manageConnection(gctx, eventChan)
	})

	// Start connection health monitoring goroutine
	g.Go(func() error {
		return c.monitorConnection(gctx, eventChan)
	})

	// Handle errgroup errors in a separate goroutine
	go func() {
		if err := g.Wait(); err != nil && ctx.Err() == nil {
			slog.Error("socket mode connection group error", "error", err)
			// Send error event for upper layers to handle
			select {
			case eventChan <- &errorEvent{fmt.Errorf("connection group error: %w", err)}:
			case <-ctx.Done():
			}
		}
	}()

	return eventChan, nil
}

// initializeConnection sets up the initial Socket Mode connection with retry logic
func (c *slackConn) initializeConnection(ctx context.Context) error {
	client := slack.New(c.config.Token,
		slack.OptionDebug(c.config.Debug),
		slack.OptionAppLevelToken(c.config.AppToken))

	socketClient := socketmode.New(client,
		socketmode.OptionDebug(c.config.Debug))

	c.mu.Lock()
	c.client = client
	c.socketClient = socketClient
	c.connected = false
	c.reconnectCount = 0
	c.lastError = nil
	c.mu.Unlock()

	// Perform initial user ID initialization with retry
	return c.initializeUserID(ctx)
}

// initializeUserID gets bot info to set userID with retry logic
func (c *slackConn) initializeUserID(ctx context.Context) error {
	for attempt := 0; attempt < maxReconnectAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		authCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		authTest, err := c.client.AuthTestContext(authCtx)
		cancel()

		if err == nil && authTest.UserID != "" {
			c.mu.Lock()
			c.userID = authTest.UserID
			c.connected = true
			c.mu.Unlock()
			slog.Info("initialized Slack connection", "user", authTest.User, "id", authTest.UserID)
			return nil
		}

		if attempt < maxReconnectAttempts-1 {
			delay := c.calculateRetryDelay(attempt)
			slog.Warn("auth test failed, retrying", "error", err, "attempt", attempt+1, "delay", delay)

			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return fmt.Errorf("failed to initialize user ID after %d attempts", maxReconnectAttempts)
}

// processEvents handles incoming Socket Mode events
func (c *slackConn) processEvents(ctx context.Context, eventChan chan<- event) error {
	slog.Debug("starting socket mode event processor")
	defer slog.Debug("socket mode event processor stopped")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt, ok := <-c.socketClient.Events:
			if !ok {
				slog.Warn("socket mode events channel closed")
				c.mu.Lock()
				c.connected = false
				c.mu.Unlock()
				return fmt.Errorf("socket mode events channel closed")
			}

			if ev := c.translateSocketModeEvent(evt); ev != nil {
				select {
				case eventChan <- ev:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

// manageConnection runs the Socket Mode client with error handling
func (c *slackConn) manageConnection(ctx context.Context, eventChan chan<- event) error {
	slog.Debug("starting socket mode connection manager")
	defer slog.Debug("socket mode connection manager stopped")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if !c.isConnected() {
				// Attempt to reconnect
				if err := c.reconnect(ctx); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					slog.Error("reconnection failed", "error", err)
					continue
				}
			}

			// Run the socket client
			if err := c.socketClient.RunContext(ctx); err != nil && ctx.Err() == nil {
				slog.Error("socket mode client error", "error", err)
				c.mu.Lock()
				c.connected = false
				c.lastError = err
				c.mu.Unlock()

				// Send error event
				select {
				case eventChan <- &errorEvent{fmt.Errorf("socket mode client error: %w", err)}:
				case <-ctx.Done():
					return ctx.Err()
				}

				// Wait before retry
				delay := c.calculateRetryDelay(c.reconnectCount)
				select {
				case <-time.After(delay):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

// monitorConnection periodically checks connection health
func (c *slackConn) monitorConnection(ctx context.Context, eventChan chan<- event) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if !c.isConnected() {
				slog.Warn("connection health check failed - connection lost")
				// Connection will be handled by manageConnection goroutine
			}
		}
	}
}

// reconnect attempts to reconnect with exponential backoff
func (c *slackConn) reconnect(ctx context.Context) error {
	c.mu.Lock()
	attempt := c.reconnectCount
	c.reconnectCount++
	c.mu.Unlock()

	if attempt >= maxReconnectAttempts {
		return fmt.Errorf("maximum reconnection attempts (%d) exceeded", maxReconnectAttempts)
	}

	delay := c.calculateRetryDelay(attempt)
	slog.Info("attempting to reconnect", "attempt", attempt+1, "delay", delay)

	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return ctx.Err()
	}

	// Reinitialize the connection
	return c.initializeUserID(ctx)
}

// calculateRetryDelay calculates exponential backoff delay
func (c *slackConn) calculateRetryDelay(attempt int) time.Duration {
	delay := time.Duration(math.Pow(2, float64(attempt))) * baseRetryDelay
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	return delay
}

// isConnected checks if the connection is active
func (c *slackConn) isConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

func (c *slackConn) Send(channel *channelID, message string) error {
	_, err := c.SendWithID(channel, message)
	return err
}

func (c *slackConn) SendWithID(channel *channelID, message string) (string, error) {
	options := c.postOptions(channel, slack.MsgOptionText(message, false))
	for {
		_, responseTS, err := c.client.PostMessage(channel.Channel, options...)
		if err == nil {
			return responseTS, nil
		}
		if e, ok := err.(*slack.RateLimitedError); ok {
			slog.Warn("rate limited; sleeping before re-sending", "error", err)
			time.Sleep(e.RetryAfter + additionalRateLimitDuration)
			continue
		}
		return "", err
	}
}

func (c *slackConn) SendEphemeral(channel *channelID, userID, message string) error {
	options := c.postOptions(channel, slack.MsgOptionText(message, false))
	for {
		_, err := c.client.PostEphemeral(channel.Channel, userID, options...)
		if err == nil {
			return nil
		}
		if e, ok := err.(*slack.RateLimitedError); ok {
			slog.Warn("rate limited; sleeping before re-sending", "error", err)
			time.Sleep(e.RetryAfter + additionalRateLimitDuration)
			continue
		}
		return err
	}
}

func (c *slackConn) SendDM(userID string, message string) error {
	ch, _, _, err := c.client.OpenConversation(&slack.OpenConversationParameters{
		ReturnIM: true,
		Users:    []string{userID},
	})
	if err != nil {
		return err
	}
	channel := &channelID{ch.ID, ""}
	return c.Send(channel, message)
}

func (c *slackConn) UploadFile(channel *channelID, message string, filename string, filetype string, file io.Reader) error {
	_, err := c.client.UploadFileV2(slack.UploadFileV2Parameters{
		Reader:          file,
		Filename:        filename,
		Channel:         channel.Channel,
		InitialComment:  message,
		ThreadTimestamp: channel.Thread,
	})
	return err
}

func (c *slackConn) Update(channel *channelID, id string, message string) error {
	options := c.postOptions(channel, slack.MsgOptionText(message, false))
	for {
		_, _, _, err := c.client.UpdateMessage(channel.Channel, id, options...)
		if err == nil {
			return nil
		}
		if e, ok := err.(*slack.RateLimitedError); ok {
			slog.Warn("rate limited; sleeping before re-sending", "error", err)
			time.Sleep(e.RetryAfter + additionalRateLimitDuration)
			continue
		}
		return err
	}
}

func (c *slackConn) Archive(_ *channelID) error {
	return nil
}

func (c *slackConn) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

func (c *slackConn) MentionBot() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return fmt.Sprintf("<@%s>", c.userID)
}

func (c *slackConn) Mention(user string) string {
	return fmt.Sprintf("<@%s>", user)
}

func (c *slackConn) ParseMention(user string) (string, error) {
	if matches := slackUserLinkRegex.FindStringSubmatch(user); len(matches) > 0 {
		return matches[1], nil
	}
	return "", NewValidationError("INVALID_USER", "invalid user", nil)
}

func (c *slackConn) Unescape(s string) string {
	s = slackLinkWithTextRegex.ReplaceAllString(s, "$1")
	s = slackRawLinkRegex.ReplaceAllString(s, "$1")
	s = slackCodeBlockRegex.ReplaceAllString(s, "$1")
	s = slackCodeRegex.ReplaceAllString(s, "$1")
	s = slackUserLinkRegex.ReplaceAllString(s, "")   // Remove entirely!
	s = slackMacQuotesRegex.ReplaceAllString(s, `"`) // See issue #14, Mac client sends wrong quotes
	s = slackReplacer.Replace(s)                     // Must happen last!
	return s
}

func (c *slackConn) translateSocketModeEvent(evt socketmode.Event) event {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			slog.Error("failed to cast event data to EventsAPIEvent")
			return nil
		}
		// Acknowledge immediately with proper error handling
		if evt.Request != nil {
			if err := c.acknowledgeEvent(*evt.Request); err != nil {
				slog.Error("failed to acknowledge event", "error", err, "event_type", evt.Type)
				// Continue processing despite acknowledgment failure
			}
		} else {
			slog.Warn("received EventsAPI event without request to acknowledge")
		}

		switch ev := eventsAPIEvent.InnerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			return &messageEvent{
				ID:          ev.TimeStamp,
				Channel:     ev.Channel,
				ChannelType: c.channelType(ev.Channel),
				Thread:      ev.ThreadTimeStamp,
				User:        ev.User,
				Message:     ev.Text,
			}
		case *slackevents.AppMentionEvent:
			return &messageEvent{
				ID:          ev.TimeStamp,
				Channel:     ev.Channel,
				ChannelType: c.channelType(ev.Channel),
				Thread:      ev.ThreadTimeStamp,
				User:        ev.User,
				Message:     ev.Text,
			}
		case *slackevents.MemberJoinedChannelEvent:
			return &channelJoinedEvent{ev.Channel}
		}
	case socketmode.EventTypeConnecting:
		slog.Info("connecting to Slack via Socket Mode")
	case socketmode.EventTypeConnectionError:
		slog.Error("Socket Mode connection error", "error", evt.Data)
		return &errorEvent{fmt.Errorf("connection error: %v", evt.Data)}
	case socketmode.EventTypeConnected:
		// Connection is established - mark as connected
		c.mu.Lock()
		c.connected = true
		c.reconnectCount = 0 // Reset reconnect count on successful connection
		c.mu.Unlock()
		slog.Info("Socket Mode connection established")
	}
	return nil
}

// acknowledgeEvent safely acknowledges a Socket Mode event with error handling
func (c *slackConn) acknowledgeEvent(req socketmode.Request) error {
	// Use a timeout to prevent hanging on acknowledgment
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic during event acknowledgment: %v", r)
			}
		}()
		c.socketClient.Ack(req)
		done <- nil
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("event acknowledgment timed out after 5s")
	}
}

func (c *slackConn) channelType(ch string) channelType {
	if strings.HasPrefix(ch, "C") {
		return channelTypeChannel
	} else if strings.HasPrefix(ch, "D") {
		return channelTypeDM
	}
	return channelTypeUnknown
}

func (c *slackConn) postOptions(channel *channelID, msg slack.MsgOption) []slack.MsgOption {
	options := []slack.MsgOption{msg, slack.MsgOptionDisableLinkUnfurl(), slack.MsgOptionDisableMediaUnfurl()}
	if channel.Thread != "" {
		options = append(options, slack.MsgOptionTS(channel.Thread))
	}
	return options
}
