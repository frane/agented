-- Adds second_parent_edit_id for merge edits with two parents.
-- Merge edits have both parent_edit_id (the head being merged into) and
-- second_parent_edit_id (the leaf being merged from). Tree walks for
-- branches/reconstruction follow parent_edit_id only; second_parent is
-- metadata for ae log and ae merge ancestry queries.
ALTER TABLE edits ADD COLUMN second_parent_edit_id INTEGER REFERENCES edits(id);
CREATE INDEX idx_edits_second_parent ON edits(second_parent_edit_id) WHERE second_parent_edit_id IS NOT NULL;
PRAGMA user_version = 2;
