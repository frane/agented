# Build and tests

```sh
make test            # go test -race -cover, all packages
make test-short      # skip the longer integration / property runs
make test-property   # the property test suite alone
make lint            # go vet (and staticcheck if installed)
make build           # ./ae binary, version + commit + date stamped via ldflags
make release         # cross-compiled binaries to ./dist
make install         # install ae to ~/.local/bin (uses install -m 755 to avoid macOS xattr SIGKILL)
make bench           # write test/benchmark/results.md
make stage-skill     # stage a ClawHub-ready folder under dist/clawhub/agented
make publish-skill   # stage + clawhub skill publish
```

## Property tests

The property tests are where the real correctness work lives. The storage layer does line-splice math under compression with periodic snapshots, and marks recompute their positions across edits without rereading content. Both are the kind of code where bugs hide for years if all you have is happy-path unit tests. The property tests run random edit sequences against an in-memory oracle and catch the drift.

## Benchmarks

`make bench` runs the in-process benchmark suite and writes results to [test/benchmark/results.md](../test/benchmark/results.md). The suite measures ae against itself across editing scenarios; each is a single run so wall-clock varies, but storage growth is exact (SQLite file + WAL + SHM byte deltas).

The README quotes the headline number from there. Producing apples-to-apples comparisons against `Read`/`Edit`/`Write` requires instrumenting those tools' tool-call protocol, which the suite doesn't run. Future work.
