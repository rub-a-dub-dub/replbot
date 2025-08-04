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
	rtm          *slack.RTM
	client       *slack.Client
	socketClient *socketmode.Client //nolint:unused // Socket Mode client
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
	client := slack.New(c.config.Token, slack.OptionDebug(c.config.Debug))
	c.client = client
	c.rtm = client.NewRTM()
	go c.rtm.ManageConnection()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-c.rtm.IncomingEvents:
				if ev := c.translateEvent(e); ev != nil {
					eventChan <- ev
				}
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
		_, responseTS, err := c.rtm.PostMessage(channel.Channel, options...)
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
		_, err := c.rtm.PostEphemeral(channel.Channel, userID, options...)
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
	ch, _, _, err := c.rtm.OpenConversation(&slack.OpenConversationParameters{
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
		_, _, _, err := c.rtm.UpdateMessage(channel.Channel, id, options...)
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

func (c *slackConn) translateEvent(event slack.RTMEvent) event {
	switch ev := event.Data.(type) {
	case *slack.ConnectedEvent:
		return c.handleConnectedEvent(ev)
	case *slack.ChannelJoinedEvent:
		return c.handleChannelJoinedEvent(ev)
	case *slack.MessageEvent:
		return c.handleMessageEvent(ev)
	case *slack.RTMError:
		return c.handleErrorEvent(ev)
	case *slack.ConnectionErrorEvent:
		return c.handleErrorEvent(ev)
	case *slack.InvalidAuthEvent:
		return &errorEvent{NewConfigError("INVALID_CREDENTIALS", "invalid credentials", nil)}
	default:
		return nil // Ignore other events
	}
}

func (c *slackConn) translateSocketModeEvent(evt socketmode.Event) event { //nolint:unused
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return nil
		}
		// Acknowledge immediately
		c.socketClient.Ack(*evt.Request)

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
	case socketmode.EventTypeConnected:
		slog.Info("connected to Slack via Socket Mode")
	}
	return nil
}

func (c *slackConn) handleMessageEvent(ev *slack.MessageEvent) event {
	if ev.User == "" || ev.SubType == "channel_join" {
		return nil // Ignore my own and join messages
	}
	return &messageEvent{
		ID:          ev.Timestamp,
		Channel:     ev.Channel,
		ChannelType: c.channelType(ev.Channel),
		Thread:      ev.ThreadTimestamp,
		User:        ev.User,
		Message:     ev.Text,
	}
}

func (c *slackConn) handleConnectedEvent(ev *slack.ConnectedEvent) event {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ev.Info == nil || ev.Info.User == nil || ev.Info.User.ID == "" {
		return errorEvent{NewBotError("MISSING_USER_INFO", "missing user info in connected event", nil)}
	}
	c.userID = ev.Info.User.ID
	slog.Info("slack connected", "user", ev.Info.User.Name, "id", ev.Info.User.ID)
	return nil
}

func (c *slackConn) handleChannelJoinedEvent(ev *slack.ChannelJoinedEvent) event {
	return &channelJoinedEvent{ev.Channel.ID}
}

func (c *slackConn) handleErrorEvent(err error) event {
	slog.Error("slack error", "error", err)
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
