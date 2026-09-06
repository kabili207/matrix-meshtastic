package meshid

import (
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/kabili207/meshtastic-go/core"
	"github.com/kabili207/meshtastic-go/core/crypto"
	"maunium.net/go/mautrix/id"
)

// Node identities are derived from one bridge root secret so a node ID follows its
// key the way firmware 2.8 requires (ID = CRC32 of the X25519 public key), while
// still being reproducible from the root secret alone. The root secret is never
// used as a curve key itself; every identity, the bridge's included, is an HKDF
// output labelled by what it is for.

const identitySalt = "matrix-meshtastic node identity v1"

// RootKeySize is the byte length of the bridge root secret.
const RootKeySize = 32

// maxIdentityAttempts bounds the re-roll loop for IDs in the reserved range.
const maxIdentityAttempts = 64

var ErrRootKeySize = errors.New("bridge root key must be 32 bytes")

// DeriveUserIdentity derives the mesh identity for a Matrix user.
func DeriveUserIdentity(rootKey []byte, mxid id.UserID) (NodeID, []byte, []byte, error) {
	return deriveIdentity(rootKey, "user:"+mxid.String())
}

// DeriveBridgeIdentity derives the bridge's own mesh identity.
func DeriveBridgeIdentity(rootKey []byte) (NodeID, []byte, []byte, error) {
	return deriveIdentity(rootKey, "bridge")
}

// deriveIdentity walks a small counter so a derived ID that lands in the reserved
// range can be replaced by a different key rather than a different ID. Any keypair
// is valid on the mesh as long as the ID is its CRC; only the ID is fixed by the key.
func deriveIdentity(rootKey []byte, label string) (nodeID NodeID, pub, priv []byte, err error) {
	if len(rootKey) != RootKeySize {
		return 0, nil, nil, ErrRootKeySize
	}
	for attempt := 0; attempt < maxIdentityAttempts; attempt++ {
		info := fmt.Sprintf("%s\x00%d", label, attempt)
		seed, err := hkdf.Key(sha256.New, rootKey, []byte(identitySalt), info, crypto.PublicKeySize)
		if err != nil {
			return 0, nil, nil, err
		}
		pub, priv, err := crypto.KeyPairFromSeed(seed)
		if err != nil {
			return 0, nil, nil, err
		}
		derived, err := core.NodeIDFromPublicKey(pub)
		if err != nil {
			return 0, nil, nil, err
		}
		if id := NodeID(derived); !derived.IsReservedID() {
			return id, pub, priv, nil
		}
	}
	return 0, nil, nil, fmt.Errorf("no usable identity for %q after %d attempts", label, maxIdentityAttempts)
}
