package meshid

import "maunium.net/go/mautrix/id"

type UserLoginMetadata struct {
	NodeID NodeID `json:"node_id"`
}

type PortalMetadata struct {
	ChannelName string  `json:"channel_name,omitempty"`
	ChannelKey  *string `json:"channel_key,omitempty"`
}
type GhostMetadata struct {
	UserMXID id.UserID `json:"user_mxid,omitempty"`
}
