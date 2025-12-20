package connector

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/id"
)

var (
	HelpSectionChannels = commands.HelpSection{Name: "Channel Management", Order: 25}
	HelpSectionNode     = commands.HelpSection{Name: "Node Management", Order: 26}
)

var cmdJoinChannel = &commands.FullHandler{
	Func: fnJoinChannel,
	Name: "join-channel",
	Help: commands.HelpMeta{
		Section:     HelpSectionChannels,
		Description: "Joins a Meshtastic channel with the supplied encryption key",
		Args:        "<_channel name_> <_channel key_>",
	},
	RequiresLogin:  true,
	RequiresPortal: false,
}

var cmdUpdateNames = &commands.FullHandler{
	Func: fnUpdateNames,
	Name: "update-names",
	Help: commands.HelpMeta{
		Section:     HelpSectionNode,
		Description: "Updates the long and short names shown to Meshtastic users",
		Args:        "<_short name_> <_long name_>",
	},
	RequiresLogin:  false,
	RequiresPortal: false,
}

var cmdNodeInfo = &commands.FullHandler{
	Func: fnNodeInfo,
	Name: "info",
	Help: commands.HelpMeta{
		Section:     HelpSectionNode,
		Description: "Displays info about a Meshtastic node",
		Args:        "[_node ID_]",
	},
	RequiresLogin:  false,
	RequiresPortal: false,
}

var cmdTraceroute = &commands.FullHandler{
	Func: fnTraceroute,
	Name: "traceroute",
	Help: commands.HelpMeta{
		Section:     HelpSectionNode,
		Description: "Sends a traceroute request to a Meshtastic node",
		Args:        "<_node ID_>",
	},
	RequiresLogin:  false,
	RequiresPortal: true,
}

func fnJoinChannel(ce *commands.Event) {

	if len(ce.Args) != 2 {
		ce.Reply("**Usage:** `$cmdprefix join-channel <channel_name> <channel_key>`")
		return
	}

	name := ce.Args[0]
	key := ce.Args[1]

	if login := ce.User.GetDefaultLogin(); login == nil {
		ce.Reply("Login not found")
	} else if !login.Client.IsLoggedIn() {
		ce.Reply("Not logged in")
	} else if client, ok := login.Client.(*MeshtasticClient); !ok {
		ce.Log.Error().Msg("Unable to cast MeshtasticClient")
		ce.Reply("Failed to get Meshtastic client (how?!)")
	} else if chanDef, err := meshid.NewChannelDef(name, &key); err != nil {
		ce.Log.Error().Msg("Failed to create channel definition")
		ce.Reply("Failed to join channel: %v", err)
	} else if err := client.MeshClient.AddChannelDef(chanDef); err != nil {
		ce.Log.Err(err).Msg("Failed to add channel to client")
		ce.Reply("Failed to join channel: %v", err)
	} else if err := client.joinChannel(name, chanDef.GetKeyString()); err != nil {
		ce.Log.Err(err).Msg("Failed to join channel")
		ce.Reply("Failed to join channel: %v", err)
	} else {
		ce.Reply("Successfully joined channel, the portal should be created momentarily")
	}
}

func fnUpdateNames(ce *commands.Event) {

	if len(ce.Args) < 2 {
		ce.Reply("**Usage:** `$cmdprefix update-names <short_name> <long_name>`")
		return
	}

	shortName := ce.Args[0]
	longName := strings.Join(ce.Args[1:], " ")

	_, _ = longName, shortName

	userMXID := ce.User.MXID
	nodeID := meshid.MXIDToNodeID(userMXID)

	if len([]byte(longName)) > 39 {
		ce.Reply("Long name must be less than 40 bytes")
	} else if len([]byte(shortName)) > 4 {
		ce.Reply("Short name must be less than 5 bytes")
	} else if conn, ok := ce.Bridge.Network.(*MeshtasticConnector); !ok {
		ce.Log.Error().Msg("Unable to cast MeshtasticConnector")
		ce.Reply("Failed to get Meshtastic connector (how?!)")
	} else if err := conn.UpdateGhostMeshNames(ce.Ctx, meshid.MakeUserID(nodeID), userMXID, longName, shortName); err != nil {
		ce.Log.Err(err).Msg("Unable to update user's mesh names")
		ce.Reply("Unable to set your names on Meshtastic: %v", err)
	} else {
		ce.Reply("Successfully updated Meshtastic names")
	}
}

