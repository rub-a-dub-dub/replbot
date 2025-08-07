package bot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/rub-a-dub-dub/replbot/config"
)

func TestRateLimiterAllowAndCleanup(t *testing.T) {
	conf := config.New("mem", "")
	conf.MessageRateLimit = config.RateLimit{Requests: 1, Burst: 1, Interval: time.Second}
	conf.RateLimitCleanupInterval = 50 * time.Millisecond

	rl := newRateLimiter(conf)
	allowed, _ := rl.allow("phil", opMessage)
	assert.True(t, allowed)

	allowed, delay := rl.allow("phil", opMessage)
	assert.False(t, allowed)
	assert.True(t, delay > 0)

	time.Sleep(conf.RateLimitCleanupInterval * 3)
	rl.mu.Lock()
	_, ok := rl.users["phil"]
	rl.mu.Unlock()
	assert.False(t, ok)
}
