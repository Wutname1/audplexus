package library

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mstrhakr/audplexus/internal/database"
	"github.com/mstrhakr/audplexus/internal/mediaserver"
)

// integration-style test: real SQLite, real synthesis, real fan-out.
// Backends won't actually reach Plex/Emby (no URLs configured), so they
// return SkippedNotConfigured outcomes — which is exactly what we want
// to assert the contract (no silent no-ops, per-destination state recorded).
func TestDestinationManagerFanOutRecordsPerDestinationState(t *testing.T) {
	db, err := database.NewSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()

	// Create a book.
	book := &database.Book{ASIN: "B0FANOUT", Title: "FanOut Book", Status: database.BookStatusComplete}
	if err := db.UpsertBook(ctx, book); err != nil {
		t.Fatalf("UpsertBook: %v", err)
	}

	// Create two destinations with placeholder URLs that DON'T resolve.
	// Backends now read URL/token/library_id from the destination row
	// (PR-F's WithDestination binding), so they'll attempt the call and
	// fail at the DNS/dial layer — outcome is "failed", not "skipped".
	// That's the correct new contract: row populated → backend tries
	// → typed Outcome reports the real error.
	dest1 := &database.LibraryDestination{
		ID: "dest-plex", DisplayName: "Plex", Type: database.LibraryDestinationTypePlex,
		Enabled: true, URL: "http://plex.invalid", PlexToken: "t", PlexSectionID: "5",
	}
	dest2 := &database.LibraryDestination{
		ID: "dest-emby", DisplayName: "Emby", Type: database.LibraryDestinationTypeEmby,
		Enabled: true, URL: "http://emby.invalid", APIKey: "k", LibraryID: "1",
	}
	for _, d := range []*database.LibraryDestination{dest1, dest2} {
		if err := db.CreateLibraryDestination(ctx, d); err != nil {
			t.Fatalf("CreateLibraryDestination: %v", err)
		}
	}

	mgr := NewDestinationManager(db, nil, "/audiobooks", 2)

	results := mgr.FanOut(ctx, mediaserver.OrganizedBook{
		BookID: book.ID, Title: book.Title, ASIN: book.ASIN,
		LocalPath: "/audiobooks/test.m4b",
	})

	if len(results) != 2 {
		t.Fatalf("FanOut returned %d results, want 2 (one per destination)", len(results))
	}

	// Every destination should have a row recorded in book_library_destinations.
	bds, err := db.GetBookDestinations(ctx, book.ID)
	if err != nil {
		t.Fatalf("GetBookDestinations: %v", err)
	}
	if len(bds) != 2 {
		t.Fatalf("expected 2 book_library_destinations rows, got %d", len(bds))
	}
	for _, bd := range bds {
		if bd.AttemptCount != 1 {
			t.Errorf("dest=%s expected attempt_count=1, got %d", bd.DestinationID, bd.AttemptCount)
		}
		if bd.LastAttemptedAt == nil {
			t.Errorf("dest=%s last_attempted_at should be populated", bd.DestinationID)
		}
		if bd.PerOpOutcomes == "" {
			t.Errorf("dest=%s per_op_outcomes should be a non-empty JSON object", bd.DestinationID)
		}
		if !strings.Contains(bd.PerOpOutcomes, "\"failed\"") {
			// Backends now read URL from the destination row. With an
			// invalid hostname, the scan trigger fails at DNS/dial — that
			// is the exact contract we want: typed outcome reports the
			// real failure rather than silently no-op'ing.
			t.Errorf("dest=%s expected failed status in per_op_outcomes, got %q", bd.DestinationID, bd.PerOpOutcomes)
		}
		if bd.SyncState != database.BookDestSyncFailed {
			t.Errorf("dest=%s expected sync_state=failed, got %q", bd.DestinationID, bd.SyncState)
		}
	}
}

func TestDestinationManagerFanOutNoEnabledDestinations(t *testing.T) {
	db, err := database.NewSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	mgr := NewDestinationManager(db, nil, "/audiobooks", 2)

	// No destinations created — fan-out is a no-op.
	results := mgr.FanOut(context.Background(), mediaserver.OrganizedBook{BookID: 1})
	if results != nil && len(results) != 0 {
		t.Errorf("expected 0 results with no destinations, got %d", len(results))
	}
}

func TestSummarizeOutcomesState(t *testing.T) {
	cases := []struct {
		name     string
		outcomes []mediaserver.Outcome
		want     database.BookDestinationSyncState
	}{
		{
			name:     "all succeeded → synced",
			outcomes: []mediaserver.Outcome{{Operation: "scan_trigger", Status: mediaserver.OutcomeSucceeded}, {Operation: "item_match", Status: mediaserver.OutcomeSucceeded}},
			want:     database.BookDestSyncSynced,
		},
		{
			name:     "any failed → failed",
			outcomes: []mediaserver.Outcome{{Operation: "scan_trigger", Status: mediaserver.OutcomeSucceeded}, {Operation: "item_match", Status: mediaserver.OutcomeFailed}},
			want:     database.BookDestSyncFailed,
		},
		{
			name:     "skipped_not_configured short-circuit → failed (destination not ready)",
			outcomes: []mediaserver.Outcome{{Operation: "scan_trigger", Status: mediaserver.OutcomeSkippedNotConfigured}},
			want:     database.BookDestSyncFailed,
		},
		{
			name:     "deferred + succeeded → synced",
			outcomes: []mediaserver.Outcome{{Operation: "scan_trigger", Status: mediaserver.OutcomeSucceeded}, {Operation: "item_match", Status: mediaserver.OutcomeDeferred}},
			want:     database.BookDestSyncSynced,
		},
		{
			name:     "empty outcomes → pending",
			outcomes: nil,
			want:     database.BookDestSyncPending,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeOutcomesState(tc.outcomes)
			if got != tc.want {
				t.Errorf("summarizeOutcomesState = %q, want %q", got, tc.want)
			}
		})
	}
}

