package logging

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
)

// Redaction replaces sensitive values in logs before they are written.
const Redacted = "[REDACTED]"

var sensitiveKeyParts = []string{
	"apikey",
	"api_key",
	"api-key",
	"key",
	"token",
	"secret",
	"password",
	"passwd",
	"mnemonic",
	"seed",
	"private",
	"auth",
	"credential",
	"bearer",
}

var queryPairPattern = regexp.MustCompile(`(?i)([A-Za-z0-9_-]*(?:key|token|secret|password|passwd|auth|mnemonic)[A-Za-z0-9_-]*)=([^&\s` + "`" + `]+)`)

var userInfoPattern = regexp.MustCompile(`(https?://)[^/@\s]+@`)

// MaskRPCURL masks credentials, path, and query of an RPC URL so API keys
// embedded in the URL never reach logs, errors, or screens.
func MaskRPCURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return scrubText(trimmed)
	}
	var builder strings.Builder
	if parsed.Scheme != "" {
		builder.WriteString(parsed.Scheme)
		builder.WriteString("://")
	} else {
		builder.WriteString("//")
	}
	if parsed.User != nil {
		builder.WriteString(Redacted)
		builder.WriteString("@")
	}
	builder.WriteString(parsed.Host)
	if parsed.Path != "" && parsed.Path != "/" {
		builder.WriteString("***")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		builder.WriteString("?***")
	}
	return builder.String()
}

// ScrubText masks key-like query pairs and URL credentials inside free text
// such as error messages that embed a full RPC URL.
func ScrubText(text string) string {
	return scrubText(text)
}

func scrubText(text string) string {
	if text == "" {
		return ""
	}
	masked := userInfoPattern.ReplaceAllString(text, "${1}"+Redacted+"@")
	return queryPairPattern.ReplaceAllString(masked, "${1}="+Redacted)
}

func isSensitiveKey(key string) bool {
	lowered := strings.ToLower(key)
	for _, part := range sensitiveKeyParts {
		if strings.Contains(lowered, part) {
			return true
		}
	}
	return false
}

func maskValue(value any) any {
	switch typed := value.(type) {
	case string:
		return maskString(typed)
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			out[i] = maskString(item)
		}
		return out
	case error:
		if typed == nil {
			return typed
		}
		return scrubText(typed.Error())
	case *url.URL:
		if typed == nil {
			return typed
		}
		return MaskRPCURL(typed.String())
	default:
		return value
	}
}

func maskString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" && parsed.Scheme != "" {
		return MaskRPCURL(trimmed)
	}
	if queryPairPattern.MatchString(trimmed) || userInfoPattern.MatchString(trimmed) {
		return scrubText(trimmed)
	}
	return value
}

func redactAttr(attr slog.Attr) slog.Attr {
	if attr.Equal(slog.Attr{}) {
		return attr
	}
	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		redone := make([]slog.Attr, 0, len(group))
		for _, item := range group {
			redone = append(redone, redactAttr(item))
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(redone...)}
	}
	if isSensitiveKey(attr.Key) {
		return slog.String(attr.Key, Redacted)
	}
	if attr.Value.Kind() == slog.KindAny {
		return slog.Any(attr.Key, maskValue(attr.Value.Any()))
	}
	if attr.Value.Kind() == slog.KindString {
		return slog.String(attr.Key, maskString(attr.Value.String()))
	}
	return attr
}

type redactingHandler struct {
	inner slog.Handler
}

// NewHandler wraps inner with secret and RPC URL redaction.
func NewHandler(inner slog.Handler) slog.Handler {
	return &redactingHandler{inner: inner}
}

// New builds a redacting text logger writing to writer at level.
func New(writer io.Writer, level slog.Level) *slog.Logger {
	return slog.New(NewHandler(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level})))
}

// NewJSON builds a redacting JSON logger writing to writer at level.
func NewJSON(writer io.Writer, level slog.Level) *slog.Logger {
	return slog.New(NewHandler(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})))
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	redacted := slog.NewRecord(record.Time, record.Level, scrubText(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		redacted.AddAttrs(redactAttr(attr))
		return true
	})
	return h.inner.Handle(ctx, redacted)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redone := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		redone = append(redone, redactAttr(attr))
	}
	return &redactingHandler{inner: h.inner.WithAttrs(redone)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: h.inner.WithGroup(name)}
}
