package connectors

import (
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
)

type MeshPacketHandler func(NetworkMeshPacket)
type StateEventHandler func(MeshConnector, ListenerEvent)

type MeshConnector interface {
	Start() error
	Stop()
	IsConnected() bool
	// AddChannel adds the name of the channel this connector should listen to.
	// This is required for connectors that are unable to listen to all channels,
	// such as MQTT. Implementors are expected to maintain this list across connection restarts
	AddChannel(channelName string)
	SendPacket(channel string, packet *pb.MeshPacket) error
	SetPacketHandler(fn MeshPacketHandler)
	SetStateHandler(fn StateEventHandler)
}

type PacketSource string

const (
	// Packet was received from an MQTT broker
	PacketSourceMQTT PacketSource = "mqtt"

	// Packet was received from Mesh over UDP (firmware 2.6+ only)
	PacketSourceUDP PacketSource = "udp"

	// Packet was received from a directly attached radio (serial/BLE/TCP)
	PacketSourceRadio PacketSource = "radio"
)

type NetworkMeshPacket struct {
	*pb.MeshPacket
	ChannelName string
	GatewayNode meshid.NodeID
	Source      PacketSource
}

type ListenerEvent int

const (
	EventStarted ListenerEvent = iota
	EventRestarted
	EventConnectionLost
)
