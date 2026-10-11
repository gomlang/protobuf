# protobuf

A bounded Protocol Buffers wire codec and explicit schema runtime for the source-built GoML toolchain with unversioned registry support pinned in [workflows/ci/toolchain.json](https://github.com/gomlang/workflows/blob/main/ci/toolchain.json). Production code is pure GoML, with no native or ecosystem dependencies. Google Go protobuf is a development-only compatibility oracle.

```toml
[dependencies]
"ecosystem::protobuf" = true
```

GoML dependencies are unversioned: `true` tracks each package repository’s default branch.

```goml
use ecosystem::protobuf as pb;

fn roundtrip() -> Result[pb::Message, pb::Error] {
    let schema = pb::Schema::new(Vec::from_array([
        pb::MessageSpec { fields: Vec::from_array([
            pb::FieldSpec { number: 1, kind: pb::Kind::String, rule: pb::Rule::Implicit },
            pb::FieldSpec { number: 2, kind: pb::Kind::Sint32, rule: pb::Rule::Packed },
        ]) },
    ]), 0, pb::Limits::standard())?;
    let message = pb::Message::new();
    message.set(1, pb::Value::String("hello"));
    message.add(2, pb::Value::I32(-7));
    message.add(2, pb::Value::I32(150));
    let bytes = schema.encode(message, pb::Limits::standard())?;
    schema.decode(bytes.as_slice(), pb::Options::standard())
}
```

The runtime follows the [protobuf wire encoding](https://protobuf.dev/programming-guides/encoding/) and [field presence rules](https://protobuf.dev/programming-guides/field_presence/). It has no `.proto` parser or code generator, gRPC transport, ProtoJSON, TextFormat, descriptor-set importer, or Serde derive. Applications define their schema and map the typed `Value` variants to their own GoML types.

## Wire API

| API | Contract |
| --- | --- |
| `consume_varint(input, canonical)` | Checked unsigned 64-bit value and consumed byte count; at most ten bytes |
| `encode_varint(value)`, `varint_size(value)` | Minimal unsigned varint |
| `zigzag32/64`, `unzigzag32/64` | Full signed-domain ZigZag conversion |
| `consume_fixed32/64(input)`, `encode_fixed32/64(value)` | Little-endian bits and consumed count |
| `consume_tag(input, canonical)`, `encode_tag(number, wire)` | Checked field number and wire type; tag primitives also permit end-group tags |
| `consume_bytes(input, options)`, `encode_bytes(payload, limits)` | Length prefix and owned payload, without a field tag |
| `parse_fields(input, options)` | Validate a complete message and return owned `RawField` records |
| `skip_field(input, options)` | Validate the first complete field, including any group, and return its consumed length |
| `field(number, WireValue, limits)` | Construct a checked raw field |
| `encode_fields(fields, limits)` | Concatenate validated fields, preserving every original byte |

Wire types 0 (varint), 1 (fixed64), 2 (length-delimited), and 5 (fixed32) are supported. Legacy start/end groups (3/4) are matched by field number and bounded by recursion and field counts; a group is one opaque `RawField`. `WireValue::Group(Vec[RawField])` constructs groups. Typed group schema fields are not supported. End-group tags outside a group and reserved wire types 6/7 fail.

`RawField` exposes `number()`, `wire_type()`, `encoded()`, `payload()`, and optional `varint()`, `fixed32()`, `fixed64()` accessors. Payload excludes the field tag and, for length-delimited fields, the length prefix; group payload excludes both enclosing tags. For fixed and varint fields it contains the encoded scalar bytes. Unknown length-delimited data stays opaque: it is not guessed to be a string or a nested message.

The valid field number range is 1 through 536870911. Raw wire APIs permit the schema-reserved range 19000–19999 so unknown fields can survive. `Schema::new` rejects that range, zero, oversized or duplicate field numbers, invalid nested definition indices, packed nonnumeric kinds, and implicit-presence message fields.

Nonminimal unsigned varints are accepted by default and retained in raw fields. `Options.canonical_varints = true` rejects them, including tag and length varints and packed elements. This option checks minimal encoding of each unsigned wire word; it does not require a canonical typed message or canonical field ordering. Overflow, more than ten bytes, truncation, invalid tags, and mismatched groups always fail. Primitive `consume_*` functions return the first value and leave trailing bytes to the caller; `parse_fields` and `Schema.decode` consume the entire message.

## Explicit schemas and messages

`Schema::new(Vec[MessageSpec], root_index, limits)` compiles and snapshots a table of definitions. `Kind::Message(index)` references that table, allowing recursive schemas. The root is selected once when the schema is built.

| Kind | Value variant |
| --- | --- |
| `Int32`, `Sint32`, `Sfixed32`, `Enum` | `I32(i32)` |
| `Int64`, `Sint64`, `Sfixed64` | `I64(i64)` |
| `Uint32`, `Fixed32` | `U32(u32)` |
| `Uint64`, `Fixed64` | `U64(u64)` |
| `Float`, `Double` | `F32(f32)`, `F64(f64)` |
| `Bool`, `String`, `Bytes` | `Bool(bool)`, `String(string)`, `Bytes(Vec[u8])` |
| `Message(index)` | `Message(Message)` |

`Rule::Optional` retains explicit presence, including zero/empty values; use it for proto2 optional fields and proto3 `optional`. `Required` additionally requires presence. `Implicit` implements ordinary proto3 scalar presence: decoded default values remove the field and encoding omits defaults. Positive floating-point zero is a default; negative zero and NaN are retained. `Repeated` emits individual occurrences, and `Packed` emits one packed numeric segment. Both repeated rules accept packed and unpacked input for all packable kinds, including mixed occurrences and multiple segments.

`Message::new`, `set`, `add`, `get`, `get_all`, `has`, and `clear` provide explicit field access. The container itself has no schema; kind, cardinality, required fields, and unknown typed field numbers are checked during encoding. `get` returns the first stored value; `get_all` returns a shallow copy of the value list. `has` describes the container's stored values: manually setting an implicit default makes `has` true until the message is normalized through encoding/decoding. Missing fields return `None`/an empty list, not synthesized defaults.

On decoding, singular scalar fields use the last occurrence, singular messages merge recursively, and repeated values append in encounter order. Required fields are validated after the entire message has been merged, so separate occurrences may jointly provide required child fields. Repeated child messages are validated separately. A known field with an incompatible wire type is retained as unknown, or discarded when `Options.discard_unknown` is true. Missing required fields still fail when an incompatible-wire occurrence exists.

`unknown_fields`, `add_unknown`, and `clear_unknown` expose unknown-field handling. Unknown fragments retain exact tag, length, nonminimal-varint, and group bytes unless discarded. Encoding emits typed fields in container insertion order, followed by unknown fragments in their stored order. Typed decoding/re-encoding does not preserve whole-message ordering or original spellings, nor promise globally canonical serialization. Adding a raw field whose number overlaps a typed schema field is allowed; its bytes may affect the next decoding according to normal merge/last-occurrence rules.

Integer decoding follows protobuf truncation semantics: `int32`/`uint32` use the low 32 bits of an accepted 64-bit varint, `bool` accepts any nonzero value, and negative `int32`/enum encoding uses the ten-byte sign-extended varint. Enums are open numeric `i32` values; declared enum constants and proto2 closed-enum validation are not modeled. Strings always require valid UTF-8. Byte fields preserve arbitrary bytes. Floating-point wire bits, including signed zero and NaN, are carried by GoML's floating types; applications should compare NaNs by their intended semantics rather than ordinary equality.

Oneof exclusion/clearing, map duplicate-key semantics, extension registries, custom proto2 defaults, and generated accessor types are outside this runtime. A map can be represented explicitly as repeated entry messages, but dictionary semantics remain the application's responsibility. Use individual optional fields only when independent presence is intended; they do not implement a oneof.

## Bounds, errors, and ownership

Every complete-message operation takes explicit `Limits` (through `Options` for decoding). Defaults are:

| Limit | Default | Meaning |
| --- | --- | --- |
| `max_input` | 16 MiB | Entire supplied decode/parse/skip input slice |
| `max_output` | 16 MiB | Complete encoded output |
| `max_blob` | 16 MiB | Each length-delimited payload, including a nested message or packed segment |
| `max_fields` | 1,000,000 | Wire field records across a message and descendants; group closing tags also count |
| `max_values` | 1,000,000 | Logical decoded/encoded values, including message occurrences and unknown records |
| `max_depth` | 64 | Nested messages/groups below root depth zero; configured range 0–256 |
| `max_schema_fields` | 16,384 | Total schema fields, and separately the number of definitions |

All limits must be nonnegative. Zero is meaningful. Message input, output, and length-delimited payloads also have a 2 GiB minus one protocol ceiling, even if configured limits are larger. Fixed/varint primitives need no configurable budget because they inspect at most 4/8/10 bytes; length-prefixed primitives apply byte limits but do not count message fields/values.

Decoded packed segments charge one field even when empty, and charge one value per element. Duplicate/overwritten fields and discarded unknowns still consume the decode budget. Raw groups count all inner records and closing tags against `max_fields`, but occupy one opaque value. `skip_field` allocates no returned field/value and does not charge `max_values`. During encoding, each packed field charges one wire record; supplied values count even if an implicit default is omitted, while omitted defaults consume no wire record. Budgets bound work and materialized values, not total heap usage or an exact instruction count. Message lookup is linear in stored field count; schema lookup is binary search.

Encoding validates and measures the complete tree before allocating the output byte vector, including required fields, type mismatches, nested lengths, unknown fields, and cycles bounded by depth. It does allocate bounded size metadata while validating. Failures return no partial output. Decoding may construct a partial private message before failure, but never returns it. Input size and limit validity are checked first; field/value budgets are checked as encountered. A declared length exceeding `max_blob` fails before testing whether its payload is truncated.

`Error` carries `kind`, `offset`, and `message`. Decode offsets are absolute bytes in the original supplied message, including nested/packed data. UTF-8 errors identify the string payload start. Missing-required, schema, and encode errors use offset zero; they do not provide a field-path diagnostic. Exact error message text is diagnostic, not a stable machine interface.

Raw fields snapshot input bytes, and `encoded()`/`payload()` return independent copies. Schemas snapshot definition arrays. They can be shared by concurrent readers. Separate decodes return independently owned byte fields; encoding returns a fresh byte vector and does not modify inputs. `Message`, nested messages, and `Value::Bytes` use ordinary mutable GoML container sharing: copying them or obtaining a value does not deep-clone the tree. `get_all` only copies the outer list. Serialize mutations of shared messages/byte values and do not mutate an input while encoding or decoding; use independent decoded messages for concurrent mutation. The race suite exercises shared immutable schema/raw fields with independent mutable messages.

## Compatibility evidence and deliberate differences

`tools/oracle` pins `google.golang.org/protobuf v1.36.10`. Actual `protowire` and dynamically constructed Google descriptors/messages generate wire inputs and expected results; GoML encodings are independently decoded again by Google. The native test builds `examples/basic/main.goml` as a separate module with a normal path dependency on this library, then invokes only public APIs. Its JSON command protocol is a test/example harness, not a ProtoJSON API.

The corpus covers all scalar kinds, repeated packed/unpacked combinations, proto2 required/optional and proto3 implicit/explicit presence, integer extremes and truncation, nonminimal encodings, signed zero/NaN, duplicate child merging, unknown groups, wrong-wire fields, malformed tags/lengths/varints, and truncation. Checked-in JSONL expectations are generated solely by Google and run in ordinary downstream GoML tests; native tests verify that they are reproducible. Error acceptance and complete normalized values are compared; Google-specific error strings are not copied.

Two differences are explicitly tested: this library rejects field numbers above the standard 29-bit range even though Google's low-level `protowire.ConsumeField` accepts some larger numbers; and it rejects invalid UTF-8 strings in proto2 as well as proto3, whereas Google's proto2 dynamic runtime accepts them. Opt-in minimal-varint enforcement and explicit resource limits are additional stricter policies. These tests establish the documented subset, not complete conformance to every protobuf edition or generated-language API.

## Validation

```sh
goml fmt --check
goml test
# GOML_VERIFY_DRIVER is preferred; GOML is the fallback, then PATH.
cd tools/oracle
GOML_VERIFY_DRIVER=/path/to/goml go test -count=1 ./...
GOML_VERIFY_DRIVER=/path/to/goml GOFLAGS=-race go test -race -count=1 ./...
# Regenerate independently computed expectations only after reviewing changes:
GOML_VERIFY_DRIVER=/path/to/goml PROTOBUF_UPDATE_VECTORS=1 go test -count=1 ./...
```

The verification runner supplies `GOML_VERIFY_DRIVER` and an isolated `GOML_HOME`; oracle child builds inherit both and `GOFLAGS`, so the race run instruments the GoML consumer too. The Go reference module requires Go 1.24 or newer; CI uses the ecosystem's Go toolchain and the source-built GoML toolchain with unversioned registry support pinned in [workflows/ci/toolchain.json](https://github.com/gomlang/workflows/blob/main/ci/toolchain.json).
