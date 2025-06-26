package meshid

type UserLoginMetadata struct {
	NodeID NodeID `json:"node_id"`
}

type PortalMetadata struct {
	ChannelName string  `json:"channel_name,omitempty"`
	ChannelKey  *string `json:"channel_key,omitempty"`
}
type GhostMetadata struct {
	UserMXID string `json:"user_mxid,omitempty"`
}
