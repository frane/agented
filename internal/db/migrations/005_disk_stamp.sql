-- Reads reconcile with disk, which means a cross-file verb like `ae find`
-- would otherwise stat and hash every open file on every invocation. The
-- in-process drift cache cannot help a CLI: each `ae` run is a new process
-- with a cold cache.
--
-- Record the (mtime, size) of the file at the moment its content last
-- matched head. A later read compares one stat against these two columns and
-- skips the read+hash entirely when they agree. NULL means "unknown, check
-- properly", so existing workspaces simply take the slow path once per file.
ALTER TABLE files ADD COLUMN disk_mtime_ns INTEGER;
ALTER TABLE files ADD COLUMN disk_size INTEGER;

PRAGMA user_version = 5;
