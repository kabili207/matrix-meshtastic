package mesh

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/kabili207/matrix-meshtastic/pkg/mesh/connectors"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/meshnet-gophers/meshtastic-go/mqtt"
	"github.com/meshnet-gophers/meshtastic-go/radio"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"google.golang.org/protobuf/proto"
)

type BitFieldMask uint32

const (
	BITFIELD_OkToMQTT     BitFieldMask = 1
	BITFIELD_WantResponse BitFieldMask = 2

	// The ASCII bell character, used to ping a channel or DM on Meshtastic
	BellCharacter string = "\x07"

	DefaultHopLimit = 3
)

type MeshEventFunc func(event any)
type IsManagedFunc func(nodeID meshid.NodeID) bool
type MeshConnectedFunc func(isReconnect bool)
type MeshDisconnectedFunc func()
type KeyRequestFunc func(nodeID meshid.NodeID) (key *string)

type MeshtasticClient struct {
	log             zerolog.Logger
	startTime       *time.Time
	nodeId          meshid.NodeID
	channels        []meshid.ChannelDef
	currentPacketId uint32
	primaryChannel  meshid.ChannelDef
	hopLimit        uint32

	previouslyConnected   bool
	eventHandlers         []MeshEventFunc
	managedNodeFunc       IsManagedFunc
	onConnectHandler      MeshConnectedFunc
	onDisconnectHandler   MeshDisconnectedFunc
	pubKeyRequestHandler  KeyRequestFunc
	privKeyRequestHandler KeyRequestFunc

	packetCache       *ttlcache.Cache[uint64, any]
	packetCacheLock   sync.Mutex
	nodeInfoSendCache map[meshid.NodeID]time.Time

	meshConnectors []connectors.MeshConnector
}

func NewMeshtasticClient(nodeId meshid.NodeID, mqttClient *mqtt.Client, logger zerolog.Logger) *MeshtasticClient {
	now := time.Now()
	now = now.UTC()

	mc := &MeshtasticClient{
		startTime:         &now,
		nodeId:            nodeId,
		channels:          []meshid.ChannelDef{},
		nodeInfoSendCache: map[meshid.NodeID]time.Time{},
		eventHandlers:     []MeshEventFunc{},
		log:               logger,
		hopLimit:          DefaultHopLimit,
		meshConnectors: []connectors.MeshConnector{
			// TODO: Let implementors decide which connector to enable
			connectors.NewUDPMessageHandler(logger),
			connectors.NewMQTTMessageHandler(nodeId, mqttClient, logger),
		},
	}

	mc.packetCache = ttlcache.New(
		ttlcache.WithTTL[uint64, any](2 * time.Hour),
	)

	for _, h := range mc.meshConnectors {
		h.SetPacketHandler(mc.handleMeshPacket)
		h.SetStateHandler(mc.handleConnectorStateChange)
	}

	return mc
}

