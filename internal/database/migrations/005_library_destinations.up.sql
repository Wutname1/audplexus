-- Library destinations: typed per-type config, replaces single-active-backend
-- model. Multiple destinations of the same type are allowed (e.g. household
-- Plex + parents' Plex). Per-type columns instead of a JSON blob keep
-- migrations honest about required fields and let redaction rules be
-- explicit at the application layer.
CREATE TABLE library_destinations (
    id                    TEXT PRIMARY KEY,
    display_name          TEXT NOT NULL,
    type                  TEXT NOT NULL,                  -- 'plex' | 'emby' | 'jellyfin' | 'abs'
    enabled               INTEGER NOT NULL DEFAULT 1,     -- 0 = disabled (no fan-out, no reconcile)
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,

    -- Type-specific config columns. NULL for non-matching types.
    url                   TEXT,                           -- all types
    api_key               TEXT,                           -- emby / jellyfin / abs (sensitive — redact in logs)
    plex_token            TEXT,                           -- plex (sensitive — redact)
    plex_section_id       TEXT,                           -- plex
    library_id            TEXT,                           -- emby / jellyfin / abs
    audiobook_path        TEXT,                           -- per-destination path translation source
    destination_path      TEXT,                           -- per-destination path translation target

    -- Health (refreshed by background poll, surfaced in Settings UI).
    last_health_check_at  TEXT,
    last_health_check_ok  INTEGER,
    last_health_check_err TEXT,

    CHECK (
        (type = 'plex'     AND url IS NOT NULL AND plex_token IS NOT NULL AND plex_section_id IS NOT NULL) OR
        (type = 'emby'     AND url IS NOT NULL AND api_key IS NOT NULL    AND library_id IS NOT NULL)      OR
        (type = 'jellyfin' AND url IS NOT NULL AND api_key IS NOT NULL    AND library_id IS NOT NULL)      OR
        (type = 'abs'      AND url IS NOT NULL AND api_key IS NOT NULL    AND library_id IS NOT NULL)
    )
);

CREATE INDEX library_destinations_enabled_idx ON library_destinations(enabled);
CREATE INDEX library_destinations_type_idx ON library_destinations(type);

-- Per-(book, destination) state. Replaces the 1:1 books.media_server_id /
-- media_server_title pair so a single Audplexus install can fan out to
-- multiple destinations and track each independently. Codex review flagged
-- the need for last_attempted_at, attempt_count, and disabled_reason
-- separately from sync_state — each captures something the simpler model
-- couldn't represent (stale success, retry budgets, admin-stop-trying).
CREATE TABLE book_library_destinations (
    book_id              INTEGER NOT NULL,
    destination_id       TEXT NOT NULL,

    -- Identity on the destination side.
    server_item_id       TEXT,
    server_item_title    TEXT,

    -- Per-(book, destination) state machine.
    -- pending  → never attempted
    -- syncing  → in-flight (set on attempt-start, cleared on outcome)
    -- synced   → succeeded most recently
    -- failed   → most recent attempt errored; eligible for retry
    -- orphaned → destination was disabled while book was synced
    -- removed_from_destination → reconcile saw book is no longer on the server
    sync_state           TEXT NOT NULL DEFAULT 'pending',

    last_attempted_at    TEXT,
    last_succeeded_at    TEXT,
    last_error           TEXT,
    attempt_count        INTEGER NOT NULL DEFAULT 0,

    -- Set when admin chose to stop retrying despite sync_state='failed'.
    -- NULL = retry budget still open.
    disabled_reason      TEXT,

    -- Per-op outcomes as JSON: {"scan_trigger":{"status":"succeeded","at":"..."}, ...}.
    -- Open-set keys per backend (the Outcome struct's Operation field).
    -- Empty string when no outcomes recorded yet.
    per_op_outcomes      TEXT NOT NULL DEFAULT '',

    PRIMARY KEY (book_id, destination_id),
    FOREIGN KEY (book_id)        REFERENCES books(id)                  ON DELETE CASCADE,
    FOREIGN KEY (destination_id) REFERENCES library_destinations(id)   ON DELETE CASCADE
);

CREATE INDEX book_library_destinations_dest_idx ON book_library_destinations(destination_id, sync_state);
