package meshid

import (
	"github.com/kabili207/meshtastic-go/core"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// NewChannelDef builds a channel definition from a name and optional base64 key.
// A nil or empty key yields a channel with the default PSK.
func NewChannelDef(name string, publicKey *string) (*core.Channel, error) {
	key := ""
	if publicKey != nil {
		key = *publicKey
	}
	return core.NewChannel(name, key)
}

func ChannelDefFromPortalID(portalID networkid.PortalID) (*core.Channel, error) {
	name, key, err := ParsePortalID(portalID)
	if err != nil {
		return nil, err
	}
	return NewChannelDef(name, &key)
}