func (c *MeshtasticClient) Connect() error {
	if c.primaryChannel == nil {
		return errors.New("primary channel not set")
	}

	errs := []error{}

	for _, h := range c.meshConnectors {
		if err := h.Start(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) == len(c.meshConnectors) {
		return errors.Join(errs...)
	}

	if len(errs) > 0 {
		c.log.Warn().AnErr("errors", errors.Join(errs...)).Msg("Error starting one or more connectors")
	}

	return nil
}

func (c *MeshtasticClient) Disconnect() {
	for _, h := range c.meshConnectors {
		h.Stop()
	}
}

func (c *MeshtasticClient) IsConnected() bool {
	for _, h := range c.meshConnectors {
		// We only care if at least one handler is connected
		if h.IsConnected() {
			return true
		}
	}
	return false
}

func (c *MeshtasticClient) handleConnectorStateChange(mh connectors.MeshConnector, le connectors.ListenerEvent) {

	if !c.previouslyConnected {
		c.previouslyConnected = true
		c.log.Info().Msg("Connection to mesh network established")
		if c.onConnectHandler != nil {
			c.onConnectHandler(false)
		}
	}

	// Count how many other handlers are still connected
	connectedCount := 0
	for _, h := range c.meshConnectors {
		if h != mh && h.IsConnected() {
			connectedCount++
		}
	}

	if connectedCount > 0 {
		c.log.Debug().Msg("Already connected to mesh")
		return
	}

	switch le {
	case connectors.EventConnectionLost:
		c.log.Warn().Msg("Connection to mesh network lost")
		if c.onDisconnectHandler != nil {
			c.onDisconnectHandler()
		}
	case connectors.EventRestarted:
		c.log.Info().Msg("Connection to mesh network re-established")
		if c.onConnectHandler != nil {
			c.onConnectHandler(true)
		}
	case connectors.EventStarted:
		c.log.Info().Msg("Connection to mesh network established")
		if c.onConnectHandler != nil {
			c.onConnectHandler(false)
		}
	}
}
func (c *MeshtasticClient) AddChannel(channelName, key string) error {
	channel, err := meshid.NewChannelDef(channelName, &key)
	if err != nil {
		return err
	}
	return c.AddChannelDef(channel)
}
func (c *MeshtasticClient) AddChannelDef(channelDef meshid.ChannelDef) error {

	chanKey := channelDef.GetPublicKey()
	c.notifyEvent(&MeshChannelJoined{
		ChannelID:  channelDef.GetName(),
		ChannelKey: &chanKey,
	})
	found := false
	for _, v := range c.channels {
		if v.GetName() == channelDef.GetName() {
			if v == channelDef {
				return nil
			}
			found = true
			break
		}
	}
	if !found {
		for _, h := range c.meshConnectors {
			h.AddChannel(channelDef.GetName())
		}
	}
	c.channels = append(c.channels, channelDef)
	return nil
}

func (c *MeshtasticClient) AddEventHandler(handler MeshEventFunc) {
	c.eventHandlers = append(c.eventHandlers, handler)
}

func (c *MeshtasticClient) SetHopLimit(hopLimit uint32) error {
	if hopLimit >= meshid.MAX_HOPS {
		return fmt.Errorf("hop_limit must be less than %d", meshid.MAX_HOPS)
	}
	c.hopLimit = hopLimit
	return nil
}

func (c *MeshtasticClient) GetPrimaryChannel() meshid.ChannelDef {
	return c.primaryChannel
}

func (c *MeshtasticClient) SetPrimaryChannel(channelName, channelKey string) {
	def, _ := meshid.NewChannelDef(channelName, &channelKey)
	c.AddChannelDef(def)
	c.primaryChannel = def
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

func (c *MeshtasticClient) SetPublicKeyRequestHandler(handler KeyRequestFunc) {
	c.pubKeyRequestHandler = handler
}

func (c *MeshtasticClient) SetPrivateKeyRequestHandler(handler KeyRequestFunc) {
	c.privKeyRequestHandler = handler
}

func (c *MeshtasticClient) sendProtoMessage(channel meshid.ChannelDef, message proto.Message, info PacketInfo) (packetID uint32, err error) {
	rawInfo, err := proto.Marshal(message)
	if err != nil {
		return 0, err
	}
	id, err := c.sendBytes(channel, rawInfo, info)
	if err == nil {
		c.printOutgoingPacketDetails(channel, info.From, info.To, id, message)
	}
	return id, err
}

type EncryptionType int

const (
	NoEncryption EncryptionType = iota
	PSKEncryption
	PKIEncryption
)

type PacketInfo struct {
	PortNum            pb.PortNum
	Encrypted          EncryptionType
	From, To           meshid.NodeID
	RequestId, ReplyId uint32
	WantResponse       bool
	Emoji              bool
}

func (c *MeshtasticClient) generatePacketId() uint32 {
	// Based on the official packet generation method
	// https://github.com/meshtastic/firmware/blob/03f19bca0e9e456342dfb0397a805404677e5abc/src/mesh/Router.cpp#L98

	rollingPacketId := c.currentPacketId

	if rollingPacketId == 0 {
		rollingPacketId = rand.Uint32()
	}

	rollingPacketId++
	c.currentPacketId = (rollingPacketId & (math.MaxUint32 >> 22)) | (rand.Uint32() << 10)
	return c.currentPacketId
}

func (c *MeshtasticClient) GenerateKeyPair() (publicKey, privateKey []byte, err error) {
	return radio.GenerateKeyPair()
}

func getLastByteOfNodeNum(num uint32) uint8 {
	lastByte := uint8(num & 0xFF)
	if lastByte != 0 {
		return lastByte
	}
	return 0xFF
}

func getPriority(data *pb.Data, wantAck bool) pb.MeshPacket_Priority {
	priority := pb.MeshPacket_DEFAULT
	if wantAck {
		// if a reliable message give a bit higher default priority
		priority = pb.MeshPacket_RELIABLE
	}
	if data.Portnum == pb.PortNum_ROUTING_APP {
		// if acks/naks give very high priority
		priority = pb.MeshPacket_ACK
	} else if data.Portnum == pb.PortNum_TEXT_MESSAGE_APP || data.Portnum == pb.PortNum_ADMIN_APP {
		// if text or admin, give high priority
		priority = pb.MeshPacket_HIGH
	} else if data.RequestId != 0 {
		// if it is a response, give higher priority to let it arrive early and stop the request being relayed
		priority = pb.MeshPacket_RESPONSE
	} else if data.WantResponse {
		// Also if we want a response, give a bit higher priority
		priority = pb.MeshPacket_RELIABLE
	}
	return priority
}

func (c *MeshtasticClient) sendBytes(channel meshid.ChannelDef, rawInfo []byte, info PacketInfo) (packetID uint32, err error) {

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

	if info.WantResponse && info.To != meshid.BROADCAST_ID && (info.PortNum == pb.PortNum_NODEINFO_APP || info.PortNum == pb.PortNum_POSITION_APP) {
		data.WantResponse = true
		data.Bitfield = ptr.Ptr(*data.Bitfield | uint32(BITFIELD_WantResponse))
	}

	// TODO: Figure out the appropriate situations where we *do* want an ACK
	wantAck := false

	now := time.Now()
	time := uint32(now.Unix())

	packetId := c.generatePacketId()

	rawData, err := proto.Marshal(&data)
	if err != nil {
		return 0, err
	}

	key := channel.GetKeyBytes()

	channelHash, _ := radio.ChannelHash(channel.GetName(), key)

	maxHops := c.hopLimit
	if info.From != c.nodeId {
		maxHops++
	}

	pkt := pb.MeshPacket{
		Id:        packetId,
		To:        uint32(info.To),
		From:      uint32(info.From),
		HopLimit:  uint32(c.hopLimit),
		HopStart:  uint32(maxHops),
		ViaMqtt:   false,
		WantAck:   wantAck,
		RxTime:    time,
		RxSnr:     0,
		RxRssi:    0,
		Channel:   channelHash,
		Priority:  getPriority(&data, wantAck),
		Delayed:   pb.MeshPacket_NO_DELAY,
		RelayNode: uint32(getLastByteOfNodeNum(uint32(c.nodeId))),
	}

	switch info.Encrypted {
	case NoEncryption:
		pkt.Channel = 0
		pkt.PayloadVariant = &pb.MeshPacket_Decoded{
			Decoded: &data,
		}
	case PSKEncryption:
		encodedBytes, err := radio.XOR(rawData, key, packetId, uint32(info.From))
		if err != nil {
			return packetId, err
		}
		pkt.PayloadVariant = &pb.MeshPacket_Encrypted{
			Encrypted: encodedBytes,
		}
	case PKIEncryption:
		priv, err := c.requestKey(info.From, c.privKeyRequestHandler)
		if err != nil {
			return packetId, err
		}
		pub, err := c.requestKey(info.To, c.pubKeyRequestHandler)
		if err != nil {
			return packetId, err
		}
		encodedBytes, err := radio.EncryptCurve25519(rawData, priv, pub, packetId, uint32(info.From))
		if err != nil {
			return packetId, err
		}
		pkt.PkiEncrypted = true
		pkt.Channel = 0
		pkt.PayloadVariant = &pb.MeshPacket_Encrypted{
			Encrypted: encodedBytes,
		}
	default:
		return 0, errors.New("unknown encryption method requested")
	}

	conLen := len(c.meshConnectors)
	errs := make([]error, conLen)
	var wg sync.WaitGroup
	wg.Add(conLen)

	for i, h := range c.meshConnectors {
		go func(h connectors.MeshConnector) {
			defer wg.Done()
			errs[i] = h.SendPacket(channel.GetName(), &pkt)
		}(h)
	}

	wg.Wait()

	errs = slices.DeleteFunc(
		errs,
		func(thing error) bool {
			return thing == nil
		},
	)

	errs = slices.Clip(errs)

	if len(errs) == len(c.meshConnectors) {
		return packetId, errors.Join(errs...)
	}

	if len(errs) > 0 {
		c.log.Warn().AnErr("errors", errors.Join(errs...)).Msg("Error sending to one or more connectors")
	}

	return packetId, nil

}

func (c *MeshtasticClient) notifyEvent(event any) {
	for _, handler := range c.eventHandlers {
		handler(event)
	}
}

func (c *MeshtasticClient) printPacketDetails(pkt connectors.NetworkMeshPacket, data any) {
	log := c.log.With().
		Str("channel", pkt.ChannelName).
		Stringer("from", meshid.NodeID(pkt.From)).
		Stringer("to", meshid.NodeID(pkt.To)).
		Uint32("packet_id", pkt.Id).
		Interface("payload", data).
		Logger()
	log.Trace().Msg("Packet received")
}

func (c *MeshtasticClient) printOutgoingPacketDetails(channel meshid.ChannelDef, from, to meshid.NodeID, packetID uint32, data any) {
	log := c.log.With().
		Str("channel", channel.GetName()).
		Stringer("from", from).
		Stringer("to", to).
		Uint32("packet_id", packetID).
		Interface("payload", data).
		Logger()
	log.Trace().Msg("Packet broadcast")
}
