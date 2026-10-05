# Google compatibility oracle

The production module has no Go dependency. This development-only module pins Google protobuf v1.36.10 and builds the public GoML example as a separate consumer with a normal path dependency. Set `GOML_VERIFY_DRIVER` (preferred) or `GOML`; otherwise `goml` is resolved on PATH. `GOML_HOME` and `GOFLAGS` are inherited by the child compiler, including `GOFLAGS=-race` during managed race validation.

Run `go test -count=1 ./...` here. The tests use actual `protowire`, `protodesc`, and `dynamicpb`, checking full normalized values, raw unknown bytes, malformed acceptance, and Google's decoding of GoML output. They do not rewrite or inspect generated Go code. Deterministic PRNG seeds are fixed in source. Separate process tests complement the library's shared-schema concurrency regression.

`PROTOBUF_UPDATE_VECTORS=1 go test -count=1 ./...` regenerates `examples/basic/tests/data/*.jsonl`; without that explicit setting native tests compare freshly computed Google expectations with the committed corpus. The corpus is a bounded selection from the larger native matrix: 138 raw-wire, 157 typed-decode, and 24 independent-construction cases. Expected data is computed before invoking GoML and never copied from the implementation under test. Google errors are represented as acceptance booleans rather than unstable error strings. Float NaNs are normalized to `nan` only in the comparison protocol.

See the root README for supported semantics and the two explicitly checked Google differences. The example JSON harness is not a protobuf JSON implementation.
