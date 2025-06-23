package mesh

import (
	"errors"
	"time"

	"github.com/iancoleman/strcase"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"go.mau.fi/util/ptr"
)

func (c *MeshtasticClient) SendMessage(from, to meshid.NodeID, channel meshid.ChannelDef, message string, usePKI bool) (uint32, error) {
	data := []byte(message)
	encType := PSKEncryption
	if usePKI {
		encType = PKIEncryption
	}
	return c.sendBytes(channel, data, PacketInfo{
		PortNum:   pb.PortNum_TEXT_MESSAGE_APP,
		Encrypted: encType,
		From:      from,
		To:        to,
	})
}

func (c *MeshtasticClient) SendReaction(from, to meshid.NodeID, channel meshid.ChannelDef, targetPacketId uint32, emoji string, usePKI bool) (packetID uint32, err error) {
	data := []byte(emoji)
	encType := PSKEncryption
	if usePKI {
		encType = PKIEncryption
	}
	return c.sendBytes(channel, data, PacketInfo{
		PortNum:   pb.PortNum_TEXT_MESSAGE_APP,
		Encrypted: encType,
		From:      from,
		To:        to,
		Emoji:     true,
		ReplyId:   targetPacketId,
	})
}

// TODO: Create a user info struct to hold from, long, and short names
func (c *MeshtasticClient) SendNodeInfo(from, to meshid.NodeID, longName, shortName string, wantResponse bool, publicKey []byte) error {

	now := time.Now()
	if v, ok := c.nodeInfoSendCache[from]; ok && v.After(now.Add(5*time.Minute)) {
		return errors.New("node info was sent too recently")
	}

	if len([]byte(longName)) > 39 {
		return errors.New("long name must be less than 40 bytes")
	} else if len([]byte(shortName)) > 4 {
		return errors.New("short name must be less than 5 bytes")
	}

	nodeInfo := pb.User{
		Id:         from.String(),
		LongName:   longName,
		ShortName:  shortName,
		IsLicensed: false,
		HwModel:    pb.HardwareModel_PRIVATE_HW,
		Role:       pb.Config_DeviceConfig_CLIENT_MUTE,
		Macaddr:    from.ToMacAddress(),
		PublicKey:  publicKey,
	}

	if from == c.nodeId {
		nodeInfo.IsUnmessagable = ptr.Ptr(true)
		// Don't use REPEATER role. Repeaters are not supposed to send their own node info, telemetry, etc.
		// While they don't crash, client apps can get a tad confused if we break this assumption
		nodeInfo.Role = pb.Config_DeviceConfig_ROUTER
		// As funny as this is, it crashes the Android app prior to version 2.5.23
		// https://github.com/meshtastic/Meshtastic-Android/issues/1787
		nodeInfo.HwModel = pb.HardwareModel_RESERVED_FRIED_CHICKEN
	}

	_, err := c.sendProtoMessage(c.primaryChannel, &nodeInfo, PacketInfo{
		To:           to,
		From:         from,
		PortNum:      pb.PortNum_NODEINFO_APP,
		Encrypted:    PSKEncryption,
		WantResponse: wantResponse,
	})
	if err == nil {
		c.nodeInfoSendCache[from] = now
	}
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
	voltage := float32(5.0)

	nodeInfo := pb.Telemetry{
		Time: uint32(now.Unix()),
		Variant: &pb.Telemetry_DeviceMetrics{
			DeviceMetrics: &pb.DeviceMetrics{
				BatteryLevel:  &battLevel,
				Voltage:       &voltage,
				UptimeSeconds: &uptimeSec,
			},
		},
	}

	_, err := c.sendProtoMessage(c.primaryChannel, &nodeInfo, PacketInfo{
		PortNum:   pb.PortNum_TELEMETRY_APP,
		Encrypted: PSKEncryption,
		From:      from,
		To:        to,
	})
	return err
}

func (c *MeshtasticClient) SendHostMetrics(from, to meshid.NodeID) error {

	now := time.Now()
	now = now.UTC()

	meminfo, err := mem.VirtualMemory()
	if err != nil {
		return err
	}
	loadAvg, err := load.Avg()
	if err != nil {
		return err
	}

	uptime, err := host.Uptime()
	if err != nil {
		return err
	}

	diskUsage, err := disk.Usage("/")
	if err != nil {
		return err
	}

	nodeInfo := pb.Telemetry{
		Time: uint32(now.Unix()),
		Variant: &pb.Telemetry_HostMetrics{
			HostMetrics: &pb.HostMetrics{
				UptimeSeconds:  uint32(uptime),
				Load1:          uint32(loadAvg.Load1 * 100),
				Load5:          uint32(loadAvg.Load5 * 100),
				Load15:         uint32(loadAvg.Load15 * 100),
				FreememBytes:   meminfo.Available,
				Diskfree1Bytes: diskUsage.Free,
			},
		},
	}

	_, err = c.sendProtoMessage(c.primaryChannel, &nodeInfo, PacketInfo{
		PortNum:   pb.PortNum_TELEMETRY_APP,
		Encrypted: PSKEncryption,
		From:      from,
		To:        to,
	})
	return err
}

