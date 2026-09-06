package msgconv

import (
	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

type MessageConverter struct {
	Bridge     *bridgev2.Bridge
	HTMLParser *format.HTMLParser
	MeshDB     *meshdb.Database
	// ResolveNodeID maps a Matrix user to their mesh node ID. Set by the connector.
	ResolveNodeID func(id.UserID) meshid.NodeID
}

func New(br *bridgev2.Bridge, db *meshdb.Database) *MessageConverter {
	mc := &MessageConverter{
		Bridge: br,
		MeshDB: db,
	}
	mc.HTMLParser = &format.HTMLParser{
		PillConverter: mc.convertPill,
		Newline:       "\n",
		TabsToSpaces:  2,
		BoldConverter: func(text string, ctx format.Context) string {
			return "*" + text + "*"
		},
		ItalicConverter: func(text string, ctx format.Context) string {
			return "_" + text + "_"
		},
		StrikethroughConverter: func(text string, ctx format.Context) string {
			return text
		},
		MonospaceConverter: func(text string, ctx format.Context) string {
			return text
		},
		MonospaceBlockConverter: func(code, language string, ctx format.Context) string {
			return "```\n" + code + "\n```"
		},
	}
	return mc
}
