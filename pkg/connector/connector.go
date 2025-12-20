package connector

import (
	"context"
	_ "embed"
	"encoding/base64"
	"log/slog"

	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb"
	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/kabili207/matrix-meshtastic/pkg/msgconv"
	"github.com/rs/zerolog"
	slogzerolog "github.com/samber/slog-zerolog/v2"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
)

type MeshtasticConnector struct {
	log              zerolog.Logger
	bridge           *bridgev2.Bridge
	Config           Config
	meshDB           *meshdb.Database
	baseNodeID       meshid.NodeID
	meshClient       *mesh.MeshtasticClient
	MsgConv          *msgconv.MessageConverter
	managedNodeCache map[meshid.NodeID]bool
	bgTaskCanceller  context.CancelFunc
	tracerouteTracker *TracerouteTracker
}

var _ bridgev2.NetworkConnector = (*MeshtasticConnector)(nil)

// NewMeshtasticConnector creates a new instance of MeshtasticConnector
func NewMeshtasticConnector(log zerolog.Logger) *MeshtasticConnector {
	return &MeshtasticConnector{
		log:               log.With().Str("component", "network-connector").Logger(),
		managedNodeCache:  map[meshid.NodeID]bool{},
		tracerouteTracker: NewTracerouteTracker(),
	}
}

func (c *MeshtasticConnector) Init(bridge *bridgev2.Bridge) {
	c.meshDB = meshdb.New(bridge.DB.Database, bridge.Log.With().Str("db_section", "meshtastic").Logger())
	c.bridge = bridge
	c.MsgConv = msgconv.New(bridge, c.meshDB)
	c.log = c.bridge.Log
	if c.managedNodeCache == nil {
		c.managedNodeCache = map[meshid.NodeID]bool{}
	}

	c.bridge.Commands.(*commands.Processor).AddHandlers(cmdJoinChannel, cmdUpdateNames, cmdNodeInfo, cmdTraceroute)

	slogger := slog.New(slogzerolog.Option{Level: slog.LevelInfo, Logger: &c.log}.NewZerologHandler())
	slog.SetDefault(slogger)

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

func (c *MeshtasticConnector) GetNetworkID() string {
	return c.GetName().NetworkID
}

func (c *MeshtasticConnector) GetBaseNodeID() meshid.NodeID {
	if c.baseNodeID == 0 {
		c.baseNodeID = meshid.MXIDToNodeID(c.bridge.Bot.GetMXID())
	}
	return c.baseNodeID
}

func (c *MeshtasticConnector) IsManagedNode(nodeID meshid.NodeID) bool {
	if v, ok := c.managedNodeCache[nodeID]; ok {
		return v
	}
	baseNode := c.GetBaseNodeID()
	if nodeID == baseNode {
		c.managedNodeCache[nodeID] = true
		return true
	}
	ctx := context.Background()
	ghost, err := c.bridge.GetExistingGhostByID(ctx, meshid.MakeUserID(nodeID))
	if err != nil {
		return false
	}
	if ghost == nil {
		c.managedNodeCache[nodeID] = false
		return false
	}
	meta, ok := ghost.Metadata.(*meshid.GhostMetadata)
	isManaged := ok && meta.UserMXID != ""
	c.managedNodeCache[nodeID] = isManaged

	return isManaged
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

func (tc *MeshtasticConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal: func() any {
			return &meshid.PortalMetadata{}
		},
		Ghost: func() any {
			return &meshid.GhostMetadata{}
		},
		Message:  nil,
		Reaction: nil,
		UserLogin: func() any {
			return &meshid.UserLoginMetadata{}
		},
	}
}

func (c *MeshtasticConnector) Start(ctx context.Context) error {
	c.log.Info().Msg("MeshtasticConnector Start called")

	c.meshDB.Upgrade(ctx)

	c.meshClient = mesh.NewMeshtasticClient(c.GetBaseNodeID(), c.log.With().Logger())
	c.meshClient.SetHopLimit(c.Config.HopLimit)

	if c.Config.UDP {
		c.meshClient.AddUDPHandler()
	}
	if c.Config.Mqtt.Enabled {
		c.meshClient.AddMQTTHandler(c.Config.Mqtt.Uri, c.Config.Mqtt.Username, c.Config.Mqtt.Password, c.Config.Mqtt.RootTopic)
	}
	c.meshClient.SetIsManagedNodeHandler(c.IsManagedNode)
	c.meshClient.SetOnDisconnectHandler(c.onMeshDisconnected)
	c.meshClient.SetOnConnectHandler(c.onMeshConnected)
	c.meshClient.AddEventHandler(c.handleGlobalMeshEvent)
	c.meshClient.SetPrimaryChannel(c.Config.PrimaryChannel.Name, c.Config.PrimaryChannel.Key)
	c.meshClient.SetPrivateKeyRequestHandler(func(nodeID meshid.NodeID) (key *string) {
		raw, err := c.getGhostPrivateKey(context.Background(), nodeID)
		if err != nil || len(raw) == 0 {
			return nil
		}
		return ptr.Ptr(base64.StdEncoding.EncodeToString(raw))
	})
	c.meshClient.SetPublicKeyRequestHandler(func(nodeID meshid.NodeID) (key *string) {
		raw, err := c.getGhostPublicKey(context.Background(), nodeID)
		if err != nil || len(raw) == 0 {
			return nil
		}
		return ptr.Ptr(base64.StdEncoding.EncodeToString(raw))
	})
	c.meshClient.Connect()

	return nil
}

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

func (c *MeshtasticConnector) onMeshDisconnected() {
	c.log.Error().Msg("Connection to Meshtastic lost")
	if c.bgTaskCanceller != nil {
		c.bgTaskCanceller()
	}
}

func (c *MeshtasticConnector) onMeshConnected(isReconnect bool) {
	c.log.Info().
		Bool("is_reconnect", isReconnect).
		Msg("Connection to Meshtastic established")
	ctx := context.Background()
	if !isReconnect {
		portals, _ := c.bridge.GetAllPortalsWithMXID(ctx)
		for _, p := range portals {
			if channelID, channelKey, err := meshid.ParsePortalID(p.ID); err == nil {
				chanDef, _ := meshid.NewChannelDef(channelID, &channelKey)
				if chanDef.GetKeyString() != channelKey {
					newID := meshid.MakePortalID(channelID, ptr.Ptr(chanDef.GetKeyString()))
					newKey := p.PortalKey
					newKey.ID = newID
					_, _, err := c.bridge.ReIDPortal(ctx, p.PortalKey, newKey)
					if err != nil {
						c.log.Err(err).Msg("Error re-keying portal")
					}
				}
				c.meshClient.AddChannelDef(chanDef)
			}
		}
	}

	bgContext, cancelFunc := context.WithCancel(ctx)
	c.bgTaskCanceller = cancelFunc
	c.RunNodeInfoTask(bgContext)
}
