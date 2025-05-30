package connectors

import (
	"fmt"
	"sync"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/meshnet-gophers/meshtastic-go/mqtt"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
)

var _ MeshConnector = (*mqttMessageHandler)(nil)

const (
	pkiChannelName string = "PKI"
)

type mqttMessageHandler struct {
	mqttClient          *mqtt.Client
	nodeID              meshid.NodeID
	log                 zerolog.Logger
	packetHandler       MeshPacketHandler
	stateFunc           StateEventHandler
	previouslyConnected bool

	channelLock     sync.RWMutex
	pendingChannels []string
}

func NewMQTTMessageHandler(nodeID meshid.NodeID, mqttClient *mqtt.Client, logger zerolog.Logger) MeshConnector {
	mc := &mqttMessageHandler{
		mqttClient:      mqttClient,
		log:             logger,
		nodeID:          nodeID,
		pendingChannels: []string{pkiChannelName},
	}

	mqttClient.SetOnConnectHandler(mc.onMqttConnected)
	mqttClient.SetReconnectingHandler(mc.onMqttReconnecting)
	mqttClient.SetConnectionLostHandler(mc.onMqttConnectionLost)

	return mc
}

func (h *mqttMessageHandler) Start() error {
	if !h.mqttClient.IsConnected() {
		err := h.mqttClient.Connect()
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *mqttMessageHandler) Stop() {
	h.mqttClient.Disconnect()
}

func (h *mqttMessageHandler) IsConnected() bool {
	return h.mqttClient.IsConnected()
}

func (h *mqttMessageHandler) SendPacket(channel string, packet *pb.MeshPacket) error {
	env := pb.ServiceEnvelope{
		ChannelId: channel,
		GatewayId: h.nodeID.String(),
		Packet:    packet,
	}

	rawEnv, err := proto.Marshal(&env)
	if err != nil {
		return err
	}
	if packet.PkiEncrypted {
		channel = pkiChannelName
	}

	reply := mqtt.Message{
		Topic:   fmt.Sprintf("%s/%s", h.mqttClient.GetFullTopicForChannel(channel), h.nodeID),
		Payload: rawEnv,
	}

	// Other connectors should take priority over MQTT, due to restrictions enforced by the firmware
	time.Sleep(200 * time.Millisecond)
	return h.mqttClient.Publish(&reply)
}

func (c *mqttMessageHandler) AddChannel(channelName string) {

	if c.mqttClient != nil && c.mqttClient.IsConnected() {
		c.mqttClient.Handle(channelName, c.handleMQTTMessage)
	} else {
		c.channelLock.Lock()
		c.pendingChannels = append(c.pendingChannels, channelName)
		c.channelLock.Unlock()
	}
}

// SetPacketHandler registers the callback for incoming messages
func (h *mqttMessageHandler) SetPacketHandler(fn MeshPacketHandler) {
	h.packetHandler = fn
}

// SetStateHandler implements MeshHandler.
func (h *mqttMessageHandler) SetStateHandler(fn StateEventHandler) {
	h.stateFunc = fn
}

func (c *mqttMessageHandler) handleMQTTMessage(m mqtt.Message) {
	log := c.log.With().Logger()
	var env pb.ServiceEnvelope
	err := proto.Unmarshal(m.Payload, &env)
	if err != nil {
		log.Err(err).Msg("failed unmarshalling to service envelope")
		return
	}
	packet := env.GetPacket()
	gateway, _ := meshid.ParseNodeID(env.GatewayId)

	// Artificially delay incoming MQTT packets in favor of other transports
	time.Sleep(500 * time.Millisecond)
	c.handleMeshPacket(NetworkMeshPacket{
		MeshPacket:  packet,
		GatewayNode: gateway,
		ChannelName: env.ChannelId,
		Source:      PacketSourceMQTT,
	})
}

func (c *mqttMessageHandler) handleMeshPacket(packet NetworkMeshPacket) {
	if c.packetHandler != nil {
		c.packetHandler(packet)
	}
}

func (c *mqttMessageHandler) onMqttConnected() {
	c.log.Debug().
		Str("topic", c.mqttClient.TopicRoot()).
		Msg("Connected to MQTT broker")
	isReconnect := c.previouslyConnected
	c.previouslyConnected = true

	c.channelLock.Lock()
	for _, name := range c.pendingChannels {
		c.mqttClient.Handle(name, c.handleMQTTMessage)
	}
	c.pendingChannels = []string{}
	c.channelLock.Unlock()

	if isReconnect {
		c.emitStateEvent(EventRestarted)
	} else {
		c.emitStateEvent(EventStarted)
	}
}

func (c *mqttMessageHandler) onMqttReconnecting() {
	c.log.Info().
		Str("topic", c.mqttClient.TopicRoot()).
		Msg("Reconnecting to MQTT broker")
}

func (c *mqttMessageHandler) onMqttConnectionLost(err error) {
	c.log.Err(err).Msg("Lost connection to MQTT broker")
	c.emitStateEvent(EventConnectionLost)
}

func (h *mqttMessageHandler) emitStateEvent(eventType ListenerEvent) {
	if h.stateFunc != nil {
		h.stateFunc(h, eventType)
	}
}
