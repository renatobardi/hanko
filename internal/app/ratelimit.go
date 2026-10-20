package app

import (
	"github.com/renatobardi/hanko/internal/ratelimit"
	"github.com/renatobardi/hanko/internal/settings"
)

// settingsLimits lê os tetos do rate limit de instance_settings a cada requisição (mudar vale sem reiniciar).
type settingsLimits struct{ m *settings.Manager }

func (l settingsLimits) Enabled() bool { return l.m.Bool(settings.RateLimitEnabled) }

func (l settingsLimits) PerMinute(c ratelimit.Class) int64 {
	switch c {
	case ratelimit.ClassWrite:
		return l.m.Int(settings.RateLimitWrites)
	case ratelimit.ClassIP:
		return l.m.Int(settings.RateLimitIP)
	case ratelimit.ClassMCP:
		return l.m.Int(settings.RateLimitMCP)
	case ratelimit.ClassWebhook:
		return l.m.Int(settings.RateLimitWebhooks)
	case ratelimit.ClassBrowserError:
		return l.m.Int(settings.RateLimitBrowserErrors)
	}
	return 0
}
