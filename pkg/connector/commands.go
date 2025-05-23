package connector

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/bridgev2/commands"
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
	},
	RequiresLogin:  false,
	RequiresPortal: false,
}

var cmdNodeInfo = &commands.FullHandler{
	Func: fnNodeInfo,
	Name: "info",
	Help: commands.HelpMeta{
		Section:     HelpSectionNode,
		Description: "Displays info about your Meshtastic node",
	},
	RequiresLogin:  false,
	RequiresPortal: false,
}

func fnJoinChannel(ce *commands.Event) {

	if len(ce.Args) != 2 {
		ce.Reply("**Usage:** `$cmdprefix join-channel <channel_name> <channel_key>`")
		return
	}

	name := ce.Args[0]
	key := ce.Args[1]

	_, _ = name, key

	if key == "AQ==" || key == "AQ" {
		key = "1PG7OiApB1nwvP+rz05pAQ=="
	}

	if login := ce.User.GetDefaultLogin(); login == nil {
		ce.Reply("Login not found")
	} else if !login.Client.IsLoggedIn() {
		ce.Reply("Not logged in")
	} else if err := login.Client.(*MeshtasticClient).MeshClient.AddChannel(name, key); err != nil {
		ce.Log.Err(err).Msg("Failed to join channel")
		ce.Reply("Failed to join channel: %v", err)
	} else if err := login.Client.(*MeshtasticClient).joinChannel(name, key); err != nil {
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
	} else if err := conn.UpdateGhostMeshNames(ce.Ctx, meshid.MakeUserID(nodeID), longName, shortName); err != nil {
		ce.Log.Err(err).Msg("Unable to update user's mesh names")
		ce.Reply("Unable to set your names on Meshtastic: %v", err)
	} else {
		ce.Reply("Successfully updated Meshtastic names")
	}
}

func fnNodeInfo(ce *commands.Event) {

	//if len(ce.Args) < 2 {
	//	ce.Reply("**Usage:** `$cmdprefix <short name> <long name>`")
	//	return
	//}

	userMXID := ce.User.MXID
	nodeID := meshid.MXIDToNodeID(userMXID)

	if ghost, err := ce.Bridge.GetGhostByID(ce.Ctx, meshid.MakeUserID(nodeID)); err != nil {
		ce.Log.Err(err).Msg("Get Matrix user node info")
		ce.Reply("Failed to join channel: %v", err)

	} else if mxUser, err := ce.Bridge.Matrix.GetMemberInfo(ce.Ctx, ce.RoomID, userMXID); err != nil {
		ce.Log.Err(err).Msg("Get Matrix user node info")
		ce.Reply("Failed to join channel: %v", err)
	} else {
		meta, ok := ghost.Metadata.(*meshid.GhostMetadata)
		if !ok {
			meta = &meshid.GhostMetadata{}
		}
		longName := meta.LongName
		shortName := meta.ShortName
		pubKey := "*not generated*"
		if longName == "" {
			longName = TruncateString(mxUser.Displayname, 39)
		}
		if shortName == "" {
			senderStr := nodeID.String()
			shortName = senderStr[len(senderStr)-4:]
		}
		if len(meta.PublicKey) > 0 {
			pubKey = fmt.Sprintf("`%s`", base64.StdEncoding.EncodeToString(meta.PublicKey))
		}
		ce.Reply("Below is your node info on Meshtastic\n**Node ID:** %s\n**Long Name:** %s\n**Short Name:** %s\n**Public Key:** %s", nodeID, longName, shortName, pubKey)
	}

}
