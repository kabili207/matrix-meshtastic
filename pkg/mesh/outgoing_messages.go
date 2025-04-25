package mesh

import (
	"errors"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
)

func (c *MeshtasticClient) SendMessage(from, to meshid.NodeID, channel string, message string) (uint32, error) {
	data := []byte(message)
	return c.sendBytes(channel, data, PacketInfo{PortNum: pb.PortNum_TEXT_MESSAGE_APP, Encrypted: true, From: from, To: to})
}

func (c *MeshtasticClient) SendReaction(from, to meshid.NodeID, channel string, targetPacketId uint32, emoji string) (packetID uint32, err error) {
	data := []byte(emoji)
	return c.sendBytes(channel, data, PacketInfo{
		PortNum:   pb.PortNum_TEXT_MESSAGE_APP,
		Encrypted: true,
		From:      from,
		To:        to,
		Emoji:     true,
		ReplyId:   targetPacketId,
	})
}

// TODO: Create a user info struct to hold from, long, and short names
func (c *MeshtasticClient) SendNodeInfo(from, to meshid.NodeID, longName, shortName string, wantAck bool) error {

	if len([]byte(longName)) > 39 {
		return errors.New("long name must be less than 40 bytes")
	} else if len([]byte(shortName)) > 4 {
		return errors.New("short name must be less than 5 bytes")
	}

	role := pb.Config_DeviceConfig_CLIENT
	hw := pb.HardwareModel_PRIVATE_HW
	if from == c.nodeId {
		role = pb.Config_DeviceConfig_REPEATER
		// As funny as this is, it crashes version 2.5.16 of the Android app
		// hw = pb.HardwareModel_RESERVED_FRIED_CHICKEN
	}

	nodeInfo := pb.User{
		Id:         from.String(),
		LongName:   longName,
		ShortName:  shortName,
		IsLicensed: false,
		HwModel:    hw,
		Role:       role,
		Macaddr:    nodeIdToMacAddr(from),
	}

	_, err := c.sendProtoMessage(DefaultChannelName, &nodeInfo, PacketInfo{
		To:        to,
		From:      from,
		PortNum:   pb.PortNum_NODEINFO_APP,
		Encrypted: true,
		WantAck:   wantAck,
	})
	return err
}

// SendTelemetry broadcasts as basic DeviceMetrics telemetry packet to the mesh
func (c *MeshtasticClient) SendTelemetry(from, to meshid.NodeID) error {

	now := time.Now()
	now = now.UTC()

	upTime := now.Sub(*c.startTime)
	uptimeSec := uint32(upTime.Abs().Seconds())

	// Value > 100 means device is mains powered
	battLevel := uint32(101)

	nodeInfo := pb.Telemetry{
		Time: uint32(now.Unix()),
		Variant: &pb.Telemetry_DeviceMetrics{
			DeviceMetrics: &pb.DeviceMetrics{
				BatteryLevel:  &battLevel,
				UptimeSeconds: &uptimeSec,
			},
		},
	}

	_, err := c.sendProtoMessage(DefaultChannelName, &nodeInfo, PacketInfo{PortNum: pb.PortNum_TELEMETRY_APP, Encrypted: true, From: from, To: to})
	return err
}

// SendNeighborInfo will broadcast the list of neighbor IDs to the mesh.
// The sender's node ID will be remove from the neighbor list if detected
func (c *MeshtasticClient) SendNeighborInfo(from meshid.NodeID, neighborIDs []meshid.NodeID) error {

	// Taken from mesh.options in protobuf project
	MAX_NEIGHBOR_LEN := 10

	neighbors := []*pb.Neighbor{}
	for _, id := range neighborIDs {
		if id != from {
			neighbors = append(neighbors, &pb.Neighbor{NodeId: uint32(id), Snr: 0})
		}
	}
	//sort.Slice(applicants, func(i, j int) bool {
	//    return applicants[i].GPA > applicants[j].GPA
	//})
	// TODO: Cap to the most recently seen instead of the first 10 in the list
	if len(neighbors) > MAX_NEIGHBOR_LEN {
		neighbors = neighbors[:MAX_NEIGHBOR_LEN]
	}

	nodeInfo := pb.NeighborInfo{
		NodeId:                    uint32(from),
		Neighbors:                 neighbors,
		LastSentById:              uint32(from),
		NodeBroadcastIntervalSecs: 3600,
	}

	_, err := c.sendProtoMessage(DefaultChannelName, &nodeInfo, PacketInfo{
		PortNum:   pb.PortNum_NEIGHBORINFO_APP,
		Encrypted: true,
		From:      from,
		To:        meshid.BROADCAST_ID_NO_LORA,
	})
	return err
}

// TODO: Create a user info struct to hold from, long, and short names
func (c *MeshtasticClient) SendPosition(from, to meshid.NodeID, latitude, longitude float32, accuracy *float32, timestamp *time.Time) (packetID uint32, err error) {

	now := time.Now()
	now = now.UTC()

	latI := int32(latitude * 1e7)
	lonI := int32(longitude * 1e7)

	nodeInfo := pb.Position{
		Time:          uint32(now.Unix()),
		Timestamp:     uint32(timestamp.Unix()),
		LatitudeI:     &latI,
		LongitudeI:    &lonI,
		PrecisionBits: c.GetPrecisionBits(accuracy),
	}

	return c.sendProtoMessage(DefaultChannelName, &nodeInfo, PacketInfo{PortNum: pb.PortNum_POSITION_APP, Encrypted: true, From: from, To: to})
}

func (c *MeshtasticClient) GetPrecisionBits(meters *float32) uint32 {
	if meters == nil {
		return 0
	}
	var val uint32 = 0

	switch m := *meters; {
	case m >= 23300:
		val = 10
	case m >= 11700:
		val = 11
	case m >= 5800:
		val = 12
	case m >= 2900:
		val = 13
	case m >= 1500:
		val = 14
	case m >= 729:
		val = 15
	case m >= 364:
		val = 16
	case m >= 182:
		val = 17
	case m >= 91:
		val = 18
	case m >= 45:
		val = 19
	default:
		val = 20
	}

	return val
}

func (c *MeshtasticClient) SendAck(from, to meshid.NodeID, packetId uint32) (uint32, error) {

	nodeInfo := pb.Routing{
		Variant: &pb.Routing_ErrorReason{
			ErrorReason: pb.Routing_NONE,
		},
	}

	return c.sendProtoMessage(DefaultChannelName, &nodeInfo, PacketInfo{
		PortNum:   pb.PortNum_ROUTING_APP,
		Encrypted: true,
		From:      from,
		To:        to,
		RequestId: packetId,
	})
}

func (c *MeshtasticClient) SendNack(from, to meshid.NodeID, packetId uint32) (uint32, error) {
	// Not all user interfaces show this as an error (notably MUI), but it's better than nothing
	nodeInfo := pb.Routing{
		Variant: &pb.Routing_ErrorReason{
			ErrorReason: pb.Routing_GOT_NAK,
		},
	}

	return c.sendProtoMessage(DefaultChannelName, &nodeInfo, PacketInfo{
		PortNum:   pb.PortNum_ROUTING_APP,
		Encrypted: true,
		From:      c.nodeId,
		To:        to,
		RequestId: packetId,
	})
}
