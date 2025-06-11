package meshid

import (
	"go.mau.fi/util/jsontime"
)

type UserLoginMetadata struct {
	NodeID NodeID `json:"node_id"`
}

type PortalMetadata struct {
	ChannelName string  `json:"channel_name"`
	ChannelKey  *string `json:"channel_key"`
}
type GhostMetadata struct {
	LongName         string         `json:"long_name"`
	ShortName        string         `json:"short_name"`
	UserMXID         string         `json:"user_mxid,omitempty"`
	IsManaged        bool           `json:"is_managed"`
	IsDirectNeighbor bool           `json:"direct_neighbor"`
	IsLicensed       bool           `json:"is_licensed"`
	IsUnmessagable   bool           `json:"is_unmessagable"`
	LastSeen         *jsontime.Unix `json:"last_seen,omitempty"`
	PublicKey        []byte         `json:"public_key,omitempty"`
	PrivateKey       []byte         `json:"private_key,omitempty"`
}
