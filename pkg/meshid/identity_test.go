package meshid

import (
	"bytes"
	"testing"

	"maunium.net/go/mautrix/id"
)

func TestDeriveUserIdentity(t *testing.T) {
	root := bytes.Repeat([]byte{0x5A}, RootKeySize)
	alice := id.UserID("@alice:example.org")

	nodeID, pub, priv, err := DeriveUserIdentity(root, alice)
	if err != nil {
		t.Fatal(err)
	}
	if again, pub2, priv2, _ := DeriveUserIdentity(root, alice); again != nodeID || !bytes.Equal(pub, pub2) || !bytes.Equal(priv, priv2) {
		t.Fatal("derivation is not deterministic")
	}
	if !nodeID.Core().MatchesPublicKey(pub) {
		t.Errorf("node ID %s is not the CRC of its public key", nodeID)
	}
	if nodeID.Core().IsReservedID() {
		t.Errorf("node ID %s is reserved", nodeID)
	}

	bob, _, _, _ := DeriveUserIdentity(root, id.UserID("@bob:example.org"))
	bridge, bridgePub, _, _ := DeriveBridgeIdentity(root)
	if bob == nodeID || bridge == nodeID || bridge == bob {
		t.Error("distinct labels produced the same node ID")
	}
	if !bridge.Core().MatchesPublicKey(bridgePub) {
		t.Error("bridge ID is not the CRC of its public key")
	}

	otherRoot := bytes.Repeat([]byte{0xA5}, RootKeySize)
	if other, _, _, _ := DeriveUserIdentity(otherRoot, alice); other == nodeID {
		t.Error("different root keys produced the same node ID")
	}
	if _, _, _, err := DeriveUserIdentity(root[:16], alice); err == nil {
		t.Error("short root key accepted")
	}
}

// legacyAlice is CRC32("@alice:example.org"), the pre-2.8 node ID for that user.
const legacyAlice = 0x555ab14a

func TestLegacyMXIDToNodeIDUnchanged(t *testing.T) {
	// Existing installs are migrated off this value, so it must stay stable.
	if got := LegacyMXIDToNodeID(id.UserID("@alice:example.org")); got != NodeID(legacyAlice) {
		t.Fatalf("legacy ID = %s, want !%08x", got, legacyAlice)
	}
}
