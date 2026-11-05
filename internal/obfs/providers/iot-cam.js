defineObfsProvider({
  apiVersion: 1,
  id: "iot-cam",
  version: "1.1.0",
  description: "JSON camera registration, media and telemetry profile",
  cover: {
    direction: "s2c",
    chunkBytesMin: 8192,
    chunkBytesMax: 49152,
    intervalMsMin: 300,
    intervalMsMax: 5000
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
      client_hello: "camera_register",
      server_hello: "camera_accept",
      frame: "media_chunk",
      error: "camera_error"
    }
  },
  messages: {
    client_hello: {
      constants: [
        { wire: "service", value: "camera-edge" }
      ],
      fields: [
        { logical: "v", wire: "schema", codec: "uint" },
        { logical: "kind", wire: "event", codec: "discriminator" },
        { logical: "client_id", wire: "device_id", codec: "string" },
        { logical: "ts", wire: "captured_at", codec: "int64" },
        { logical: "cn", wire: "challenge", codec: "base64url" },
        { logical: "ce", wire: "device_key", codec: "base64url" },
        { logical: "otp", wire: "rolling_code", codec: "decimal" },
        { logical: "mac", wire: "signature", codec: "base64url" }
      ],
      noiseSlots: [
        { after: "event", candidates: ["camera-model", "firmware"] },
        { after: "device_key", candidates: ["uptime", "signal", "temperature"] }
      ]
    },
    server_hello: {
      constants: [
        { wire: "service", value: "camera-control" }
      ],
      fields: [
        { logical: "v", wire: "schema", codec: "uint" },
        { logical: "kind", wire: "event", codec: "discriminator" },
        { logical: "sid", wire: "stream_id", codec: "base64url" },
        { logical: "sn", wire: "server_nonce", codec: "base64url" },
        { logical: "se", wire: "controller_key", codec: "base64url" },
        { logical: "exp", wire: "lease_until", codec: "int64" },
        { logical: "mac", wire: "signature", codec: "base64url" }
      ],
      noiseSlots: [
        { after: "event", candidates: ["firmware", "codec", "resolution"] }
      ]
    },
    frame: {
      constants: [],
      fields: [
        { logical: "v", wire: "schema", codec: "uint" },
        { logical: "kind", wire: "event", codec: "discriminator" },
        { logical: "sid", wire: "stream_id", codec: "base64url" },
        { logical: "dir", wire: "channel", codec: "enum" },
        { logical: "seq", wire: "chunk_id", codec: "uint64" },
        { logical: "data", wire: "media", codec: "base64url" }
      ],
      noiseSlots: [
        { after: "channel", candidates: ["codec", "fps", "bitrate", "signal", "dropped"] },
        { after: "media", candidates: ["temperature", "storage"] }
      ]
    },
    error: {
      constants: [
        { wire: "service", value: "camera-control" }
      ],
      fields: [
        { logical: "v", wire: "schema", codec: "uint" },
        { logical: "kind", wire: "event", codec: "discriminator" },
        { logical: "code", wire: "error_code", codec: "string" },
        { logical: "message", wire: "detail", codec: "string" }
      ],
      noiseSlots: []
    }
  },
  noiseFields: {
    "camera-model": {
      wire: "camera_model",
      phases: ["client_hello"],
      directions: ["c2s"],
      baseProbability: 0.55,
      generator: { type: "choice", values: ["IPC-220", "IPC-320", "NVR-8CH"] }
    },
    firmware: {
      wire: "firmware",
      phases: ["client_hello", "server_hello"],
      directions: ["c2s", "s2c"],
      baseProbability: 0.45,
      generator: { type: "choice", values: ["3.4.1", "3.5.0", "4.0.2"] }
    },
    codec: {
      wire: "codec",
      phases: ["server_hello", "frame"],
      directions: ["c2s", "s2c"],
      baseProbability: 0.65,
      generator: { type: "choice", values: ["h264", "h265"] }
    },
    resolution: {
      wire: "resolution",
      phases: ["server_hello"],
      directions: ["s2c"],
      baseProbability: 0.45,
      generator: { type: "choice", values: ["1920x1080", "2560x1440", "3840x2160"] }
    },
    fps: {
      wire: "fps",
      phases: ["frame"],
      directions: ["c2s", "s2c"],
      baseProbability: 0.35,
      generator: { type: "uint", min: 15, max: 30 }
    },
    bitrate: {
      wire: "bitrate_kbps",
      phases: ["frame"],
      directions: ["c2s", "s2c"],
      baseProbability: 0.30,
      generator: { type: "uint", min: 512, max: 8192 }
    },
    signal: {
      wire: "signal_dbm",
      phases: ["client_hello", "frame"],
      directions: ["c2s"],
      baseProbability: 0.30,
      generator: { type: "int", min: -90, max: -35 }
    },
    temperature: {
      wire: "temperature_c",
      phases: ["client_hello", "frame"],
      directions: ["c2s", "s2c"],
      baseProbability: 0.25,
      generator: { type: "float", min: 28, max: 72, decimals: 1 }
    },
    uptime: {
      wire: "uptime_sec",
      phases: ["client_hello"],
      directions: ["c2s"],
      baseProbability: 0.35,
      generator: { type: "uint", min: 60, max: 2592000 }
    },
    dropped: {
      wire: "dropped_frames",
      phases: ["frame"],
      directions: ["c2s", "s2c"],
      baseProbability: 0.15,
      generator: { type: "uint", min: 0, max: 12 }
    },
    storage: {
      wire: "storage_percent",
      phases: ["frame"],
      directions: ["c2s"],
      baseProbability: 0.15,
      generator: { type: "uint", min: 5, max: 98 }
    }
  },
  decoys: [
    {
      id: "camera-heartbeat",
      wireEvent: "camera_heartbeat",
      directions: ["c2s"],
      phases: ["established", "idle"],
      trigger: { type: "idle_interval", minMs: 15000, maxMs: 45000 },
      baseProbability: 1,
      response: { decoy: "camera-heartbeat-ack", probability: 0.8, minDelayMs: 30, maxDelayMs: 250 },
      fields: [
        { wire: "event", value: "camera_heartbeat" },
        { wire: "request_id", generator: { type: "hex", bytes: 8 } },
        { wire: "sent_at", generator: { type: "unix_ms", jitterMs: 500 } }
      ]
    },
    {
      id: "camera-heartbeat-ack",
      wireEvent: "camera_heartbeat_ack",
      directions: ["s2c"],
      phases: ["established", "idle"],
      trigger: { type: "response", minMs: 0, maxMs: 0 },
      baseProbability: 1,
      responseOnly: true,
      fields: [
        { wire: "event", value: "camera_heartbeat_ack" },
        { wire: "request_id", copyFromRequest: "request_id" },
        { wire: "status", value: "ok" }
      ]
    },
    {
      id: "camera-status",
      wireEvent: "camera_status",
      directions: ["s2c"],
      phases: ["established", "idle"],
      trigger: { type: "idle_interval", minMs: 30000, maxMs: 90000 },
      baseProbability: 0.5,
      responseOnly: false,
      fields: [
        { wire: "event", value: "camera_status" },
        { wire: "online", value: true },
        { wire: "temperature_c", generator: { type: "float", min: 28, max: 72, decimals: 1 } },
        { wire: "sent_at", generator: { type: "unix_ms", jitterMs: 500 } }
      ]
    }
  ]
});
