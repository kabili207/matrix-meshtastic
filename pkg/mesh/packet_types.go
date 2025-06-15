package mesh

import "github.com/kabili207/matrix-meshtastic/pkg/meshid"

type MeshtasticPacket struct {
	ChannelName  string
	ChannelKey   *string
	From         meshid.NodeID
	To           meshid.NodeID
	UsePKI       bool
	WantAck      bool
	WantResponse bool
}
