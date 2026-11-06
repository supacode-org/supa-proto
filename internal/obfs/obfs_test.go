package obfs

import (
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"supaproto/internal/protocol"
)

func TestBuiltInProvidersLoad(t *testing.T) {
	for _, name := range []string{"none", "iot-cam", "mysql"} {
		t.Run(name, func(t *testing.T) {
			provider, err := Load(Config{Provider: name, Participation: 0.65})
			if err != nil {
				t.Fatal(err)
			}
			if provider.ID() != name || len(provider.Identity()) == 0 {
				t.Fatalf("unexpected provider identity: %q", provider.ID())
			}
		})
	}
}

func TestNoneProviderUsesIdentityWireFormat(t *testing.T) {
	provider, err := Load(Config{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if !provider.IdentityWireFormat() {
		t.Fatal("embedded none provider did not enable identity wire format")
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	sender, err := provider.NewConn(left, "c2s")
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := provider.NewConn(right, "s2c")
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.Envelope{V: protocol.Version, Kind: "error", Code: "invalid_request", Message: "invalid request"}
	errCh := make(chan error, 1)
	go func() { errCh <- sender.WriteEnvelope(want) }()
	got, err := receiver.ReadEnvelope()
	if err != nil {
		t.Fatal(err)
	}
	if err = <-errCh; err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("identity envelope mismatch: got %#v, want %#v", got, want)
	}
}

func TestStaticParserRejectsExecutableJavaScript(t *testing.T) {
	cases := []string{
		`defineObfsProvider((()=>({}))());`,
		`process.exit(1); defineObfsProvider({});`,
		`defineObfsProvider({id:"x"}); fetch("https://example.com");`,
		`defineObfsProvider({__proto__:{}});`,
		`defineObfsProvider({get id(){ return "x"; }});`,
		`defineObfsProvider({id: environment.SECRET});`,
	}
	for _, source := range cases {
		if _, err := parseStaticProvider([]byte(source)); err == nil {
			t.Fatalf("executable provider accepted: %s", source)
		}
	}
}

func TestProviderEnvelopeRoundTrip(t *testing.T) {
	seq := uint64(9)
	envelopes := []protocol.Envelope{
		{
			V: protocol.Version, Kind: "client_hello", ClientID: "camera-7",
			TS: 1785300000, CN: "nonce", CE: "key", OTP: "12345678", MAC: "mac",
		},
		{
			V: protocol.Version, Kind: "server_hello", SID: "sid",
			SN: "nonce", SE: "key", Exp: 1785303600, MAC: "mac",
		},
		{
			V: protocol.Version, Kind: "frame", SID: "sid", Dir: "c2s",
			Seq: &seq, Data: "ciphertext",
		},
		{
			V: protocol.Version, Kind: "error",
			Code: "invalid_request", Message: "invalid request",
		},
	}
	for _, providerName := range []string{"none", "iot-cam", "mysql"} {
		t.Run(providerName, func(t *testing.T) {
			provider, err := Load(Config{Provider: providerName, Participation: 1})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range envelopes {
				direction := "c2s"
				if want.Kind == "server_hello" || want.Kind == "error" {
					direction = "s2c"
				}
				fields, err := provider.encodeEnvelope(want, direction)
				if err != nil {
					t.Fatal(err)
				}
				line, err := encodeWireObject(fields)
				if err != nil {
					t.Fatal(err)
				}
				got, decoy, _, err := provider.decodeLine(bytes.TrimSuffix(line, []byte{'\n'}), direction)
				if err != nil {
					t.Fatalf("%s: %v\n%s", want.Kind, err, line)
				}
				if decoy != nil {
					t.Fatalf("%s decoded as decoy", want.Kind)
				}
				wantJSON, _ := json.Marshal(want)
				gotJSON, _ := json.Marshal(got)
				if !bytes.Equal(gotJSON, wantJSON) {
					t.Fatalf("%s round trip mismatch\nwant %s\ngot  %s", want.Kind, wantJSON, gotJSON)
				}
				if providerName == "iot-cam" &&
					(strings.Contains(string(line), `"kind"`) || strings.Contains(string(line), `"data"`)) {
					t.Fatalf("iot-cam leaked canonical field names: %s", line)
				}
			}
		})
	}
}

func TestProviderMismatchIsRejected(t *testing.T) {
	iot, err := Load(Config{Provider: "iot-cam", Participation: 0.65})
	if err != nil {
		t.Fatal(err)
	}
	mysql, err := Load(Config{Provider: "mysql", Participation: 0.65})
	if err != nil {
		t.Fatal(err)
	}
	env := protocol.Envelope{
		V: protocol.Version, Kind: "error", Code: "invalid_request", Message: "invalid request",
	}
	fields, err := iot.encodeEnvelope(env, "s2c")
	if err != nil {
		t.Fatal(err)
	}
	line, err := encodeWireObject(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = mysql.decodeLine(bytes.TrimSpace(line), "s2c"); err == nil {
		t.Fatal("mismatched provider accepted")
	}
}
