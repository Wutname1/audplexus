package web

import (
	"context"
	"testing"

	"github.com/mstrhakr/audplexus/internal/database"
	"github.com/mstrhakr/audplexus/internal/mediaserver"
)

func TestNormalizeClientIPForLog(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: ""},
		{in: " 127.0.0.1 ", want: "127.0.0.1"},
		{in: "127.0.0.1:8080", want: "127.0.0.1"},
		{in: "[::1]:443", want: "127.0.0.1"},
		{in: "2001:db8::1", want: "2001:db8::1"},
		{in: "10.0.0.1, 192.168.1.10", want: "10.0.0.1"},
		{in: "not-an-ip", want: "not-an-ip"},
	}

	for _, tc := range tests {
		if got := normalizeClientIPForLog(tc.in); got != tc.want {
			t.Fatalf("normalizeClientIPForLog(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMediaServerLabel(t *testing.T) {
	if key, label := mediaServerLabel(mediaserver.TypeEmby); key != "emby" || label != "Emby" {
		t.Fatalf("mediaServerLabel(emby) = (%q,%q)", key, label)
	}
	if key, label := mediaServerLabel(mediaserver.TypePlex); key != "plex" || label != "Plex" {
		t.Fatalf("mediaServerLabel(plex) = (%q,%q)", key, label)
	}
	if key, label := mediaServerLabel(mediaserver.Type("unknown")); key != "plex" || label != "Plex" {
		t.Fatalf("mediaServerLabel(default) = (%q,%q)", key, label)
	}
}

func TestSettingsHelpers(t *testing.T) {
	stub := database.NewStubDB()
	s := &Server{db: stub}
	ctx := context.Background()

	if got := s.settingBool(ctx, "missing_bool", true); !got {
		t.Fatalf("settingBool missing default true should return true")
	}
	_ = stub.SetSetting(ctx, "bool_true", "1")
	_ = stub.SetSetting(ctx, "bool_false", "false")
	if !s.settingBool(ctx, "bool_true", false) {
		t.Fatalf("settingBool bool_true should be true")
	}
	if s.settingBool(ctx, "bool_false", true) {
		t.Fatalf("settingBool bool_false should be false")
	}

	if got := s.settingString(ctx, "missing_string", "fallback"); got != "fallback" {
		t.Fatalf("settingString missing = %q, want fallback", got)
	}
	_ = stub.SetSetting(ctx, "name", "  value  ")
	if got := s.settingString(ctx, "name", "fallback"); got != "value" {
		t.Fatalf("settingString trim = %q, want value", got)
	}

	if got := s.settingInt(ctx, "missing_int", 7); got != 7 {
		t.Fatalf("settingInt missing = %d, want 7", got)
	}
	_ = stub.SetSetting(ctx, "int_ok", "42")
	_ = stub.SetSetting(ctx, "int_bad", "abc")
	if got := s.settingInt(ctx, "int_ok", 0); got != 42 {
		t.Fatalf("settingInt int_ok = %d, want 42", got)
	}
	if got := s.settingInt(ctx, "int_bad", 9); got != 9 {
		t.Fatalf("settingInt int_bad = %d, want 9", got)
	}
}

func TestNormalizeTitle(t *testing.T) {
	if got := normalizeTitle(" The-Book: A_Test, Vol. 1 "); got != "the book a test vol 1" {
		t.Fatalf("normalizeTitle() = %q", got)
	}
}

func TestExtractRegionFromPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "/library/My Book [US]", want: "us"},
		{in: "/library/My Book [uk]", want: "uk"},
		{in: "/library/no-region", want: ""},
		{in: "/library/too-long [abcd]", want: ""},
		{in: "/library/bad [x]", want: ""},
	}

	for _, tc := range tests {
		if got := extractRegionFromPath(tc.in); got != tc.want {
			t.Fatalf("extractRegionFromPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
