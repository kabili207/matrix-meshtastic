package connector

import (
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/bridgev2/database"
)

type UserLoginMetadata struct {
	NodeID meshid.NodeID `json:"node_id"`
}

type PortalMetadata struct {
	ChannelName string  `json:"channel_name"`
	ChannelKey  *string `json:"channel_key"`
}
type GhostMetadata struct {
	LongName   string `json:"long_name"`
	ShortName  string `json:"short_name"`
	UserMXID   string `json:"user_mxid,omitempty"`
	IsManaged  bool   `json:"is_managed"`
	PublicKey  []byte `json:"public_key,omitempty"`
	PrivateKey []byte `json:"private_key,omitempty"`
}

func (tc *MeshtasticConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal: func() any {
			return &PortalMetadata{}
		},
		Ghost: func() any {
			return &GhostMetadata{}
		},
		Message:  nil,
		Reaction: nil,
		UserLogin: func() any {
			return &UserLoginMetadata{}
		},
	}
}
