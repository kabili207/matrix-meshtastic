package mesh

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/meshnet-gophers/meshtastic-go/mqtt"
	"github.com/meshnet-gophers/meshtastic-go/radio"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
)

type BitFieldMask uint32

const (
	BITFIELD_OkToMQTT     BitFieldMask = 1
	BITFIELD_WantResponse BitFieldMask = 2

	// Node ID used for broadcasting
	BROADCAST_ID meshid.NodeID = 0xffffffff

	DefaultChannelName string = "LongFast"
)

type MeshEventFunc func(event any)
type IsManagedFunc func(nodeID meshid.NodeID) bool

type MeshtasticClient struct {
	startTime         *time.Time
	nodeId            meshid.NodeID
	mqttClient        *mqtt.Client
	channelKeys       map[string][]byte
	channelKeyStrings map[string]string
	currentPacketId   uint32
	seenNodes         map[meshid.NodeID]MeshNodeInfo
	eventHandlers     []MeshEventFunc
	log               zerolog.Logger
	managedNodeFunc   IsManagedFunc
}

func nodeIdToMacAddr(nodeId meshid.NodeID) []byte {
	a := make([]byte, 4)
	binary.BigEndian.PutUint32(a, uint32(nodeId))
	// Set first byte to 0xA so it's marked as locally administered
	return []byte{0xA, 0, a[0], a[1], a[2], a[3]}
}

func NewMeshtasticClient(nodeId meshid.NodeID, mqttClient *mqtt.Client, logger zerolog.Logger) *MeshtasticClient {
	now := time.Now()
	now = now.UTC()

	mc := &MeshtasticClient{
		startTime:         &now,
		nodeId:            nodeId,
		mqttClient:        mqttClient,
		channelKeys:       map[string][]byte{},
		channelKeyStrings: map[string]string{},
		currentPacketId:   uint32(now.Unix()),
		seenNodes:         map[meshid.NodeID]MeshNodeInfo{},
		eventHandlers:     []MeshEventFunc{},
		log:               logger,
	}
	mqttClient.SetOnConnectHandler(mc.onMqttConnected)
	mqttClient.SetReconnectingHandler(mc.onMqttReconnecting)
	mqttClient.SetConnectionLostHandler(mc.onMqttConnectionLost)

	return mc
}

func (c *MeshtasticClient) Connect() error {
	err := c.mqttClient.Connect()
	if err != nil {
		return err
	}
	return nil
}

func (c *MeshtasticClient) Disconnect() {
	c.mqttClient.Disconnect()
}

func (c *MeshtasticClient) IsConnected() bool {
	return c.mqttClient.IsConnected()
}

func (c *MeshtasticClient) onMqttConnected() {
	c.log.Debug().
		Str("topic", c.mqttClient.TopicRoot()).
		Msg("Connected to MQTT broker")
}

func (c *MeshtasticClient) onMqttReconnecting() {
	c.log.Info().
		Str("topic", c.mqttClient.TopicRoot()).
		Msg("Reconnecting to MQTT broker")
}

func (c *MeshtasticClient) onMqttConnectionLost(err error) {
	c.log.Err(err).Msg("Lost connection to MQTT broker")
}

func (c *MeshtasticClient) AddChannel(channelName, key string) error {
	keyBytes, err := c.generateKey(key)
	if err != nil {
		return err
	}

	c.notifyEvent(&MeshChannelJoined{
		ChannelID:  channelName,
		ChannelKey: &key,
	})
	if _, ok := c.channelKeys[channelName]; !ok && c.mqttClient != nil && c.mqttClient.IsConnected() {
		c.mqttClient.Handle(channelName, c.channelHandler(channelName))
	}
	c.channelKeys[channelName] = keyBytes
	c.channelKeyStrings[channelName] = key
	return nil
}

func (c *MeshtasticClient) AddEventHandler(handler MeshEventFunc) {
	c.eventHandlers = append(c.eventHandlers, handler)
}

func (c *MeshtasticClient) SetIsManagedNodeHandler(handler IsManagedFunc) {
	c.managedNodeFunc = handler
}

func (c *MeshtasticClient) generateKey(key string) ([]byte, error) {
	// Pad the key with '=' characters to ensure it's a valid base64 string
	padding := (4 - len(key)%4) % 4
	paddedKey := key + strings.Repeat("=", padding)

	// Replace '-' with '+' and '_' with '/'
	replacedKey := strings.ReplaceAll(paddedKey, "-", "+")
	replacedKey = strings.ReplaceAll(replacedKey, "_", "/")

	// Decode the base64-encoded key
	return base64.StdEncoding.DecodeString(replacedKey)
}

