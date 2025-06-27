package meshid

import (
	"github.com/meshnet-gophers/meshtastic-go/radio"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type ChannelDef interface {
	GetName() string
	GetKeyString() string
	GetKeyBytes() []byte
}

type channelDefImpl struct {
	name     string
	key      *string
	keyBytes []byte
}

func NewChannelDef(name string, publicKey *string) (ChannelDef, error) {
	keyBytes, err := []byte{}, (error)(nil)
	if publicKey != nil && *publicKey != "" {
		keyBytes, err = radio.ParseKey(*publicKey)
		if err != nil {
			return nil, err
		}
		if len(keyBytes) == 1 {
			keyBytes = expandShortPSK(keyBytes)
		}
	}
	return &channelDefImpl{
		name:     name,
		key:      publicKey,
		keyBytes: keyBytes,
	}, nil
}

// ExpandShortPSK converts a short-form PSK into a full-length PSK derived from the defaultPSK.
func expandShortPSK(input []byte) []byte {
	if len(input) != 1 {
		return nil
	}

	pskIndex := input[0]

	if pskIndex == 0 {
		return nil // encryption off
	}

	psk := make([]byte, len(radio.DefaultKey))
	copy(psk, radio.DefaultKey)

	// Bump the last byte of the PSK if needed
	psk[len(psk)-1] += pskIndex - 1

	return psk
}

func (c *channelDefImpl) GetName() string {
	return c.name
}

func (c *channelDefImpl) GetKeyString() string {
	if c.key == nil {
		return ""
	}
	return *c.key
}

func (c *channelDefImpl) GetKeyBytes() []byte {
	return c.keyBytes
}

func ChannelDefFromPortalID(portalID networkid.PortalID) (channel ChannelDef, err error) {
	name, key, err := ParsePortalID(portalID)
	if err != nil {
		return nil, err
	}
	return NewChannelDef(name, &key)
}
