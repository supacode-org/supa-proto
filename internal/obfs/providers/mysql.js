defineObfsProvider({
  apiVersion: 1,
  id: "mysql",
  version: "1.1.0",
  description: "JSON MySQL gateway and connection telemetry profile",
  cover: {
    direction: "c2s",
    chunkBytesMin: 2048,
    chunkBytesMax: 32768,
    intervalMsMin: 80,
    intervalMsMax: 4000
  },
  limits: {
    maxExtraFieldsPerMessage: 8,
    maxExtraBytesPerMessage: 1024,
    maxDecoyBytes: 1024,
    maxDecoysPerSecond: 2,
    maxDecoysPerMinute: 60,
    maxOverheadRatio: 0.15,
    maxConsecutiveDecoys: 4,
    maxPreAuthDecoys: 1
  },
  discriminator: {
    field: "event",
    logicalValues: {
      client_hello: "handshake_response",
      server_hello: "handshake_ok",
      frame: "packet",
      error: "server_error"
    }
  },
  messages: {
    client_hello: {
      constants: [{ wire: "protocol", value: "mysql" }],
      fields: [
        { logical: "v", wire: "schema_revision", codec: "uint" },
        { logical: "kind", wire: "event", codec: "discriminator" },
        { logical: "client_id", wire: "user", codec: "string" },
        { logical: "ts", wire: "client_time", codec: "int64" },
        { logical: "cn", wire: "scramble", codec: "base64url" },
        { logical: "ce", wire: "client_key", codec: "base64url" },
        { logical: "otp", wire: "auth_code", codec: "decimal" },
        { logical: "mac", wire: "auth_response", codec: "base64url" }
      ],
      noiseSlots: [{ after: "event", candidates: ["charset", "capabilities", "schema-name"] }]
    },
    server_hello: {
      constants: [{ wire: "protocol", value: "mysql" }],
      fields: [
        { logical: "v", wire: "schema_revision", codec: "uint" },
        { logical: "kind", wire: "event", codec: "discriminator" },
        { logical: "sid", wire: "connection_id", codec: "base64url" },
        { logical: "sn", wire: "server_nonce", codec: "base64url" },
        { logical: "se", wire: "server_key", codec: "base64url" },
        { logical: "exp", wire: "wait_timeout_at", codec: "int64" },
        { logical: "mac", wire: "auth_response", codec: "base64url" }
      ],
      noiseSlots: [{ after: "event", candidates: ["server-version", "capabilities", "status-flags"] }]
    },
    frame: {
      constants: [{ wire: "protocol", value: "mysql" }],
      fields: [
        { logical: "v", wire: "schema_revision", codec: "uint" },
        { logical: "kind", wire: "event", codec: "discriminator" },
        { logical: "sid", wire: "connection_id", codec: "base64url" },
        { logical: "dir", wire: "packet_direction", codec: "enum" },
        { logical: "seq", wire: "packet_id", codec: "uint64" },
        { logical: "data", wire: "packet", codec: "base64url" }
      ],
      noiseSlots: [{ after: "packet_direction", candidates: ["status-flags", "warnings", "autocommit", "rows-affected"] }]
    },
    error: {
      constants: [{ wire: "protocol", value: "mysql" }],
      fields: [
        { logical: "v", wire: "schema_revision", codec: "uint" },
        { logical: "kind", wire: "event", codec: "discriminator" },
        { logical: "code", wire: "error_code", codec: "string" },
        { logical: "message", wire: "error_message", codec: "string" }
      ],
      noiseSlots: [{ after: "event", candidates: ["status-flags"] }]
    }
  },
  noiseFields: {
    "server-version": {
      wire: "server_version",
      phases: ["server_hello"],
      directions: ["s2c"],
      baseProbability: 0.8,
      generator: { type: "choice", values: ["8.0.36", "8.0.37", "8.4.0"] }
    },
    capabilities: {
      wire: "capabilities",
      phases: ["client_hello", "server_hello"],
      directions: ["c2s", "s2c"],
      baseProbability: 0.65,
      generator: { type: "choice", values: ["0x81fff7ff", "0xc1fff7ff", "0x01bfa285"] }
    },
    charset: {
      wire: "charset",
      phases: ["client_hello"],
      directions: ["c2s"],
      baseProbability: 0.5,
      generator: { type: "choice", values: ["utf8mb4", "utf8", "latin1"] }
    },
    "status-flags": {
      wire: "status_flags",
      phases: ["server_hello", "frame", "error"],
      directions: ["c2s", "s2c"],
      baseProbability: 0.55,
      generator: { type: "choice", values: [2, 3, 16386] }
    },
    warnings: {
      wire: "warnings",
      phases: ["frame"],
      directions: ["s2c"],
      baseProbability: 0.25,
      generator: { type: "uint", min: 0, max: 3 }
    },
    autocommit: {
      wire: "autocommit",
      phases: ["frame"],
      directions: ["c2s", "s2c"],
      baseProbability: 0.35,
      generator: { type: "bool", trueProbability: 0.9 }
    },
    "schema-name": {
      wire: "schema",
      phases: ["client_hello"],
      directions: ["c2s"],
      baseProbability: 0.2,
      generator: { type: "choice", values: ["app", "inventory", "telemetry"] }
    },
    "rows-affected": {
      wire: "rows_affected",
      phases: ["frame"],
      directions: ["s2c"],
      baseProbability: 0.15,
      generator: { type: "uint", min: 0, max: 64 }
    }
  },
  decoys: [
    {
      id: "server-greeting",
      wireEvent: "server_greeting",
      directions: ["s2c"],
      phases: ["before_hello"],
      trigger: { type: "before_hello", minMs: 0, maxMs: 0 },
      baseProbability: 1,
      responseOnly: false,
      fields: [
        { wire: "protocol", value: "mysql" },
        { wire: "event", value: "server_greeting" },
        { wire: "server_version", generator: { type: "choice", values: ["8.0.36", "8.0.37", "8.4.0"] } },
        { wire: "thread_id", generator: { type: "uint", min: 1000, max: 900000 } }
      ]
    },
    {
      id: "com-ping",
      wireEvent: "com_ping",
      directions: ["c2s"],
      phases: ["established", "idle"],
      trigger: { type: "idle_interval", minMs: 15000, maxMs: 60000 },
      baseProbability: 0.9,
      response: { decoy: "ok-packet", probability: 0.9, minDelayMs: 20, maxDelayMs: 180 },
      fields: [
        { wire: "protocol", value: "mysql" },
        { wire: "event", value: "com_ping" },
        { wire: "request_id", generator: { type: "hex", bytes: 8 } }
      ]
    },
    {
      id: "ok-packet",
      wireEvent: "ok_packet",
      directions: ["s2c"],
      phases: ["established", "idle"],
      trigger: { type: "response", minMs: 0, maxMs: 0 },
      baseProbability: 1,
      responseOnly: true,
      fields: [
        { wire: "protocol", value: "mysql" },
        { wire: "event", value: "ok_packet" },
        { wire: "request_id", copyFromRequest: "request_id" },
        { wire: "status_flags", value: 2 },
        { wire: "warnings", value: 0 }
      ]
    }
  ]
});
