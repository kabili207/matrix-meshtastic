package mesh

import "github.com/kabili207/matrix-meshtastic/pkg/meshid"

type MeshEnvelope struct {
	ChannelID  string
	ChannelKey *string
	From       meshid.NodeID
	To         meshid.NodeID
	Timestamp  uint32
	PacketId   uint32
}

type MeshMessageEvent struct {
	Envelope MeshEnvelope
	Message  string
	IsDM     bool
}

type MeshReactionEvent struct {
	Envelope MeshEnvelope
	Emoji    string
	IsDM     bool
	ReplyId  uint32
}

type MeshLocationEvent struct {
	Envelope    MeshEnvelope
	Latitude    float32
	Longitude   float32
	Altitude    *int32
	GroundSpeed *uint32
	GroundTrack *uint32
}

type MeshWaypointEvent struct {
	Envelope    MeshEnvelope
	Name        string
	Description string
	Latitude    float32
	Longitude   float32
	Icon        string
}

type MeshNodeInfoEvent struct {
	Envelope  MeshEnvelope
	ShortName string
	LongName  string
	PublicKey []byte
}

type MeshChannelJoined struct {
	ChannelID  string
	ChannelKey *string
}
