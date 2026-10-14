-- Preserve generated artwork even when server-side rendering or review is unavailable.
ALTER TABLE group_quality_check_latest
    ADD COLUMN IF NOT EXISTS artwork_html TEXT;
ALTER TABLE group_quality_check_latest
    ADD CONSTRAINT group_quality_artwork_size CHECK (artwork_html IS NULL OR octet_length(artwork_html) <= 1048576);