// stubBackend is a minimal mediaserver.Backend used by unit tests that need
// to control TriggerLibraryScan / ReconcileLibrary results without real
// network calls. It is NOT registered with the factory — tests that need it
// build DestinationBackend slices directly and call the fan-out helpers.
type stubBackend struct {
	scanItems    int
	scanErr      error
	reconcileErr error
}

func (s *stubBackend) Name() string                                             { return "stub" }
func (s *stubBackend) Configured(_ context.Context) bool                       { return true }
func (s *stubBackend) Capabilities() mediaserver.CapabilitySet                 { return mediaserver.CapabilitySet{} }
func (s *stubBackend) OnBookOrganized(_ context.Context, _ mediaserver.OrganizedBook) []mediaserver.Outcome {
	return nil
}
func (s *stubBackend) ReconcileLibrary(_ context.Context, progressFn func(int, int)) error {
	if progressFn != nil {
		progressFn(5, 10)
	}
	return s.reconcileErr
}
func (s *stubBackend) TriggerLibraryScan(_ context.Context) (int, error) {
	return s.scanItems, s.scanErr
}
func (s *stubBackend) LibraryItemCount(_ context.Context) (int, error) { return 0, nil }

// triggerScanAllWithFnFromDests drives the fan-out logic with pre-built
// backends, bypassing the real DB ListEnabled call, so tests can inject stubs.
func triggerScanAllWithFnFromDests(ctx context.Context, dests []DestinationBackend, maxConcurrency int, subFn SubPhaseFn) (int, []DestinationScanResult) {
	return fanOutScanWithFn(ctx, dests, maxConcurrency, subFn)
}

func reconcileAllWithFnFromDests(ctx context.Context, dests []DestinationBackend, maxConcurrency int, subFn SubPhaseFn, progressFn func(int, int)) []DestinationReconcileResult {
	return fanOutReconcileWithFn(ctx, dests, maxConcurrency, subFn, progressFn)
}

func TestTriggerScanAllWithFn_CallsSubFnPerDestination(t *testing.T) {
	type call struct{ id, status string }
	var mu sync.Mutex
	var calls []call

	subFn := func(id, label, status, message string, current, total int) {
		mu.Lock()
		calls = append(calls, call{id, status})
		mu.Unlock()
	}

	dests := []DestinationBackend{
		{Row: database.LibraryDestination{ID: "dest-a", DisplayName: "Alpha"}, Backend: &stubBackend{scanItems: 42}},
		{Row: database.LibraryDestination{ID: "dest-b", DisplayName: "Beta"}, Backend: &stubBackend{scanItems: 17}},
	}

	total, results := triggerScanAllWithFnFromDests(context.Background(), dests, 2, subFn)

	if total != 42 {
		t.Errorf("expected max items = 42, got %d", total)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("dest %s unexpected error: %v", r.Destination.ID, r.Err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	statusesForID := func(id string) []string {
		var ss []string
		for _, c := range calls {
			if c.id == id {
				ss = append(ss, c.status)
			}
		}
		return ss
	}
	for _, id := range []string{"dest-a", "dest-b"} {
		ss := statusesForID(id)
		if len(ss) < 2 {
			t.Errorf("dest %s: expected ≥2 subFn calls (running+terminal), got %v", id, ss)
			continue
		}
		last := ss[len(ss)-1]
		if last != "complete" {
			t.Errorf("dest %s: last status should be complete, got %q (calls: %v)", id, last, ss)
		}
	}
}

func TestTriggerScanAllWithFn_ReportsFailedOnError(t *testing.T) {
	type call struct{ id, status string }
	var mu sync.Mutex
	var calls []call

	subFn := func(id, label, status, message string, current, total int) {
		mu.Lock()
		calls = append(calls, call{id, status})
		mu.Unlock()
	}

	dests := []DestinationBackend{
		{Row: database.LibraryDestination{ID: "dest-err", DisplayName: "ErrDest"}, Backend: &stubBackend{scanErr: context.DeadlineExceeded}},
	}

	_, results := triggerScanAllWithFnFromDests(context.Background(), dests, 2, subFn)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Error("expected error result, got nil")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("expected at least one subFn call")
	}
	last := calls[len(calls)-1]
	if last.status != "failed" {
		t.Errorf("expected last call status=failed, got %q", last.status)
	}
}

func TestReconcileAllWithFn_CallsSubFnAndProgressFn(t *testing.T) {
	type call struct{ id, status string }
	var mu sync.Mutex
	var calls []call

	subFn := func(id, label, status, message string, current, total int) {
		mu.Lock()
		calls = append(calls, call{id, status})
		mu.Unlock()
	}

	var progressCalls int
	progressFn := func(_, _ int) { progressCalls++ }

	dests := []DestinationBackend{
		{Row: database.LibraryDestination{ID: "dest-a", DisplayName: "Alpha"}, Backend: &stubBackend{}},
	}

	results := reconcileAllWithFnFromDests(context.Background(), dests, 2, subFn, progressFn)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("unexpected error: %v", results[0].Err)
	}
	if progressCalls == 0 {
		t.Error("expected progressFn to be called at least once (stub calls it once)")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("expected at least one subFn call")
	}
	last := calls[len(calls)-1]
	if last.status != "complete" {
		t.Errorf("expected last status=complete, got %q (all calls: %v)", last.status, calls)
	}
}
