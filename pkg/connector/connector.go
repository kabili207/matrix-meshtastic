package connector

import (
	"context"
	_ "embed"
	"fmt"
	"hash/crc32"
	"log/slog"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/meshnet-gophers/meshtastic-go/mqtt"
	"github.com/rs/zerolog"
	slogzerolog "github.com/samber/slog-zerolog/v2"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

type MeshtasticConnector struct {
	log              zerolog.Logger
	bridge           *bridgev2.Bridge
	Config           Config
	baseNodeID       meshid.NodeID
	managedNodeCache map[meshid.NodeID]bool
}

var _ bridgev2.NetworkConnector = (*MeshtasticConnector)(nil)

// NewSimpleNetworkConnector creates a new instance of SimpleNetworkConnector
func NewMeshtasticConnector(log zerolog.Logger) *MeshtasticConnector {
	return &MeshtasticConnector{
		log:              log.With().Str("component", "network-connector").Logger(),
		managedNodeCache: map[meshid.NodeID]bool{},
	}
}

func (c *MeshtasticConnector) Init(bridge *bridgev2.Bridge) {
	c.bridge = bridge
	c.log = c.bridge.Log
	if c.managedNodeCache == nil {
		c.managedNodeCache = map[meshid.NodeID]bool{}
	}

	c.bridge.Commands.(*commands.Processor).AddHandlers(cmdJoinChannel)
	c.bridge.Commands.(*commands.Processor).AddHandlers(cmdUpdateNames)

	c.log.Info().Msg("MeshtasticConnector Init called")
}

func (c *MeshtasticConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "Meshtastic",
		NetworkURL:           "https://meshtastic.org",
		NetworkIcon:          "mxc://catgirl.works/jZRvHJNDFdrEhqrPmkzjOqyA",
		NetworkID:            "meshtastic",
		BeeperBridgeType:     "github.com/kabili207/matrix-meshtastic",
		DefaultPort:          29322,
		DefaultCommandPrefix: "!mt",
	}
}

// GetNetworkID implements bridgev2.NetworkConnector
func (c *MeshtasticConnector) GetNetworkID() string {
	return c.GetName().NetworkID
}

func (c *MeshtasticConnector) GetBaseNodeID() meshid.NodeID {
	if c.baseNodeID == 0 {
		c.baseNodeID = c.MXIDToNodeId(c.bridge.Bot.GetMXID())
	}
	return c.baseNodeID
}

func (c *MeshtasticConnector) IsManagedNode(nodeID meshid.NodeID) bool {
	if v, ok := c.managedNodeCache[nodeID]; ok {
		return v
	}
	baseNode := c.GetBaseNodeID()
	if nodeID == baseNode {
		c.managedNodeCache[baseNode] = true
		return true
	}
	ctx := context.Background()
	ghost, err := c.bridge.GetExistingGhostByID(ctx, meshid.MakeUserID(nodeID))
	if err != nil {
		return false
	}
	if ghost == nil {
		c.managedNodeCache[baseNode] = false
		return false
	}
	meta, ok := ghost.Metadata.(*GhostMetadata)
	isManaged := ok && meta.UserMXID != ""
	c.managedNodeCache[nodeID] = isManaged

	return isManaged
}

func (c *MeshtasticConnector) MXIDToNodeId(mxid id.UserID) meshid.NodeID {
	mxidBytes := []byte(mxid.String())
	return meshid.NodeID(crc32.ChecksumIEEE(mxidBytes))
}

func (tc *MeshtasticConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{
		DisappearingMessages: false,
		AggressiveUpdateInfo: false,
	}
}

func (tc *MeshtasticConnector) GetBridgeInfoVersion() (info, capabilities int) {
	// When you modify the room capabilities, you should increment the second return value to tell
	// mautrix-go to resend all com.beeper.room_features state events. Similarly, if you modify
	// something that affects the uk.half-shot.bridge state event, increment the first return value.
	return 1, 1
}

// Start implements bridgev2.NetworkConnector
// Keeping context here as it was originally present.
func (c *MeshtasticConnector) Start(ctx context.Context) error {
	c.log.Info().Msg("MeshtasticConnector Start called")
	// TODO: Implement actual startup logic if needed (e.g., connect to the network)
	return nil
}

// Stop implements bridgev2.NetworkConnector
// Keeping context here as it was originally present.
func (c *MeshtasticConnector) Stop(ctx context.Context) error {
	c.log.Info().Msg("MeshtasticConnector Stop called")
	// TODO: Implement actual shutdown logic if needed
	return nil
}

type UserLoginMetadata struct {
	ServerNodeId meshid.NodeID `json:"node_id"`
	RootTopic    string        `json:"root_topic"`
}

type PortalMetadata struct {
	ChannelName string  `json:"channel_name"`
	ChannelKey  *string `json:"channel_key"`
}
type GhostMetadata struct {
	LongName  string `json:"long_name"`
	ShortName string `json:"short_name"`
	UserMXID  string `json:"user_mxid"`
}

func (tc *MeshtasticConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal: func() any {
			return &PortalMetadata{}
		},
		Ghost: func() any {
			return &GhostMetadata{}
		},
		Message:  nil,
		Reaction: nil,
		UserLogin: func() any {
			return &UserLoginMetadata{}
		},
	}
}

// --- Login Flow Implementation ---

// GetLoginFlows implements bridgev2.NetworkConnector
func (c *MeshtasticConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{{
		ID:          LoginFlowIDUsernamePassword,
		Name:        "MQTT Topic",
		Description: "Connect to an MQTT topic.",
	}}
}

// CreateLogin implements bridgev2.NetworkConnector
func (c *MeshtasticConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != LoginFlowIDUsernamePassword {
		return nil, fmt.Errorf("unsupported login flow ID: %s", flowID)
	}
	// Now returns SimpleLogin defined in login.go
	return &MeshtasticLogin{
		User: user,
		Main: c, // Pass the connector instance
		Log:  user.Log.With().Str("action", "login").Str("flow", flowID).Logger(),
	}, nil
}

// --- End Login Flow Implementation ---

func (c *MeshtasticConnector) MakeMqttClient(rootTopic string) *mqtt.Client {
	mc := mqtt.NewClient(c.Config.Mqtt.Uri, c.Config.Mqtt.Username, c.Config.Mqtt.Password, rootTopic)
	// Init with Info level rather than the default of Debug, as the MQTT client is VERY noisy
	slogger := slog.New(slogzerolog.Option{Level: slog.LevelInfo, Logger: &c.log}.NewZerologHandler())
	mc.SetLogger(slogger)
	return mc
}

func (c *MeshtasticConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	meta := login.Metadata.(*UserLoginMetadata)

	mqttClient := c.MakeMqttClient(meta.RootTopic)

	meshClient := mesh.NewMeshtasticClient(c.GetBaseNodeID(), mqttClient, c.log.With().Str("topic", meta.RootTopic).Logger())
	meshClient.SetIsManagedNodeHandler(c.IsManagedNode)

	login.Client = &MeshtasticClient{
		UserLogin:  login,
		MeshClient: meshClient,
		log:        c.log.With().Str("user_id", string(login.ID)).Logger(),
		bridge:     c.bridge,
		main:       c,
	}
	return nil
}

func (c *MeshtasticConnector) TryJoinChannels(ctx context.Context, login *bridgev2.UserLogin) {
	login.Client.(*MeshtasticClient).TryJoinChannels(ctx)
}
