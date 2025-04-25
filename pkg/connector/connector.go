package connector

import (
	"context"
	_ "embed"
	"hash/crc32"
	"log/slog"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/meshnet-gophers/meshtastic-go/mqtt"
	"github.com/rs/zerolog"
	slogzerolog "github.com/samber/slog-zerolog/v2"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/id"
)

type MeshtasticConnector struct {
	log              zerolog.Logger
	bridge           *bridgev2.Bridge
	Config           Config
	baseNodeID       meshid.NodeID
	meshClient       *mesh.MeshtasticClient
	managedNodeCache map[meshid.NodeID]bool
	bgTaskCanceller  context.CancelFunc
}

var _ bridgev2.NetworkConnector = (*MeshtasticConnector)(nil)

// NewMeshtasticConnector creates a new instance of MeshtasticConnector
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

	c.bridge.Commands.(*commands.Processor).AddHandlers(cmdJoinChannel, cmdUpdateNames, cmdNodeInfo)

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
func (c *MeshtasticConnector) Start(ctx context.Context) error {
	c.log.Info().Msg("MeshtasticConnector Start called")
	mqttClient := mqtt.NewClient(c.Config.Mqtt.Uri, c.Config.Mqtt.Username, c.Config.Mqtt.Password, c.Config.Mqtt.RootTopic)
	// Init with Info level rather than the default of Debug, as the MQTT client is VERY noisy
	slogger := slog.New(slogzerolog.Option{Level: slog.LevelInfo, Logger: &c.log}.NewZerologHandler())
	mqttClient.SetLogger(slogger)

	c.meshClient = mesh.NewMeshtasticClient(c.GetBaseNodeID(), mqttClient, c.log.With().Logger())
	c.meshClient.SetIsManagedNodeHandler(c.IsManagedNode)
	c.meshClient.SetOnConnectHandler(c.onMeshConnected)
	c.meshClient.Connect()

	return nil
}

// Stop implements bridgev2.NetworkConnector
func (c *MeshtasticConnector) Stop(ctx context.Context) error {
	c.log.Info().Msg("MeshtasticConnector Stop called")

	if c.bgTaskCanceller != nil {
		c.bgTaskCanceller()
	}

	if c.meshClient != nil {
		c.meshClient.Disconnect()
	}
	return nil
}

func (c *MeshtasticConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	login.Client = &MeshtasticClient{
		UserLogin:  login,
		MeshClient: c.meshClient,
		log:        c.log.With().Str("user_id", string(login.ID)).Logger(),
		bridge:     c.bridge,
		main:       c,
	}
	return nil
}

func (c *MeshtasticConnector) onMeshConnected(client *mesh.MeshtasticClient) {
	client.AddChannel(c.Config.PrimaryChannel.Name, c.Config.PrimaryChannel.Key)

	ctx := context.Background()

	portals, _ := c.bridge.GetAllPortalsWithMXID(ctx)
	for _, p := range portals {
		if channelID, channelKey, err := meshid.ParsePortalID(p.ID); err == nil {
			client.AddChannel(channelID, channelKey)
		}
	}

	bgContext, cancelFunc := context.WithCancel(ctx)
	c.bgTaskCanceller = cancelFunc
	c.RunNodeInfoTask(bgContext)
}
