package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestMaskRPCURLMasksKeyPathAndQuery(t *testing.T) {
	got := MaskRPCURL("https://rpc.example/v1/secret-path?apiKey=abc123&x=1")
	if strings.Contains(got, "abc123") || strings.Contains(got, "secret-path") || strings.Contains(got, "x=1") {
		t.Fatalf("masked URL still leaks: %s", got)
	}
	if !strings.Contains(got, "rpc.example") {
		t.Fatalf("host must remain visible: %s", got)
	}
}

func TestMaskRPCURLMasksUserInfo(t *testing.T) {
	got := MaskRPCURL("https://user:pass@rpc.example/rpc")
	if strings.Contains(got, "user") || strings.Contains(got, "pass") {
		t.Fatalf("userinfo leaked: %s", got)
	}
}

func TestMaskRPCURLInvalidInputScrubbed(t *testing.T) {
	got := MaskRPCURL("not a url apiKey=rawkey")
	if strings.Contains(got, "rawkey") {
		t.Fatalf("key leaked: %s", got)
	}
}

func TestLoggerRedactsSensitiveKeys(t *testing.T) {
	var buffer bytes.Buffer
	logger := New(&buffer, slog.LevelDebug)
	logger.Debug("connect", "apiKey", "super-secret", "url", "https://rpc.example/rpc?key=abc")
	output := buffer.String()
	if strings.Contains(output, "super-secret") || strings.Contains(output, "key=abc") {
		t.Fatalf("log leaked secrets: %s", output)
	}
	if !strings.Contains(output, "rpc.example") {
		t.Fatalf("expected host in log, got: %s", output)
	}
}

func TestLoggerRedactsMessageText(t *testing.T) {
	var buffer bytes.Buffer
	logger := New(&buffer, slog.LevelInfo)
	logger.Info("failed over to https://rpc.example/rpc?apiKey=leaky&chain=bsc", "attempt", 1)
	output := buffer.String()
	if strings.Contains(output, "leaky") {
		t.Fatalf("message leaked key: %s", output)
	}
}

func TestLoggerRedactsErrorMessageAttr(t *testing.T) {
	var buffer bytes.Buffer
	logger := New(&buffer, slog.LevelInfo)
	err := errors.New("dial failed for https://rpc.example/rpc?token=hidden")
	logger.Info("rpc call", "error", err)
	output := buffer.String()
	if strings.Contains(output, "hidden") {
		t.Fatalf("error attr leaked key: %s", output)
	}
}

func TestLoggerRedactsWithAttrsAndGroups(t *testing.T) {
	var buffer bytes.Buffer
	logger := New(&buffer, slog.LevelInfo).With("mnemonic", "abandon art able")
	child := logger.WithGroup("rpc")
	child.Info("probe", "endpoint", "https://rpc.example/v1?apiKey=zzz")
	output := buffer.String()
	if strings.Contains(output, "abandon") || strings.Contains(output, "apiKey=zzz") {
		t.Fatalf("leaked via with/group: %s", output)
	}
}

func TestScrubTextMasksQueryPairs(t *testing.T) {
	got := ScrubText("failed: https://rpc.example/rpc?apiKey=abc&public=1")
	if strings.Contains(got, "apiKey=abc") {
		t.Fatalf("query key leaked: %s", got)
	}
	if !strings.Contains(got, "public=1") {
		t.Fatalf("non-sensitive query should stay: %s", got)
	}
}

func TestJSONLoggerRedacts(t *testing.T) {
	var buffer bytes.Buffer
	logger := NewJSON(&buffer, slog.LevelInfo)
	logger.Info("connect", "password", "hunter2", "url", "https://rpc.example/rpc?apiKey=k")
	output := buffer.String()
	if strings.Contains(output, "hunter2") || strings.Contains(output, "apiKey=k") {
		t.Fatalf("json log leaked: %s", output)
	}
}
