package msgconv

import (
	"context"
	"fmt"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
)

func (mc *MessageConverter) ToMeshtastic(ctx context.Context, evt *event.Event, content *event.MessageEventContent) (string, error) {
	return mc.parseText(ctx, content), nil
}

func (mc *MessageConverter) parseText(ctx context.Context, content *event.MessageEventContent) (text string) {
	parseCtx := format.NewContext(ctx)
	if content.Format == event.FormatHTML {
		text = mc.HTMLParser.Parse(content.FormattedBody, parseCtx)
	} else {
		text = content.Body
	}
	return
}

func (mc *MessageConverter) convertPill(displayname, mxid, eventID string, ctx format.Context) string {

	if len(mxid) == 0 || mxid[0] != '@' {
		if displayname == "@room" {
			return mesh.BellCharacter
		}
		return format.DefaultPillConverter(displayname, mxid, eventID, ctx)
	}

	var node meshid.NodeID

	if nodeInfo, err := mc.MeshDB.MeshNodeInfo.GetByNodeID(ctx.Ctx, node); err != nil {
		mc.Bridge.Log.Err(err).Msg("Unable to query node info")
	} else if nodeInfo != nil {
		return fmt.Sprintf("@%s", nodeInfo.ShortName)
	}

	nodeStr := node.String()
	shortName := nodeStr[len(nodeStr)-4:]
	return fmt.Sprintf("@%s", shortName)
}
