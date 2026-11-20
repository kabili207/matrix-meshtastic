package connector

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
)

func TestNodeColor(t *testing.T) {
	cases := []struct {
		node            meshid.NodeID
		hex, brightness string
	}{
		{0x12345678, "#345678", "dark"},
		{0xa0ffffff, "#ffffff", "light"},
		{0xa0000000, "#000000", "dark"},
		{0xdeadbeef, "#adbeef", "light"},
	}
	for _, c := range cases {
		hex, brightness := nodeColor(c.node)
		if hex != c.hex || brightness != c.brightness {
			t.Errorf("nodeColor(%s) = %s, %s; want %s, %s", c.node, hex, brightness, c.hex, c.brightness)
		}
	}
}

func profileField(t *testing.T, mn *meshdb.MeshNodeInfo, key string) json.RawMessage {
	t.Helper()
	return nodeProfile(0x12345678, mn)[key]
}

func TestNodeProfileStatus(t *testing.T) {
	if got := string(profileField(t, &meshdb.MeshNodeInfo{}, statusField)); got != "null" {
		t.Errorf("no status: %s = %s, want null so the field is deleted", statusField, got)
	}
	mn := &meshdb.MeshNodeInfo{Status: "On a hike"}
	if got := string(profileField(t, mn, statusField)); got != `{"text":"On a hike"}` {
		t.Errorf("%s = %s", statusField, got)
	}
	if got := string(profileField(t, mn, commetStatusField)); got != `"On a hike"` {
		t.Errorf("%s = %s", commetStatusField, got)
	}
}

func TestNodeProfileColorScheme(t *testing.T) {
	if got := string(profileField(t, nil, colorSchemeField)); got != `{"brightness":"dark","color":"#345678"}` {
		t.Errorf("%s = %s", colorSchemeField, got)
	}
}

func TestNodeBio(t *testing.T) {
	mn := &meshdb.MeshNodeInfo{
		ShortName:      "<b>",
		Role:           "ROUTER",
		HwModel:        "HELTEC_V3",
		IsLicensed:     true,
		IsUnmessagable: true,
	}
	plain, formatted := nodeBio(0x12345678, mn)
	for _, want := range []string{"Node ID: !12345678", "Short name: <b>", "Role: ROUTER", "Hardware: HELTEC_V3", "Licensed: yes", "Messages: not accepted"} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain bio missing %q:\n%s", want, plain)
		}
	}
	if !strings.Contains(formatted, "<b>Short name:</b> &lt;b&gt;") {
		t.Errorf("formatted bio does not escape values:\n%s", formatted)
	}

	plain, _ = nodeBio(0x12345678, &meshdb.MeshNodeInfo{})
	if plain != "Node ID: !12345678" {
		t.Errorf("bio for an unknown node = %q, want only the node ID", plain)
	}
}
