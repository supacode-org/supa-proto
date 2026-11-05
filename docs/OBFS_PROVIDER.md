# Obfs Provider Guide

An Obfs Provider defines JSON2Proxy's JSON presentation layer. It maps the
protocol's fixed logical fields to wire-field names and order, and can
optionally add bounded metadata fields or maintenance messages. It does not
change TCP framing, authentication, encryption, routing, or SOCKS5 behavior.

This guide describes the public `obfs-provider/v1` descriptor format. It is
intended for predictable layouts in systems you operate. A Provider is not a
claim of compatibility with another product or protocol.

## Configure a Provider

The client and server must load the exact same Provider file and use the same
participation value:

```json
{
  "obfs": {
    "provider": "file:/etc/json2proxy/obfs/example.js",
    "participation": 0.25
  }
}
```

`provider` accepts an embedded Provider name (`none`, `iot-cam`, or `mysql`) or
an absolute file path prefixed with `file:`. `participation` is a number from
`0` to `1`. It scales optional metadata and maintenance-message frequency, but
does not change required protocol fields.

The Provider ID, version, exact source-file hash, and normalized participation
value are bound to the handshake. A mismatch causes the connection to fail;
there is no automatic fallback or per-connection negotiation.

## Provider files are static

Provider files use a `.js` extension solely as a descriptor container. They are
not executable JavaScript. JSON2Proxy does not embed a JavaScript runtime and
does not call `eval`, `Function`, `require`, imports, or code from the file.

The only accepted top-level form is a single static object literal:

```javascript
defineObfsProvider({ /* descriptor */ });
```

Values may only be objects, arrays, strings, finite numbers, booleans, and
`null`. Functions, expressions, computed keys, imports, assignments, loops,
and duplicate object keys are rejected during startup. A Provider therefore
cannot access files, environment variables, the network, processes, or
cryptographic key material.

## Required structure

Every descriptor has these top-level fields:

| Field | Purpose |
| --- | --- |
| `apiVersion` | Must be `1`. |
| `id` | Lower-case ID matching `[a-z0-9][a-z0-9._-]{0,63}`. |
| `version` | Non-empty version string, up to 32 bytes. |
| `description` | Human-readable text. |
| `limits` | Upper bounds for optional fields and maintenance messages. |
| `discriminator` | Wire field and values identifying each logical message. |
| `messages` | Templates for the four required logical message types. |
| `noiseFields` | Optional metadata rules, or `{}`. |
| `decoys` | Optional maintenance-message rules, or `[]`. |
| `cover` | Optional cover-traffic profile; omit to disable cover for the provider. |

The required message types and logical fields are fixed:

| Message | Required logical fields |
| --- | --- |
| `client_hello` | `v`, `kind`, `client_id`, `ts`, `cn`, `ce`, `otp`, `mac` |
| `server_hello` | `v`, `kind`, `sid`, `sn`, `se`, `exp`, `mac` |
| `frame` | `v`, `kind`, `sid`, `dir`, `seq`, `data` |
| `error` | `v`, `kind`, `code`, `message` |

Each logical field must appear exactly once in its template. A template may
rename its wire field, but cannot omit a required field or change its codec.

## Minimal custom Provider

The following complete Provider only changes a few wire-field names. It adds
no optional fields or maintenance messages, so it is a good starting point for
validation and interoperability testing.

```javascript
defineObfsProvider({
  apiVersion: 1,
  id: "example-layout",
  version: "1.0.0",
  description: "Example static JSON layout",
  limits: {
    maxExtraFieldsPerMessage: 4,
    maxExtraBytesPerMessage: 512,
    maxDecoyBytes: 512,
    maxDecoysPerSecond: 1,
    maxDecoysPerMinute: 12,
    maxOverheadRatio: 0.10,
    maxConsecutiveDecoys: 2,
    maxPreAuthDecoys: 1
  },
  discriminator: {
    field: "type",
    logicalValues: {
      client_hello: "open",
      server_hello: "accept",
      frame: "record",
      error: "error"
    }
  },
  messages: {
    client_hello: {
      constants: [],
      fields: [
        { logical: "v", wire: "version", codec: "uint" },
        { logical: "kind", wire: "type", codec: "discriminator" },
        { logical: "client_id", wire: "client", codec: "string" },
        { logical: "ts", wire: "timestamp", codec: "int64" },
        { logical: "cn", wire: "challenge", codec: "base64url" },
        { logical: "ce", wire: "client_key", codec: "base64url" },
        { logical: "otp", wire: "otp", codec: "decimal" },
        { logical: "mac", wire: "mac", codec: "base64url" }
      ],
      noiseSlots: []
    },
    server_hello: {
      constants: [],
      fields: [
        { logical: "v", wire: "version", codec: "uint" },
        { logical: "kind", wire: "type", codec: "discriminator" },
        { logical: "sid", wire: "session", codec: "base64url" },
        { logical: "sn", wire: "nonce", codec: "base64url" },
        { logical: "se", wire: "server_key", codec: "base64url" },
        { logical: "exp", wire: "expires", codec: "int64" },
        { logical: "mac", wire: "mac", codec: "base64url" }
      ],
      noiseSlots: []
    },
    frame: {
      constants: [],
      fields: [
        { logical: "v", wire: "version", codec: "uint" },
        { logical: "kind", wire: "type", codec: "discriminator" },
        { logical: "sid", wire: "session", codec: "base64url" },
        { logical: "dir", wire: "direction", codec: "enum" },
        { logical: "seq", wire: "sequence", codec: "uint64" },
        { logical: "data", wire: "data", codec: "base64url" }
      ],
      noiseSlots: []
    },
    error: {
      constants: [],
      fields: [
        { logical: "v", wire: "version", codec: "uint" },
        { logical: "kind", wire: "type", codec: "discriminator" },
        { logical: "code", wire: "code", codec: "string" },
        { logical: "message", wire: "message", codec: "string" }
      ],
      noiseSlots: []
    }
  },
  noiseFields: {},
  decoys: []
});
```

