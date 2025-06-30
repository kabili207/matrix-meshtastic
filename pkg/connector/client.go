package connector

import (
	"context"
	"errors"
	"fmt"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
)

type MeshtasticClient struct {
	log        zerolog.Logger
	bridge     *bridgev2.Bridge
	main       *MeshtasticConnector
	UserLogin  *bridgev2.UserLogin
	MeshClient *mesh.MeshtasticClient
}

var _ bridgev2.NetworkAPI = (*MeshtasticClient)(nil)
var _ bridgev2.IdentifierResolvingNetworkAPI = (*MeshtasticClient)(nil)

func (mc *MeshtasticClient) Connect(ctx context.Context) {
	nodeID := meshid.MXIDToNodeID(mc.UserLogin.UserMXID)
	nodeInfo, err := mc.main.meshDB.MeshNodeInfo.GetByNodeID(ctx, nodeID)
	if err != nil {
		mc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateBadCredentials,
			Error:      "meshtastic-node-db-error",
			Message:    "Failed to retrieve node info",
			Info: map[string]any{
				"go_error": err.Error(),
			},
		})
		return
	}
	if nodeInfo == nil {
		mc.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateBadCredentials,
			Error:      "meshtastic-node-info-missing",
			Message:    "Node info for this user is missing",
			Info: map[string]any{
				"node_id": nodeID.String(),
			},
		})
		return
	}
	if len(nodeInfo.PrivateKey) == 0 {
		mc.log.Debug().Msg("Generating new keypair")
		pub, priv, err := mc.main.meshClient.GenerateKeyPair()
		if err != nil {
			mc.UserLogin.BridgeState.Send(status.BridgeState{
				StateEvent: status.StateBadCredentials,
				Error:      "meshtastic-key-pair-generation-error",
				Message:    "Unable to generate key pair",
				Info: map[string]any{
					"go_error": err.Error(),
				},
			})
			return
		}
		nodeInfo.PublicKey = pub
		nodeInfo.PrivateKey = priv

		if err := nodeInfo.SetAll(ctx); err != nil {
			mc.UserLogin.BridgeState.Send(status.BridgeState{
				StateEvent: status.StateBadCredentials,
				Error:      "meshtastic-node-info-save-error",
				Message:    "Unable to save updated node info",
				Info: map[string]any{
					"go_error": err.Error(),
				},
			})
			return
		}
	}
	mc.MeshClient.AddEventHandler(mc.handleMeshEvent)
}

func (mc *MeshtasticClient) Disconnect() {
	mc.MeshClient.Disconnect()
}

func (tc *MeshtasticClient) IsLoggedIn() bool {
	return tc.MeshClient.IsConnected()
}

// LogoutRemote is a no-op for this simple connector.
func (nc *MeshtasticClient) LogoutRemote(ctx context.Context) {
	nc.log.Info().Msg("MeshtasticClient LogoutRemote called (no-op)")
}

func (s *MeshtasticClient) makePortalKey(channelID string, channelKey *string) networkid.PortalKey {
	key := networkid.PortalKey{
		ID: meshid.MakePortalID(channelID, channelKey),
	}
	// For non-group chats, add receiver
	if s.bridge.Config.SplitPortals {
		key.Receiver = s.UserLogin.ID
	}
	return key
}

func (s *MeshtasticClient) makeDMPortalKey(remote, synth meshid.NodeID) networkid.PortalKey {
	key := networkid.PortalKey{
		ID:       meshid.MakeDMPortalID(remote, synth),
		Receiver: s.UserLogin.ID,
	}
	return key
}

func (s *MeshtasticClient) makeEventSender(sender meshid.NodeID) bridgev2.EventSender {
	meta := s.UserLogin.Metadata.(*meshid.UserLoginMetadata)
	return bridgev2.EventSender{
		IsFromMe:    meta.NodeID == sender,
		Sender:      meshid.MakeUserID(sender),
		SenderLogin: meshid.MakeUserLoginID(sender),
	}
}

func (tc *MeshtasticClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	meta := tc.UserLogin.Metadata.(*meshid.UserLoginMetadata)
	return meshid.MakeUserID(meta.NodeID) == userID
}

func (tc *MeshtasticClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	remoteNode, synthNode, err1 := meshid.ParseDMPortalID(portal.ID)
	if err1 == nil {
		portal.RoomType = database.RoomTypeDM
		return tc.wrapDMInfo(synthNode, remoteNode), nil
	}
	channelName, channelKey, err2 := meshid.ParsePortalID(portal.ID)
	if err2 == nil {
		portal.RoomType = database.RoomTypeDefault
		return tc.wrapChatInfo(nil, channelName, channelKey), nil
	}
	return nil, errors.Join(err1, err2)
}

func (c *MeshtasticClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if nodeID, err := meshid.ParseUserID(ghost.ID); err != nil {
		return nil, err
	} else if nodeInfo, err := c.main.meshDB.MeshNodeInfo.GetByNodeID(ctx, nodeID); err != nil {
		return nil, err
	} else if nodeInfo != nil && nodeInfo.LongName != "" {
		return &bridgev2.UserInfo{
			Name: ptr.Ptr(nodeInfo.LongName),
		}, nil
	} else {
		long, _ := nodeID.GetDefaultNodeNames()
		return &bridgev2.UserInfo{
			Name: ptr.Ptr(long),
		}, nil
	}

}

func (c *MeshtasticClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	remoteNode, err := meshid.ParseNodeID(identifier)
	if err != nil {
		return nil, err
	}
	ghost, err := c.main.bridge.GetGhostByID(ctx, meshid.MakeUserID(remoteNode))
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost: %w", err)
	}
	myNode := meshid.MXIDToNodeID(c.UserLogin.UserMXID)

	return &bridgev2.ResolveIdentifierResponse{
		Ghost:  ghost,
		UserID: meshid.MakeUserID(remoteNode),
		Chat: &bridgev2.CreateChatResponse{
			PortalKey:  c.makeDMPortalKey(remoteNode, myNode),
			PortalInfo: c.wrapDMInfo(myNode, myNode),
		},
	}, nil
}