func fnNodeInfo(ce *commands.Event) {

	userMXID := ce.User.MXID
	fromNode := meshid.MXIDToNodeID(userMXID)
	isMeshNode := false

	if len(ce.Args) > 0 {
		ce.Log.Debug().Msg(ce.Args[0])

		if parsedID, err := meshid.ParseNodeID(ce.Args[0]); err == nil {
			uid := meshid.MakeUserID(parsedID)
			if conn, ok := ce.Bridge.Matrix.(*matrix.Connector); ok {
				userMXID = conn.FormatGhostMXID(uid)
				isMeshNode = true
			}
		} else {
			mtxID := id.UserID(ce.Args[0])
			if _, _, err := mtxID.ParseAndValidate(); err == nil {
				userMXID = mtxID
			}
		}
	}

	nodeID := meshid.MXIDToNodeID(userMXID)
	if gid, ok := ce.Bridge.Matrix.ParseGhostMXID(userMXID); ok {
		nodeID, _ = meshid.ParseUserID(gid)
		isMeshNode = true
	}

	// Just so it's marked as used for now
	_ = isMeshNode

	if conn, ok := ce.Bridge.Network.(*MeshtasticConnector); !ok {
		ce.Log.Error().Msg("Unable to cast Meshtastic connector (how?!)")
	} else if nodeInfo, err := conn.meshDB.MeshNodeInfo.GetByNodeID(ce.Ctx, nodeID); err != nil {
		ce.Log.Err(err).Msg("Unable to search for node info")
		ce.Reply("Failed to fetch node info: %v", err)
	} else if nodeInfo == nil || nodeInfo.LongName == "" || nodeInfo.ShortName == "" {
		if isMeshNode {
			ce.Reply("Unknown node %s. Sending request to mesh", nodeID)
			conn.sendNodeInfo(fromNode, nodeID, true)
		} else {
			ce.Reply("Matrix user %s is not associated with a Meshtastic node", userMXID)
		}
		return
	} else {
		longName := nodeInfo.LongName
		shortName := nodeInfo.ShortName
		pubKey := "*unknown*"
		if len(nodeInfo.PublicKey) > 0 {
			pubKey = fmt.Sprintf("`%s`", base64.StdEncoding.EncodeToString(nodeInfo.PublicKey))
		}
		ce.Reply("**Node ID:** %s\n**Long Name:** %s\n**Short Name:** %s\n**Public Key:** %s", nodeID, longName, shortName, pubKey)
	}

}

func fnTraceroute(ce *commands.Event) {
	if len(ce.Args) < 1 {
		ce.Reply("**Usage:** `$cmdprefix traceroute <node_id>`")
		return
	}

	userMXID := ce.User.MXID
	fromNode := meshid.MXIDToNodeID(userMXID)

	// Parse the target node ID (same format as info command)
	var targetNode meshid.NodeID
	isMeshNode := false

	if parsedID, err := meshid.ParseNodeID(ce.Args[0]); err == nil {
		targetNode = parsedID
		isMeshNode = true
	} else {
		// Try parsing as a Matrix user ID
		mtxID := id.UserID(ce.Args[0])
		if _, _, err := mtxID.ParseAndValidate(); err == nil {
			if gid, ok := ce.Bridge.Matrix.ParseGhostMXID(mtxID); ok {
				if nodeID, err := meshid.ParseUserID(gid); err == nil {
					targetNode = nodeID
					isMeshNode = true
				}
			}
		}
	}

	if !isMeshNode || targetNode == 0 {
		ce.Reply("Invalid node ID: %s", ce.Args[0])
		return
	}

	conn, ok := ce.Bridge.Network.(*MeshtasticConnector)
	if !ok {
		ce.Log.Error().Msg("Unable to cast Meshtastic connector")
		ce.Reply("Failed to get Meshtastic connector")
		return
	}

	// Check rate limit
	canSend, waitTime := conn.tracerouteTracker.CanSendTraceroute(fromNode, targetNode)
	if !canSend {
		ce.Reply("Please wait %d seconds before sending another traceroute to this node", int(waitTime.Seconds()))
		return
	}

	// Get the channel for this portal
	portal := ce.Portal
	if portal == nil {
		ce.Reply("This command must be run in a Meshtastic channel")
		return
	}

	channelID, channelKey, err := meshid.ParsePortalID(portal.ID)
	if err != nil {
		ce.Reply("Failed to get channel info: %v", err)
		return
	}

	channel, err := meshid.NewChannelDef(channelID, &channelKey)
	if err != nil {
		ce.Reply("Failed to create channel definition: %v", err)
		return
	}

	// Send the traceroute request
	packetID, err := conn.meshClient.SendTraceroute(fromNode, targetNode, channel)
	if err != nil {
		ce.Log.Err(err).Msg("Failed to send traceroute")
		ce.Reply("Failed to send traceroute: %v", err)
		return
	}

	// Track the request so we can route the response back to this room
	req := &TracerouteRequest{
		PacketID:   packetID,
		FromNode:   fromNode,
		TargetNode: targetNode,
		RoomID:     portal.MXID,
		Timestamp:  time.Now(),
		Channel:    channel,
	}
	conn.tracerouteTracker.AddRequest(req)

	ce.Reply("Traceroute request sent to %s. Waiting for response...", targetNode)
}