// SendNeighborInfo will broadcast the list of neighbor IDs to the mesh.
// The sender's node ID will be remove from the neighbor list if detected
func (c *MeshtasticClient) SendNeighborInfo(from meshid.NodeID, neighborIDs []meshid.NodeID, broadcastInterval uint32) error {

	// Taken from mesh.options in protobuf project
	MAX_NEIGHBOR_LEN := 10

	neighbors := []*pb.Neighbor{}
	for _, id := range neighborIDs {
		if id != from && id > 1 {
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
		NodeBroadcastIntervalSecs: broadcastInterval,
	}

	_, err := c.sendProtoMessage(c.primaryChannel, &nodeInfo, PacketInfo{
		PortNum:   pb.PortNum_NEIGHBORINFO_APP,
		Encrypted: PSKEncryption,
		From:      from,
		To:        meshid.BROADCAST_ID_NO_LORA,
	})
	return err
}

// TODO: Create a user info struct to hold from, long, and short names
func (c *MeshtasticClient) SendPosition(from, to meshid.NodeID, location meshid.GeoURI, timestamp *time.Time) (packetID uint32, err error) {

	now := time.Now()
	now = now.UTC()

	latI := int32(location.Latitude * 1e7)
	lonI := int32(location.Longitude * 1e7)

	nodeInfo := pb.Position{
		Time:          uint32(now.Unix()),
		Timestamp:     uint32(timestamp.Unix()),
		LatitudeI:     &latI,
		LongitudeI:    &lonI,
		PrecisionBits: c.GetPrecisionBits(location.Uncertainty),
	}

	return c.sendProtoMessage(c.primaryChannel, &nodeInfo, PacketInfo{
		PortNum:   pb.PortNum_POSITION_APP,
		Encrypted: PSKEncryption,
		From:      from,
		To:        to,
	})
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

func GetPositionPrecisionInMeters(positionPrecision uint32) uint32 {
	precisionMap := map[uint32]uint32{
		2:  5976446,
		3:  2988223,
		4:  1494111,
		5:  747055,
		6:  373527,
		7:  186763,
		8:  93381,
		9:  46690,
		10: 23345,
		11: 11672, // Android LOW_PRECISION
		12: 5836,
		13: 2918,
		14: 1459,
		15: 729,
		16: 364, // Android MED_PRECISION
		17: 182,
		18: 91,
		19: 45,
		20: 22,
		21: 11,
		22: 5,
		23: 2,
		24: 1,
		32: 0, // Android HIGH_PRECISION
	}

	return precisionMap[positionPrecision]
}

func (c *MeshtasticClient) SendMapReport(from meshid.NodeID, longName, shortName string, location meshid.GeoURI, numNodes uint32) (packetID uint32, err error) {

	if location.Latitude == 0 || location.Longitude == 0 {
		return 0, errors.New("a valid location is required")
	}

	role := pb.Config_DeviceConfig_CLIENT
	hw := pb.HardwareModel_PRIVATE_HW
	if from == c.nodeId {
		role = pb.Config_DeviceConfig_REPEATER
	}

	// TODO: Pull from root topic
	region := pb.Config_LoRaConfig_RegionCode_value["US"]
	defChanSnake := strcase.ToScreamingSnake(c.primaryChannel.GetName())
	preset, hasDefaultChan := pb.Config_LoRaConfig_ModemPreset_value[defChanSnake]

	latI := int32(location.Latitude * 1e7)
	lonI := int32(location.Longitude * 1e7)

	nodeInfo := pb.MapReport{
		LongName:            longName,
		ShortName:           shortName,
		Role:                role,
		HwModel:             hw,
		Region:              pb.Config_LoRaConfig_RegionCode(region),
		HasDefaultChannel:   hasDefaultChan,
		LatitudeI:           latI,
		LongitudeI:          lonI,
		PositionPrecision:   c.GetPrecisionBits(location.Uncertainty),
		NumOnlineLocalNodes: numNodes,
	}

	if hasDefaultChan {
		nodeInfo.ModemPreset = pb.Config_LoRaConfig_ModemPreset(preset)
	}

	if location.Altitude != nil {
		nodeInfo.Altitude = int32(*location.Altitude)
	}

	return c.sendProtoMessage(c.primaryChannel, &nodeInfo, PacketInfo{
		PortNum:   pb.PortNum_MAP_REPORT_APP,
		Encrypted: NoEncryption,
		From:      from,
		To:        meshid.BROADCAST_ID,
	})
}

func (c *MeshtasticClient) SendAck(from, to meshid.NodeID, packetId uint32) (uint32, error) {
	c.log.Debug().
		Stringer("from", from).
		Stringer("to", to).
		Uint32("packet_id", packetId).
		Msg("Sending ACK")
	nodeInfo := pb.Routing{
		Variant: &pb.Routing_ErrorReason{
			ErrorReason: pb.Routing_NONE,
		},
	}

	return c.sendProtoMessage(c.primaryChannel, &nodeInfo, PacketInfo{
		PortNum:   pb.PortNum_ROUTING_APP,
		Encrypted: PSKEncryption,
		From:      from,
		To:        to,
		RequestId: packetId,
	})
}

func (c *MeshtasticClient) SendNack(from, to meshid.NodeID, packetId uint32) (uint32, error) {
	c.log.Debug().
		Stringer("from", from).
		Stringer("to", to).
		Uint32("packet_id", packetId).
		Msg("Sending Negative ACK")
	// Not all user interfaces show this as an error (notably MUI), but it's better than nothing
	nodeInfo := pb.Routing{
		Variant: &pb.Routing_ErrorReason{
			ErrorReason: pb.Routing_GOT_NAK,
		},
	}

	return c.sendProtoMessage(c.primaryChannel, &nodeInfo, PacketInfo{
		PortNum:   pb.PortNum_ROUTING_APP,
		Encrypted: PSKEncryption,
		From:      c.nodeId,
		To:        to,
		RequestId: packetId,
	})
}
