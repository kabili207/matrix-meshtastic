package mesh

import (
	"encoding/base64"
	"encoding/binary"
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

	DefaultChannelName string = "LongFast"
)

type MeshEventFunc func(event any)
type IsManagedFunc func(nodeID meshid.NodeID) bool
type MeshConnectedFunc func(isReconnect bool)
type MeshDisconnectedFunc func()

type MeshtasticClient struct {
	startTime           *time.Time
	nodeId              meshid.NodeID
	mqttClient          *mqtt.Client
	channelKeys         map[string][]byte
	channelKeyStrings   map[string]string
	currentPacketId     uint32
	seenNodes           map[meshid.NodeID]MeshNodeInfo
	eventHandlers       []MeshEventFunc
	log                 zerolog.Logger
	managedNodeFunc     IsManagedFunc
	onConnectHandler    MeshConnectedFunc
	onDisconnectHandler MeshDisconnectedFunc
	previouslyConnected bool
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
	if !c.mqttClient.IsConnected() {
		err := c.mqttClient.Connect()
		if err != nil {
			return err
		}
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
	isReconnect := c.previouslyConnected
	c.previouslyConnected = true
	if c.onConnectHandler != nil {
		c.onConnectHandler(isReconnect)
	}
}

func (c *MeshtasticClient) onMqttReconnecting() {
	c.log.Info().
		Str("topic", c.mqttClient.TopicRoot()).
		Msg("Reconnecting to MQTT broker")
}

func (c *MeshtasticClient) onMqttConnectionLost(err error) {
	c.log.Err(err).Msg("Lost connection to MQTT broker")
	if c.onDisconnectHandler != nil {
		c.onDisconnectHandler()
	}
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

func (c *MeshtasticClient) SetOnConnectHandler(handler MeshConnectedFunc) {
	c.onConnectHandler = handler
}

func (c *MeshtasticClient) SetOnDisconnectHandler(handler MeshDisconnectedFunc) {
	c.onDisconnectHandler = handler
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

type MeshNodeInfo struct {
	Id        uint32
	LongName  string
	ShortName string
	//IsLicensed    bool
	HardwareModel *pb.HardwareModel
	LastSeen      *time.Time
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
	priority := pb.MeshPacket_DEFAULT
	if info.WantAck {
		priority = pb.MeshPacket_RELIABLE
	} else if info.PortNum == pb.PortNum_ROUTING_APP {
		priority = pb.MeshPacket_ACK
	} else if info.ReplyId != 0 {
		priority = pb.MeshPacket_RESPONSE
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
		Priority: priority,
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
