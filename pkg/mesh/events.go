package mesh

import "github.com/kabili207/matrix-meshtastic/pkg/meshid"

type MeshEvent struct {
	ChannelName string
	ChannelKey  *string
	From        meshid.NodeID
	To          meshid.NodeID
	Via         meshid.NodeID
	Timestamp   uint32
	PacketId    uint32
	WantAck     bool
	IsNeighbor  bool
}

type MeshMessageEvent struct {
	MeshEvent
	Message string
	IsDM    bool
}

type MeshReactionEvent struct {
	MeshEvent
	Emoji   string
	IsDM    bool
	ReplyId uint32
}

type MeshLocationEvent struct {
	MeshEvent
	Location    meshid.GeoURI
	GroundSpeed *uint32
	GroundTrack *uint32
}

type MeshWaypointEvent struct {
	MeshEvent
	Name        string
	Description string
	Latitude    float32
	Longitude   float32
	Icon        string
}

type MeshNodeInfoEvent struct {
	MeshEvent
	ShortName      string
	LongName       string
	IsLicensed     bool
	PublicKey      []byte
	IsUnmessagable *bool
}

type MeshChannelJoined struct {
	ChannelID  string
	ChannelKey *string
}

type MeshMapReportEvent struct {
	MeshEvent
	Location         meshid.GeoURI
	ShortName        string
	LongName         string
	FirmwareVersion  string
	OnlineLocalNodes uint32
}
