package connector

import (
	"testing"

	pb "github.com/kabili207/meshtastic-go/core/proto"
)

func validConfig() Config {
	return Config{
		LongName:       "Matrix Bridge",
		ShortName:      "MB",
		HopLimit:       3,
		PrimaryChannel: ChannelConfig{Name: "LongFast", Key: "AQ=="},
		UDP:            true,
	}
}

func TestValidateConfigPrimaryChannelName(t *testing.T) {
	for _, name := range []string{"", "Default"} {
		cfg := validConfig()
		cfg.PrimaryChannel.Name = name
		if err := (&MeshtasticConnector{Config: cfg}).ValidateConfig(); err == nil {
			t.Errorf("primary channel name %q: expected an error", name)
		}
	}
	if err := (&MeshtasticConnector{Config: validConfig()}).ValidateConfig(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestSignaturePolicy(t *testing.T) {
	cases := map[string]pb.Config_SecurityConfig_PacketSignaturePolicy{
		"":           pb.Config_SecurityConfig_PACKET_SIGNATURE_POLICY_COMPATIBLE,
		"compatible": pb.Config_SecurityConfig_PACKET_SIGNATURE_POLICY_COMPATIBLE,
		"Balanced":   pb.Config_SecurityConfig_PACKET_SIGNATURE_POLICY_BALANCED,
		"STRICT":     pb.Config_SecurityConfig_PACKET_SIGNATURE_POLICY_STRICT,
	}
	for in, want := range cases {
		got, err := Config{SignaturePolicy: in}.signaturePolicy()
		if err != nil || got != want {
			t.Errorf("signaturePolicy(%q) = %v, %v; want %v", in, got, err, want)
		}
	}

	cfg := validConfig()
	cfg.SignaturePolicy = "paranoid"
	if err := (&MeshtasticConnector{Config: cfg}).ValidateConfig(); err == nil {
		t.Error("unknown signature policy: expected an error")
	}
}
