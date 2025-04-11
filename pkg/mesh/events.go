package mesh

import "fmt"

type NodeId uint32

func (n NodeId) String() string {
	return fmt.Sprintf("!%08x", uint32(n))
}

type MeshEnvelope struct {
	ChannelID  string
	ChannelKey *string
	From       NodeId
	To         NodeId
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

type MeshNodeInfoEvent struct {
	Envelope  MeshEnvelope
	ShortName string
	LongName  string
}

type MeshChannelJoined struct {
	ChannelID  string
	ChannelKey *string
}
