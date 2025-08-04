package bot

import (
	"context"
	"fmt"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"heckel.io/replbot/config"
	"io"
	"log/slog"
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
)

type slackConn struct {
	client       *slack.Client
	socketClient *socketmode.Client
	cancel       context.CancelFunc
	userID       string
	config       *config.Config
	mu           sync.RWMutex
}

func newSlackConn(conf *config.Config) *slackConn {
	return &slackConn{
		config: conf,
	}
}

func (c *slackConn) Connect(ctx context.Context) (<-chan event, error) {
	eventChan := make(chan event)

	client := slack.New(c.config.Token,
		slack.OptionDebug(c.config.Debug),
		slack.OptionAppLevelToken(c.config.AppToken))

	socketClient := socketmode.New(client,
		socketmode.OptionDebug(c.config.Debug))

	c.client = client
	c.socketClient = socketClient

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go func() {
		defer close(eventChan)
		for {
			select {
			case <-ctx.Done():
				slog.Debug("socket mode event handler stopping due to context cancellation")
				return
			case evt, ok := <-socketClient.Events:
				if !ok {
					slog.Warn("socket mode events channel closed")
					return
				}
				if ev := c.translateSocketModeEvent(evt); ev != nil {
					select {
					case eventChan <- ev:
					case <-ctx.Done():
						slog.Debug("context cancelled while sending event")
						return
					}
				}
			}
		}
	}()

	go func() {
		if err := socketClient.RunContext(ctx); err != nil && ctx.Err() == nil {
			slog.Error("socket mode client error", "error", err)
			// Send error event to trigger reconnection logic
			select {
			case eventChan <- &errorEvent{fmt.Errorf("socket mode client error: %w", err)}:
			case <-ctx.Done():
			}
		}
	}()

	return eventChan, nil
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
		// Acknowledge immediately with error handling
		if evt.Request != nil {
			c.socketClient.Ack(*evt.Request)
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
		case *slackevents.MemberJoinedChannelEvent:
			return &channelJoinedEvent{ev.Channel}
		}
	case socketmode.EventTypeConnecting:
		slog.Info("connecting to Slack via Socket Mode")
	case socketmode.EventTypeConnectionError:
		slog.Error("Socket Mode connection error", "error", evt.Data)
		return &errorEvent{fmt.Errorf("connection error: %v", evt.Data)}
	case socketmode.EventTypeConnected:
		// Get bot info to set userID with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		
		authTest, err := c.client.AuthTestContext(ctx)
		if err != nil {
			slog.Error("failed to get bot info", "error", err)
			return &errorEvent{fmt.Errorf("authentication failed: %w", err)}
		}
		
		if authTest.UserID == "" {
			err := fmt.Errorf("authentication succeeded but no user ID returned")
			slog.Error("invalid auth response", "error", err)
			return &errorEvent{err}
		}
		
		c.mu.Lock()
		c.userID = authTest.UserID
		c.mu.Unlock()
		slog.Info("connected to Slack", "user", authTest.User, "id", authTest.UserID)
	}
	return nil
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