func (c *MeshtasticClient) channelHandler(channel string) mqtt.HandlerFunc {
	return func(m mqtt.Message) {
		log := c.log.With().
			Str("channel", channel).
			Bytes("payload", m.Payload).
			Logger()
		var env pb.ServiceEnvelope
		err := proto.Unmarshal(m.Payload, &env)
		if err != nil {
			log.Err(err).Msg("failed unmarshalling to service envelope")
			return
		}

		if c.managedNodeFunc(meshid.NodeID(env.Packet.From)) {
			return
		}

		key := c.channelKeys[channel]

		message, err := radio.TryDecode(env.GetPacket(), key)
		if err != nil {
			log.Err(err).Msg("failed to decrypt the message packet")
		}

		_ = c.processMessage(&env, message)
		//log.Info(res, "topic", m.Topic, "channel", channel, "portnum", message.Portnum.String())
	}
}

type MeshNodeInfo struct {
	Id        uint32
	LongName  string
	ShortName string
	//IsLicensed    bool
	HardwareModel *pb.HardwareModel
	LastSeen      *time.Time
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

// TODO: Create a user info struct to hold from, long, and short names
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

// TODO: Create a user info struct to hold from, long, and short names
func (c *MeshtasticClient) SendPosition(from, to meshid.NodeID, latitude, longitude float32, timestamp *time.Time) (packetID uint32, err error) {

	now := time.Now()
	now = now.UTC()

	latI := int32(latitude * 1e7)
	lonI := int32(longitude * 1e7)
	nodeInfo := pb.Position{
		Time:       uint32(now.Unix()),
		Timestamp:  uint32(timestamp.Unix()),
		LatitudeI:  &latI,
		LongitudeI: &lonI,
	}

	return c.sendProtoMessage(DefaultChannelName, &nodeInfo, PacketInfo{PortNum: pb.PortNum_POSITION_APP, Encrypted: true, From: from, To: to})
}

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

func (c *MeshtasticClient) sendProtoMessage(channel string, message proto.Message, info PacketInfo) (packetID uint32, err error) {
	rawInfo, err := proto.Marshal(message)
	if err != nil {
		return 0, err
	}
	return c.sendBytes(channel, rawInfo, info)
}

type PacketInfo struct {
	PortNum            pb.PortNum
	Encrypted          bool
	From, To           meshid.NodeID
	RequestId, ReplyId uint32
	WantAck            bool
	Emoji              bool
}

func (c *MeshtasticClient) sendBytes(channel string, rawInfo []byte, info PacketInfo) (packetID uint32, err error) {

	if !c.managedNodeFunc(meshid.NodeID(info.From)) {
		return 0, fmt.Errorf("from node is not managed by this bridge: %s", info.From)
	}

	bitfield := uint32(BITFIELD_OkToMQTT)

	emojiVal := 0
	if info.Emoji {
		emojiVal = 1
	}

	// While most devices seem to just ignore payloads that are too large, one of my devices
	// on an older firmware had part of it's memory corrupted and started broadcasting different
	// node info on every boot, adding junk node IDs the device db of nearby nodes
	if len(rawInfo) > int(pb.Constants_DATA_PAYLOAD_LEN)-1 {
		return 0, fmt.Errorf("message is too large for meshtastic network: max(%d) sent(%d)", int(pb.Constants_DATA_PAYLOAD_LEN)-1, len(rawInfo))
	}

	data := pb.Data{
		Portnum:   info.PortNum,
		Payload:   rawInfo,
		Bitfield:  &bitfield,
		RequestId: info.RequestId,
		ReplyId:   info.ReplyId,
		Emoji:     uint32(emojiVal),
	}

	now := time.Now()
	time := uint32(now.Unix())

	c.currentPacketId = c.currentPacketId + 1
	packetId := c.currentPacketId

	rawData, err := proto.Marshal(&data)
	if err != nil {
		return 0, err
	}

	key := c.channelKeys[channel]

	channelHash, _ := radio.ChannelHash(channel, key)

	maxHops := 3
	if info.From == c.nodeId {
		maxHops = 2
	}

	pkt := pb.MeshPacket{
		Id:       packetId,
		To:       uint32(info.To),
		From:     uint32(info.From),
		HopLimit: 2,
		HopStart: uint32(maxHops),
		ViaMqtt:  false,
		WantAck:  info.WantAck,
		RxTime:   time,
		RxSnr:    0,
		RxRssi:   0,
		Channel:  channelHash,
		Priority: pb.MeshPacket_RELIABLE,
		Delayed:  pb.MeshPacket_NO_DELAY,
	}

	if !info.Encrypted {
		pkt.Channel = 0
		pkt.PayloadVariant = &pb.MeshPacket_Decoded{
			Decoded: &data,
		}
	} else {
		encodedBytes, err := radio.XOR(rawData, key, packetId, uint32(info.From))
		if err != nil {
			return packetId, err
		}
		pkt.PayloadVariant = &pb.MeshPacket_Encrypted{
			Encrypted: encodedBytes,
		}
	}

	env := pb.ServiceEnvelope{
		ChannelId: channel,
		GatewayId: c.nodeId.String(),
		Packet:    &pkt,
	}

	rawEnv, err := proto.Marshal(&env)
	if err != nil {
		log.Error(err)
		return packetId, err
	}

	c.printPacketDetails(&env, &data)

	reply := mqtt.Message{
		Topic:   fmt.Sprintf("%s/%s", c.mqttClient.GetFullTopicForChannel(channel), c.nodeId),
		Payload: rawEnv,
	}
	return packetId, c.mqttClient.Publish(&reply)
}

func (c *MeshtasticClient) notifyEvent(event any) {
	for _, handler := range c.eventHandlers {
		handler(event)
	}
}

func (c *MeshtasticClient) processMessage(envelope *pb.ServiceEnvelope, message *pb.Data) error {
	if message == nil {
		return fmt.Errorf("nil message")
	}

	if c.managedNodeFunc(meshid.NodeID(envelope.Packet.From)) {
		return nil
	}

	chanKey := c.channelKeyStrings[envelope.ChannelId]
	meshEventEnv := MeshEnvelope{
		PacketId:   envelope.Packet.Id,
		To:         meshid.NodeID(envelope.Packet.To),
		From:       meshid.NodeID(envelope.Packet.From),
		ChannelID:  envelope.ChannelId,
		ChannelKey: &chanKey,
		Timestamp:  envelope.Packet.RxTime,
	}
	var err error
	var evt any = meshEventEnv

	switch message.Portnum {
	case pb.PortNum_TEXT_MESSAGE_APP:
		if message.Emoji == 1 {
			evt = &MeshReactionEvent{
				Envelope: meshEventEnv,
				Emoji:    string(message.Payload),
				IsDM:     envelope.Packet.To != uint32(BROADCAST_ID),
				ReplyId:  message.ReplyId,
			}
		} else {
			evt = &MeshMessageEvent{
				Envelope: meshEventEnv,
				Message:  string(message.Payload),
				IsDM:     envelope.Packet.To != uint32(BROADCAST_ID),
			}
		}
	case pb.PortNum_NODEINFO_APP:
		var user = pb.User{}
		proto.Unmarshal(message.Payload, &user)
		evt = &MeshNodeInfoEvent{
			Envelope:  meshEventEnv,
			LongName:  user.LongName,
			ShortName: user.ShortName,
		}

	case pb.PortNum_POSITION_APP:
		var pos = pb.Position{}
		proto.Unmarshal(message.Payload, &pos)
		if pos.LatitudeI != nil && pos.LongitudeI != nil {
			lat := float32(*pos.LatitudeI) * 1e-7
			lon := float32(*pos.LongitudeI) * 1e-7
			evt = &MeshLocationEvent{
				Envelope:    meshEventEnv,
				Latitude:    lat,
				Longitude:   lon,
				Altitude:    pos.Altitude,
				GroundSpeed: pos.GroundSpeed,
				GroundTrack: pos.GroundTrack,
			}
		}

	case pb.PortNum_TRACEROUTE_APP:
		var r = pb.RouteDiscovery{}
		err = proto.Unmarshal(message.Payload, &r)
		c.printPacketDetails(envelope, &r)
		c.handleTraceroute(envelope, message, &r)

	case pb.PortNum_TELEMETRY_APP:
		var t = pb.Telemetry{}
		err = proto.Unmarshal(message.Payload, &t)
		c.printPacketDetails(envelope, &t)

	case pb.PortNum_NEIGHBORINFO_APP:
		var n = pb.NeighborInfo{}
		err = proto.Unmarshal(message.Payload, &n)

	case pb.PortNum_STORE_FORWARD_APP:
		var s = pb.StoreAndForward{}
		err = proto.Unmarshal(message.Payload, &s)

	case pb.PortNum_ROUTING_APP:
		var r = pb.Routing{}
		err = proto.Unmarshal(message.Payload, &r)

	}

	c.notifyEvent(evt)
	return err
}

func (c *MeshtasticClient) printPacketDetails(env *pb.ServiceEnvelope, data any) {

	pkt := env.Packet
	log := c.log.With().
		Str("channel", env.ChannelId).
		Stringer("from", meshid.NodeID(pkt.From)).
		Stringer("to", meshid.NodeID(pkt.To)).
		Uint32("packet_id", pkt.Id).
		Interface("payload", data).
		Logger()
	log.Debug().Msg("Packet received")
}
