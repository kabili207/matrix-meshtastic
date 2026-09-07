package connector

import (
	"context"
	_ "embed"
	"log/slog"
	"sync"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/kabili207/matrix-meshtastic/pkg/msgconv"
	"github.com/kabili207/meshtastic-go/core"
	pb "github.com/kabili207/meshtastic-go/core/proto"
	"github.com/kabili207/meshtastic-go/device/node"
	"github.com/kabili207/meshtastic-go/transport"
	"github.com/kabili207/meshtastic-go/transport/mqtt"
	"github.com/kabili207/meshtastic-go/transport/raw"
	"github.com/kabili207/meshtastic-go/transport/udp"
	"github.com/rs/zerolog"
	slogzerolog "github.com/samber/slog-zerolog/v2"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

type MeshtasticConnector struct {
	log               zerolog.Logger
	bridge            *bridgev2.Bridge
	Config            Config
	meshDB            *meshdb.Database
	baseNodeID        meshid.NodeID
	meshBridge        *node.BridgeNode
	meshCancel        context.CancelFunc
	primaryChannel    *core.Channel
	MsgConv           *msgconv.MessageConverter
	managedNodeCache  map[meshid.NodeID]bool
	bgTaskCanceller   context.CancelFunc
	tracerouteTracker *TracerouteTracker
	prevConnected     bool

	// rootKey is the secret every managed identity derives from; see identity.go.
	rootKey       []byte
	identityMu    sync.RWMutex
	identityCache map[id.UserID]meshid.NodeID
}

// PrimaryChannel returns the configured primary channel for outbound sends.
func (c *MeshtasticConnector) PrimaryChannel() *core.Channel {
	return c.primaryChannel
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
	c.MsgConv.ResolveNodeID = c.NodeIDForMXID
	c.identityCache = map[id.UserID]meshid.NodeID{}
	c.log = c.bridge.Log
	if c.managedNodeCache == nil {
		c.managedNodeCache = map[meshid.NodeID]bool{}
	}
	if c.tracerouteTracker == nil {
		c.tracerouteTracker = NewTracerouteTracker()
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

// GetBaseNodeID returns the bridge's own node ID, fixed by ensureIdentity at start.
func (c *MeshtasticConnector) GetBaseNodeID() meshid.NodeID {
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

	if err := c.meshDB.Upgrade(ctx); err != nil {
		c.log.Err(err).Msg("Failed to upgrade mesh database")
		return err
	}
	if err := c.ensureIdentity(ctx); err != nil {
		c.log.Err(err).Msg("Failed to establish bridge identity")
		return err
	}

	// Let zerolog's level govern library output. MQTT stays at Info because paho
	// is very noisy at debug.
	meshLogger := slog.New(slogzerolog.Option{Level: slog.LevelDebug, Logger: ptr.Ptr(c.log.With().Logger())}.NewZerologHandler())
	mqttLogger := slog.New(slogzerolog.Option{Level: slog.LevelInfo, Logger: ptr.Ptr(c.log.With().Logger())}.NewZerologHandler())

	// Build the primary channel and the channel set used by the bridge.
	primary, err := core.NewChannel(c.Config.PrimaryChannel.Name, c.Config.PrimaryChannel.Key)
	if err != nil {
		c.log.Err(err).Msg("Invalid primary channel configuration")
		return err
	}
	c.primaryChannel = primary
	channelSet := &pb.ChannelSet{
		Settings: []*pb.ChannelSettings{{Name: primary.GetName(), Psk: primary.GetKeyBytes()}},
	}

	// Build the transports. MQTT is deprioritized relative to UDP so the faster
	// path delivers first and slower MQTT sends are paced behind it.
	var transports []raw.TransportOption
	if c.Config.UDP {
		transports = append(transports, raw.TransportOption{
			Transport: udp.New(udp.Config{Logger: meshLogger}),
		})
	}
	if c.Config.Mqtt.Enabled {
		transports = append(transports, raw.TransportOption{
			Transport: mqtt.New(mqtt.Config{
				Broker:   c.Config.Mqtt.Uri,
				Username: c.Config.Mqtt.Username,
				Password: c.Config.Mqtt.Password,
				Root:     c.Config.Mqtt.RootTopic,
				NodeID:   c.GetBaseNodeID().Core(),
				Logger:   mqttLogger,
			}),
			SendDelay: 700 * time.Millisecond,
			RecvDelay: 500 * time.Millisecond,
		})
	}
	multi := raw.NewMultiTransport(raw.MultiConfig{Logger: meshLogger}, transports...)

	bridgeNode, err := node.NewBridge(node.BridgeConfig{
		Transport:       multi,
		NodeID:          c.GetBaseNodeID().Core(),
		LongName:        c.Config.LongName,
		ShortName:       c.Config.ShortName,
		HwModel:         pb.HardwareModel_PRIVATE_HW,
		Channels:        channelSet,
		DefaultHopLimit: c.Config.HopLimit,
		OkToMQTT:        true,
		IsManagedNode: func(nodeID core.NodeID) bool {
			return c.IsManagedNode(meshid.FromCore(nodeID))
		},
		PrivateKeyForNode: func(nodeID core.NodeID) []byte {
			raw, err := c.getGhostPrivateKey(context.Background(), meshid.FromCore(nodeID))
			if err != nil {
				return nil
			}
			return raw
		},
		PublicKeyForNode: func(nodeID core.NodeID) []byte {
			raw, err := c.getGhostPublicKey(context.Background(), meshid.FromCore(nodeID))
			if err != nil {
				return nil
			}
			return raw
		},
		NodeInfoForNode: c.nodeInfoForNode,
		NeighborProvider: func(nodeID core.NodeID) []core.NodeID {
			directNeighbors, err := c.meshDB.MeshNodeInfo.GetDirectNeighbors(context.Background())
			if err != nil {
				c.log.Err(err).Msg("Failed to get neighbors for request response")
				return nil
			}
			ids := make([]core.NodeID, 0, len(directNeighbors))
			for _, n := range directNeighbors {
				ids = append(ids, n.NodeID.Core())
			}
			return ids
		},
		NeighborBroadcastInterval: uint32(rateNeighborInfo.Seconds()),
		HostMetricsProvider:       c.buildHostMetrics,
		OnStateChange:             c.onMeshStateChange,
		Logger:                    meshLogger,
	})
	if err != nil {
		c.log.Err(err).Msg("Failed to create mesh bridge")
		return err
	}
	c.meshBridge = bridgeNode
	c.meshBridge.OnEvent(c.handleGlobalMeshEvent)

	runCtx, cancel := context.WithCancel(context.Background())
	c.meshCancel = cancel
	go func() {
		if err := c.meshBridge.Run(runCtx); err != nil {
			c.log.Err(err).Msg("Mesh bridge stopped with error")
		}
	}()

	return nil
}

// nodeInfoForNode supplies NodeInfo details for a managed ghost to the bridge.
// The bridge's own identity comes from its config and PublicKeyForNode instead.
func (c *MeshtasticConnector) nodeInfoForNode(nodeID core.NodeID) (longName, shortName string, pubKey []byte, ok bool) {
	id := meshid.FromCore(nodeID)
	if !c.IsManagedNode(id) {
		return "", "", nil, false
	}
	nodeInfo, err := c.meshDB.MeshNodeInfo.GetByNodeID(context.Background(), id)
	if err != nil || nodeInfo == nil {
		long, short := id.GetDefaultNodeNames()
		return long, short, nil, true
	}
	return nodeInfo.LongName, nodeInfo.ShortName, nodeInfo.PublicKey, true
}

// onMeshStateChange translates aggregated transport state changes into the
// connector's connect/disconnect handling.
func (c *MeshtasticConnector) onMeshStateChange(e transport.ListenerEvent) {
	switch e {
	case transport.ListenerEventConnected:
		isReconnect := c.prevConnected
		c.prevConnected = true
		c.onMeshConnected(isReconnect)
	case transport.ListenerEventReconnecting:
		// no-op: wait for the reconnect to complete
	case transport.ListenerEventDisconnected:
		c.onMeshDisconnected()
	}
}

func (c *MeshtasticConnector) Stop(ctx context.Context) error {
	c.log.Info().Msg("MeshtasticConnector Stop called")

	if c.bgTaskCanceller != nil {
		c.bgTaskCanceller()
	}

	if c.meshCancel != nil {
		c.meshCancel()
	}
	return nil
}

func (c *MeshtasticConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	login.Client = &MeshtasticClient{
		UserLogin: login,
		log:       c.log.With().Str("user_id", string(login.ID)).Logger(),
		bridge:    c.bridge,
		main:      c,
	}
	return nil
}

// seedNodeChannels restores each node's last-heard channel into the bridge's
// in-memory node DB so DMs follow it from the first send after a restart. Runs
// after portal channels are registered, since unregistered names are rejected.
func (c *MeshtasticConnector) seedNodeChannels(ctx context.Context) {
	nodes, err := c.meshDB.MeshNodeInfo.GetNodesWithChannel(ctx)
	if err != nil {
		c.log.Err(err).Msg("Failed to load node channels")
		return
	}
	seeded := 0
	for _, n := range nodes {
		ch := c.storedChannel(ctx, n)
		if ch != nil && c.meshBridge.SetNodeChannel(n.NodeID.Core(), ch) {
			seeded++
		} else {
			c.log.Debug().Stringer("node_id", n.NodeID).Str("channel", n.Channel).Msg("Node channel not registered, skipping")
		}
	}
	c.log.Info().Int("seeded", seeded).Int("known", len(nodes)).Msg("Restored node channels")
}

// storedChannel rebuilds the channel a node was last heard on. Rows recorded
// before the key was stored carry only a name; those resolve against the
// channels the bridge actually has, and are skipped when the name is ambiguous.
// The next NodeInfo from the node records the key.
func (c *MeshtasticConnector) storedChannel(ctx context.Context, n *meshdb.MeshNodeInfo) *core.Channel {
	if n.ChannelKey != "" {
		ch, err := core.NewChannel(n.Channel, n.ChannelKey)
		if err != nil {
			return nil
		}
		return ch
	}
	var found *core.Channel
	consider := func(ch *core.Channel) bool {
		if ch == nil || ch.GetName() != n.Channel {
			return true
		}
		if found != nil && !core.SameChannel(found, ch) {
			found = nil
			return false
		}
		found = ch
		return true
	}
	if !consider(c.primaryChannel) {
		return nil
	}
	portals, err := c.bridge.GetAllPortalsWithMXID(ctx)
	if err != nil {
		return nil
	}
	for _, p := range portals {
		if ch, err := meshid.ChannelDefFromPortalID(p.ID); err == nil && !consider(ch) {
			return nil
		}
	}
	return found
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
				if err := c.meshBridge.AddChannel(channelID, chanDef.GetKeyString()); err != nil {
					c.log.Err(err).Msg("Error adding channel")
				}
			}
		}
		c.seedNodeChannels(ctx)
	}

	// Cancel any previously running background tasks before restarting them.
	if c.bgTaskCanceller != nil {
		c.bgTaskCanceller()
	}
	bgContext, cancelFunc := context.WithCancel(ctx)
	c.bgTaskCanceller = cancelFunc
	c.RunNodeInfoTask(bgContext)
	c.RunInactiveCleanupTask(bgContext)
}
