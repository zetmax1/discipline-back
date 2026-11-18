// Package logging writes structured JSON logs and carries the request ID that
// ties a Gin request to the analytics service's logs.
package logging

import (
	"context"
	"io"
	"log"
	"log/slog"
)

type ctxKey struct{}

// WithRequestID returns ctx carrying the request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// RequestID returns the request ID in ctx, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// requestIDHandler adds request_id to every record logged with a request context.
type requestIDHandler struct{ slog.Handler }

func (h requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestIDHandler{h.Handler.WithAttrs(attrs)}
}

func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{h.Handler.WithGroup(name)}
}

// Setup makes JSON on w the default for slog and for the standard log package.
func Setup(w io.Writer) {
	slog.SetDefault(slog.New(requestIDHandler{slog.NewJSONHandler(w, nil)}))
	log.SetFlags(0) // slog adds the time
}
