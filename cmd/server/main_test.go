package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestMultiHandler_Enabled_TrueIfAnyHandlerEnabled(t *testing.T) {
	t.Parallel()

	// A handler that only enables Info and above
	h1 := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}) //nolint:sloglint // testing different level thresholds
	// A handler that only enables Error and above
	h2 := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}) //nolint:sloglint // testing different level thresholds

	m := &multiHandler{handlers: []slog.Handler{h1, h2}}

	if !m.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("expected Enabled to be true for Info when one handler accepts it")
	}
	if m.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("expected Enabled to be false for Debug when no handler accepts it")
	}
	if !m.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("expected Enabled to be true for Error")
	}
}

func TestMultiHandler_Handle_FansOutToAllHandlers(t *testing.T) {
	t.Parallel()

	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, nil)
	h2 := slog.NewJSONHandler(&buf2, nil)

	m := &multiHandler{handlers: []slog.Handler{h1, h2}}
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "hello world", 0)

	if err := m.Handle(context.Background(), record); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Contains(buf1.Bytes(), []byte("hello world")) {
		t.Fatalf("expected handler 1 to receive record, got %q", buf1.String())
	}
	if !bytes.Contains(buf2.Bytes(), []byte("hello world")) {
		t.Fatalf("expected handler 2 to receive record, got %q", buf2.String())
	}
}

func TestMultiHandler_WithAttrs_AppliesToAllHandlers(t *testing.T) {
	t.Parallel()

	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, nil)
	h2 := slog.NewJSONHandler(&buf2, nil)

	m := (&multiHandler{handlers: []slog.Handler{h1, h2}}).WithAttrs([]slog.Attr{
		slog.String("key", "value"),
	})

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	if err := m.Handle(context.Background(), record); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Contains(buf1.Bytes(), []byte(`"key":"value"`)) {
		t.Fatalf("expected handler 1 to have attr, got %q", buf1.String())
	}
	if !bytes.Contains(buf2.Bytes(), []byte(`"key":"value"`)) {
		t.Fatalf("expected handler 2 to have attr, got %q", buf2.String())
	}
}

func TestMultiHandler_WithGroup_AppliesToAllHandlers(t *testing.T) {
	t.Parallel()

	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, nil)
	h2 := slog.NewJSONHandler(&buf2, nil)

	m := (&multiHandler{handlers: []slog.Handler{h1, h2}}).WithGroup("mygroup")

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	record.AddAttrs(slog.String("inner", "val"))
	if err := m.Handle(context.Background(), record); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Contains(buf1.Bytes(), []byte(`"mygroup":{"inner":"val"}`)) {
		t.Fatalf("expected handler 1 to have grouped attr, got %q", buf1.String())
	}
	if !bytes.Contains(buf2.Bytes(), []byte(`"mygroup":{"inner":"val"}`)) {
		t.Fatalf("expected handler 2 to have grouped attr, got %q", buf2.String())
	}
}
