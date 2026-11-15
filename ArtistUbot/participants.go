package artistubot

import (
	"fmt"
	"maps"
	"slices"
	"time"

	tg "github.com/amarnathcjd/gogram/telegram"
)

// ───────────── get_input_group_call ─────────────

func (ctx *Context) GetInputGroupCall(chatId int64) (tg.InputGroupCall, error) {
	ctx.inputGroupCallsMutex.RLock()
	call, ok := ctx.inputGroupCalls[chatId]
	ctx.inputGroupCallsMutex.RUnlock()

	if ok {
		if call == nil {
			return nil, fmt.Errorf("group call for chatId %d is closed", chatId)
		}
		return call, nil
	}

	peer, err := ctx.app.ResolvePeer(chatId)
	if err != nil {
		return nil, err
	}

	var retrievedCall tg.InputGroupCall

	switch chatPeer := peer.(type) {
	case *tg.InputPeerChannel:
		fullChat, err := ctx.app.ChannelsGetFullChannel(
			&tg.InputChannelObj{
				ChannelID:  chatPeer.ChannelID,
				AccessHash: chatPeer.AccessHash,
			},
		)
		if err != nil {
			return nil, err
		}
		retrievedCall = fullChat.FullChat.(*tg.ChannelFull).Call
	case *tg.InputPeerChat:
		fullChat, err := ctx.app.MessagesGetFullChat(chatPeer.ChatID)
		if err != nil {
			return nil, err
		}
		retrievedCall = fullChat.FullChat.(*tg.ChatFullObj).Call
	default:
		return nil, fmt.Errorf("chatId %d is not a group call", chatId)
	}

	ctx.inputGroupCallsMutex.Lock()
	ctx.inputGroupCalls[chatId] = retrievedCall
	ctx.inputGroupCallsMutex.Unlock()

	if retrievedCall == nil {
		return nil, fmt.Errorf("group call for chatId %d is closed", chatId)
	}
	return retrievedCall, nil
}

// ───────────── get_p2p_configs ─────────────

func (ctx *Context) getP2PConfigs(GAorB []byte) (*P2PConfig, error) {
	dhConfigRaw, err := ctx.app.MessagesGetDhConfig(0, 256)
	if err != nil {
		return nil, err
	}
	dhConfig := dhConfigRaw.(*tg.MessagesDhConfigObj)
	return &P2PConfig{
		DhConfig:   dhConfig,
		IsOutgoing: GAorB == nil,
		GAorB:      GAorB,
		WaitData:   make(chan error),
	}, nil
}

// ───────────── get_participant_id ─────────────

func getParticipantId(peer tg.Peer) int64 {
	var participantId int64
	switch chatObj := peer.(type) {
	case *tg.PeerUser:
		participantId = chatObj.UserID
	case *tg.PeerChannel:
		participantId = chatObj.ChannelID
	case *tg.PeerChat:
		participantId = chatObj.ChatID
	}
	return participantId
}

// ───────────── get_participants ─────────────

func (ctx *Context) GetParticipants(
	chatId int64,
) ([]*tg.GroupCallParticipant, error) {
	ctx.participantsMutex.Lock()
	defer ctx.participantsMutex.Unlock()
	if ctx.callParticipants[chatId] == nil {
		ctx.callParticipants[chatId] = &CallParticipantsCache{
			CallParticipants: make(map[int64]*tg.GroupCallParticipant),
		}
	}
	if time.Since(
		ctx.callParticipants[chatId].LastMtprotoUpdate,
	) > time.Minute {
		groupCall, err := ctx.GetInputGroupCall(chatId)
		if err != nil {
			return nil, err
		}
		ctx.callParticipants[chatId].CallParticipants = make(
			map[int64]*tg.GroupCallParticipant,
		)
		var nextOffset string
		for {
			res, err := ctx.app.PhoneGetGroupParticipants(
				groupCall,
				[]tg.InputPeer{},
				[]int32{},
				nextOffset,
				0,
			)
			if err != nil {
				return nil, err
			}
			for _, participant := range res.Participants {
				ctx.callParticipants[chatId].CallParticipants[getParticipantId(participant.Peer)] = participant
			}
			if res.NextOffset == "" {
				break
			}
			nextOffset = res.NextOffset
		}
		ctx.callParticipants[chatId].LastMtprotoUpdate = time.Now()
	}
	return slices.Collect(
		maps.Values(ctx.callParticipants[chatId].CallParticipants),
	), nil
}
