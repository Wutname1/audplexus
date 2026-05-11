package library

import (
	"testing"
)

func TestFileValidator_DetectSuspicious(t *testing.T) {
	fv := NewFileValidator(nil, nil)

	if bad, reason := fv.detectSuspicious(3600, 128, 1000); !bad || reason == "" {
		t.Fatalf("small file should be suspicious")
	}
	if bad, reason := fv.detectSuspicious(120, 128, 10*1024*1024); !bad || reason == "" {
		t.Fatalf("short duration should be suspicious")
	}
	if bad, reason := fv.detectSuspicious(3600, 32, 50*1024*1024); !bad || reason == "" {
		t.Fatalf("low bitrate should be suspicious")
	}
	if bad, reason := fv.detectSuspicious(3600, 128, 20_000_000_000); !bad || reason == "" {
		t.Fatalf("oversized file should be suspicious")
	}

	if bad, reason := fv.detectSuspicious(3600, 128, 40*1024*1024); bad || reason != "" {
		t.Fatalf("normal characteristics should be non-suspicious, got bad=%v reason=%q", bad, reason)
	}
}

func TestFileValidator_ExtractASIN(t *testing.T) {
	fv := NewFileValidator(nil, nil)

	if got := fv.extractASIN("/library/B012345678.m4b"); got != "B012345678" {
		t.Fatalf("extractASIN filename = %q, want B012345678", got)
	}
	if got := fv.extractASIN("/library/Author/B012345678 Title/book.m4b"); got != "B012345678" {
		t.Fatalf("extractASIN parent dir = %q, want B012345678", got)
	}
	if got := fv.extractASIN("/library/not-an-asin-file.m4b"); got != "" {
		t.Fatalf("extractASIN invalid should be empty, got %q", got)
	}
}

func TestFileValidator_ProbeNoFFmpeg(t *testing.T) {
	fv := NewFileValidator(nil, nil)
	if _, _, _, err := fv.probeFile("/tmp/a.m4b"); err == nil {
		t.Fatalf("probeFile with nil ffmpeg should error")
	}
}

func TestIsAlphanumeric(t *testing.T) {
	if !isAlphanumeric("B012345678") {
		t.Fatalf("expected true for alphanumeric")
	}
	if isAlphanumeric("B01234-678") {
		t.Fatalf("expected false for non-alphanumeric")
	}
}
