package bot

import (
	"expvar"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"github.com/rub-a-dub-dub/replbot/config"
)

type operation int

const (
	opMessage operation = iota
	opSession
	opCommand
)

func (o operation) String() string {
	switch o {
	case opMessage:
		return "message"
	case opSession:
		return "session"
	case opCommand:
		return "command"
	default:
		return "unknown"
	}
}

var rateLimitHits = expvar.NewMap("ratelimit_hits")

type rateLimiter struct {
	conf  *config.Config
	mu    sync.Mutex
	users map[string]*userLimiter
}

type userLimiter struct {
	limits   map[operation]*rate.Limiter
	lastSeen time.Time
}

func newRateLimiter(conf *config.Config) *rateLimiter {
	rl := &rateLimiter{conf: conf, users: make(map[string]*userLimiter)}
	if conf.RateLimitCleanupInterval > 0 {
		go rl.cleanupLoop()
	}
	return rl
}

func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.conf.RateLimitCleanupInterval)
	for range ticker.C {
		rl.mu.Lock()
		for u, ul := range rl.users {
			if time.Since(ul.lastSeen) > rl.conf.RateLimitCleanupInterval {
				delete(rl.users, u)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *rateLimiter) allow(user string, op operation) (bool, time.Duration) {
	conf := rl.opConfig(op)
	if conf.Requests <= 0 || conf.Interval <= 0 {
		return true, 0
	}
	rl.mu.Lock()
	ul := rl.users[user]
	if ul == nil {
		ul = &userLimiter{limits: make(map[operation]*rate.Limiter)}
		rl.users[user] = ul
	}
	lim := ul.limits[op]
	if lim == nil {
		r := rate.Every(conf.Interval / time.Duration(conf.Requests))
		lim = rate.NewLimiter(r, conf.Burst)
		ul.limits[op] = lim
	}
	ul.lastSeen = time.Now()
	rl.mu.Unlock()

	if lim.Allow() {
		return true, 0
	}
	res := lim.Reserve()
	if !res.OK() {
		rateLimitHits.Add(op.String(), 1)
		return false, conf.Interval
	}
	delay := res.Delay()
	res.Cancel()
	rateLimitHits.Add(op.String(), 1)
	return false, delay
}

func (rl *rateLimiter) opConfig(op operation) config.RateLimit {
	switch op {
	case opMessage:
		return rl.conf.MessageRateLimit
	case opSession:
		return rl.conf.SessionRateLimit
	case opCommand:
		return rl.conf.CommandRateLimit
	default:
		return config.RateLimit{}
	}
}
