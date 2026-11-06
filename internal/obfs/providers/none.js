defineObfsProvider({
  apiVersion: 1,
  id: "none",
  version: "1.0.0",
  description: "Identity Supa Proto wire format",
  limits: {
    maxExtraFieldsPerMessage: 1,
    maxExtraBytesPerMessage: 1,
    maxDecoyBytes: 1,
    maxDecoysPerSecond: 1,
    maxDecoysPerMinute: 1,
    maxOverheadRatio: 0.01,
    maxConsecutiveDecoys: 1,
    maxPreAuthDecoys: 1
  },
  discriminator: {
    field: "kind",
    logicalValues: {
      client_hello: "client_hello",
      server_hello: "server_hello",
      frame: "frame",
      error: "error"
    }
  },
  messages: {
    client_hello: {
      constants: [],
      fields: [
        { logical: "v", wire: "v", codec: "uint" },
        { logical: "kind", wire: "kind", codec: "discriminator" },
        { logical: "client_id", wire: "client_id", codec: "string" },
        { logical: "ts", wire: "ts", codec: "int64" },
        { logical: "cn", wire: "cn", codec: "base64url" },
        { logical: "ce", wire: "ce", codec: "base64url" },
        { logical: "otp", wire: "otp", codec: "decimal" },
        { logical: "mac", wire: "mac", codec: "base64url" }
      ],
      noiseSlots: []
    },
    server_hello: {
      constants: [],
      fields: [
        { logical: "v", wire: "v", codec: "uint" },
        { logical: "kind", wire: "kind", codec: "discriminator" },
        { logical: "sid", wire: "sid", codec: "base64url" },
        { logical: "sn", wire: "sn", codec: "base64url" },
        { logical: "se", wire: "se", codec: "base64url" },
        { logical: "exp", wire: "exp", codec: "int64" },
        { logical: "mac", wire: "mac", codec: "base64url" }
      ],
      noiseSlots: []
    },
    frame: {
      constants: [],
      fields: [
        { logical: "v", wire: "v", codec: "uint" },
        { logical: "kind", wire: "kind", codec: "discriminator" },
        { logical: "sid", wire: "sid", codec: "base64url" },
        { logical: "dir", wire: "dir", codec: "enum" },
        { logical: "seq", wire: "seq", codec: "uint64" },
        { logical: "data", wire: "data", codec: "base64url" }
      ],
      noiseSlots: []
    },
    error: {
      constants: [],
      fields: [
        { logical: "v", wire: "v", codec: "uint" },
        { logical: "kind", wire: "kind", codec: "discriminator" },
        { logical: "code", wire: "code", codec: "string" },
        { logical: "message", wire: "message", codec: "string" }
      ],
      noiseSlots: []
    }
  },
  noiseFields: {},
  decoys: []
});
