// Package errtrack reports panics to Sentry when SENTRY_DSN is set. Without a DSN
// it does nothing. Events never carry request bodies, cookies, query strings or
// credentials.
package errtrack

import (
	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"

	"discipline-backend/internal/logging"
)

// Options configures Sentry; an empty DSN disables it.
type Options struct {
	DSN         string
	Environment string
	transport   sentry.Transport // tests only
}

// Init starts Sentry if a DSN is set and reports whether it did.
func Init(o Options) (bool, error) {
	if o.DSN == "" {
		return false, nil
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              o.DSN,
		Environment:      o.Environment,
		SendDefaultPII:   false,
		AttachStacktrace: true,
		Transport:        o.transport,
		BeforeSend:       scrub,
	})
	return err == nil, err
}

// keptHeaders are the only request headers an event may carry.
var keptHeaders = map[string]bool{"User-Agent": true, "Content-Type": true, "X-Request-Id": true, "X-Request-ID": true}

// scrub drops everything from the request that could hold secrets or personal data.
func scrub(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if e.Request != nil {
		e.Request.Data = ""
		e.Request.Cookies = ""
		e.Request.QueryString = ""
		e.Request.Env = nil
		for k := range e.Request.Headers {
			if !keptHeaders[k] {
				delete(e.Request.Headers, k)
			}
		}
	}
	e.User = sentry.User{}
	return e
}

// Middleware reports panics to Sentry, tagged with the request ID, then re-panics
// so the app's Recovery answers. Use it after RequestID and Recovery:
// r.Use(RequestID(), AccessLog(), Recovery(), errtrack.Middleware()...).
func Middleware() []gin.HandlerFunc {
	return []gin.HandlerFunc{
		sentrygin.New(sentrygin.Options{Repanic: true}),
		func(c *gin.Context) {
			if hub := sentrygin.GetHubFromContext(c); hub != nil {
				hub.Scope().SetTag("request_id", logging.RequestID(c.Request.Context()))
			}
			c.Next()
		},
	}
}
