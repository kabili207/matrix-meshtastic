package msgconv

import (
	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/format"
)

type MessageConverter struct {
	Bridge     *bridgev2.Bridge
	HTMLParser *format.HTMLParser
	MeshDB     *meshdb.Database
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