Save the descriptor locally, configure its absolute `file:` path on both
endpoints, and restart both processes. Bump `version` whenever the descriptor
changes; the exact source hash also protects against accidental configuration
drift.

## Templates and codecs

`messages` controls wire-field names and output order. Each `fields` entry has
three properties:

```javascript
{ logical: "sid", wire: "session", codec: "base64url" }
```

- `logical` is the fixed protocol field name.
- `wire` is the JSON field name emitted on the connection.
- `codec` must match the logical field type.

Supported codecs are `uint`, `uint64`, `int64`, `string`, `base64url`,
`decimal`, `enum`, and `discriminator`. The `kind` field must use
`discriminator` and map to the descriptor's discriminator field.

`constants` can add fixed wire fields. They cannot duplicate a wire field name
within the same message. The receiver validates their values before passing a
message to the protocol core.

## Optional metadata and maintenance messages

`noiseFields` defines optional metadata. A template's `noiseSlots` identifies
which rules may be inserted after a named wire field. Every rule needs a unique
ID and wire name, one or more allowed message phases and directions, a base
probability, and an approved generator.

```javascript
noiseFields: {
  "build-tag": {
    wire: "build",
    phases: ["client_hello"],
    directions: ["c2s"],
    baseProbability: 0.5,
    generator: { type: "choice", values: ["a", "b", "c"] }
  }
}
```

Allowed generators are `choice`, `bool`, `int`, `uint`, `float`, `hex`,
`base64url`, `uuid`, `unix_sec`, `unix_ms`, and `ascii`. Values are generated
by the host implementation, not by code in the descriptor. The effective
probability is `baseProbability × participation`, capped at `1`.

`decoys` defines schema-validated, non-protocol maintenance messages. They are
optional and should be used sparingly. A rule contains an ID, a distinct wire
event, allowed direction and session phases, a bounded trigger, and fixed or
generated fields. The runtime enforces the limits for message size, rate,
consecutive-message count, pre-handshake count, and overhead ratio.

## Cover traffic

`cover` is an optional profile that lets the provider carry cross-connection
cover traffic: extra sessions that open with `COVER_OPEN` instead of `OPEN`,
carry no real target, and generate (or discard) steady, video-like decoy data.
It is off unless both endpoints enable cover in their configuration; a provider
without a `cover` block does not support cover at all. The profile only sets the
direction and shaping band; the connection count, per-connection rate, lifetime
and rotation are fixed in the client and are not configurable.

| Field | Purpose |
| --- | --- |
| `direction` | `s2c` (the server streams down, like a camera feed) or `c2s` (the client uploads, like a bulk load). Required. |
| `chunkBytesMin` / `chunkBytesMax` | Per-frame payload size range, within `[256, 49152]`. Defaults 4096 / 49152. |
| `intervalMsMin` / `intervalMsMax` | Cadence band in milliseconds, within `[1, 5000]`. Defaults 40 / 400. Keep it wide; the shaper derives the delay from the target rate and only clamps it into this band. |

Adding or changing `cover` changes the descriptor hash, so bump `version` and
deploy the same file to both endpoints, as for any other change.

## Validation checklist

Before deploying a custom Provider:

1. Start from the minimal example or an embedded Provider.
2. Preserve all required logical fields and codecs.
3. Give the Provider a unique ID and bump its version when changing the file.
4. Use identical file contents and participation values on both endpoints.
5. Restart both endpoints and verify a local SOCKS5 request.
6. Test malformed input and reconnect behavior in a controlled environment.
7. Measure throughput and latency with the intended workload before production
   use.

If parsing or validation fails, JSON2Proxy refuses to start with that Provider;
it does not silently fall back to the `none` layout.
