package voice

import (
	"fmt"

	"ArtistMusic/internal/artistcalls"
	tg "github.com/amarnathcjd/gogram/telegram"
)

// ───────────── parse_chat_id ─────────────

func (ctx *Context) parseChatId(chatId any) (int64, error) {
	var parsedChatId int64
	switch v := chatId.(type) {
	case int64:
		parsedChatId = v
	case int:
		parsedChatId = int64(v)
	case int32:
		parsedChatId = int64(v)
	case int16:
		parsedChatId = int64(v)
	case int8:
		parsedChatId = int64(v)
	case string:
		rawChat, err := ctx.app.ResolveUsername(chatId.(string))
		if err != nil {
			return 0, fmt.Errorf("failed to resolve username: %w", err)
		}
		switch chat := rawChat.(type) {
		case *tg.UserObj:
			parsedChatId = chat.ID
		case *tg.ChatObj:
			parsedChatId = -chat.ID
		case *tg.Channel:
			parsedChatId = -1000000000000 - chat.ID
		}
	case tg.Peer:
		switch p := v.(type) {
		case *tg.PeerUser:
			parsedChatId = p.UserID
		case *tg.PeerChat:
			parsedChatId = -p.ChatID
		case *tg.PeerChannel:
			parsedChatId = -1000000000000 - p.ChannelID
		}
	default:
		return 0, fmt.Errorf("unsupported chatId type: %T", chatId)
	}

	switch chatId.(type) {
	case int64, int, int32, int16, int8:
		rawChat, err := ctx.app.GetInputPeer(parsedChatId)
		if err != nil {
			return 0, fmt.Errorf("failed to resolve peer: %w", err)
		}
		switch chat := rawChat.(type) {
		case *tg.InputPeerUser:
			parsedChatId = chat.UserID
		case *tg.InputPeerChat:
			parsedChatId = -chat.ChatID
		case *tg.InputPeerChannel:
			parsedChatId = -1000000000000 - chat.ChannelID
		}
	}
	return parsedChatId, nil
}

// ───────────── parse_rtc_servers ─────────────

func parseRTCServers(connections []tg.PhoneConnection) []artistcalls.RTCServer {
	rtcServers := make([]artistcalls.RTCServer, len(connections))
	for i, c := range connections {
		switch connection := c.(type) {
		case *tg.PhoneConnectionWebrtc:
			rtcServers[i] = artistcalls.RTCServer{
				ID:       connection.ID,
				Ipv4:     connection.Ip,
				Ipv6:     connection.Ipv6,
				Username: connection.Username,
				Password: connection.Password,
				Port:     connection.Port,
				Turn:     connection.Turn,
				Stun:     connection.Stun,
			}
		case *tg.PhoneConnectionObj:
			rtcServers[i] = artistcalls.RTCServer{
				ID:      connection.ID,
				Ipv4:    connection.Ip,
				Ipv6:    connection.Ipv6,
				Port:    connection.Port,
				Turn:    true,
				Tcp:     connection.Tcp,
				PeerTag: connection.PeerTag,
			}
		}
	}
	return rtcServers
}

// ───────────── parse_video_sources ─────────────

func parseVideoSources(
	sources []*tg.GroupCallParticipantVideoSourceGroup,
) []artistcalls.SsrcGroup {
	var ssrcGroups []artistcalls.SsrcGroup
	for _, source := range sources {
		var sourceIds []uint32
		for _, sourceId := range source.Sources {
			sourceIds = append(sourceIds, uint32(sourceId))
		}
		ssrcGroups = append(ssrcGroups, artistcalls.SsrcGroup{
			Semantics: source.Semantics,
			Ssrcs:     sourceIds,
		})
	}
	return ssrcGroups
}

// ───────────── convert_call_id ─────────────

func (ctx *Context) convertCallId(callId int64) (int64, error) {
	ctx.inputCallsMutex.RLock()
	defer ctx.inputCallsMutex.RUnlock()

	for chatId, inputCall := range ctx.inputCalls {
		if inputCall.ID == callId {
			return chatId, nil
		}
	}
	return 0, fmt.Errorf("call id %d not found", callId)
}

// ───────────── convert_group_call_id ─────────────

func (ctx *Context) convertGroupCallId(callId int64) (int64, error) {
	ctx.inputGroupCallsMutex.RLock()
	defer ctx.inputGroupCallsMutex.RUnlock()

	for chatId, inputCallInterface := range ctx.inputGroupCalls {
		if inputCall, ok := inputCallInterface.(*tg.InputGroupCallObj); ok {
			if inputCall.ID == callId {
				return chatId, nil
			}
		}
	}
	return 0, fmt.Errorf("group call id %d not found", callId)
}

// ───────────── std_remove ─────────────

func stdRemove[T comparable](slice []T, val T) []T {
	result := make([]T, 0, len(slice))
	for _, v := range slice {
		if v != val {
			result = append(result, v)
		}
	}
	return result
}
