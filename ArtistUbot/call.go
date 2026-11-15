package artistubot

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"ArtistMusic/ArtistCalls"
	tg "github.com/amarnathcjd/gogram/telegram"
)

// ───────────── connect_call ─────────────

func (ctx *Context) connectCall(
	chatId int64,
	mediaDescription artistcalls.MediaDescription,
	jsonParams string,
) error {
	ctx.waitConnectMutex.Lock()
	waitChan := make(chan error)
	ctx.waitConnect[chatId] = waitChan
	ctx.waitConnectMutex.Unlock()

	defer func() {
		ctx.waitConnectMutex.Lock()
		delete(ctx.waitConnect, chatId)
		ctx.waitConnectMutex.Unlock()
	}()

	if chatId >= 0 {
		// P2P call handling
		defer func() {
			ctx.p2pConfigsMutex.Lock()
			delete(ctx.p2pConfigs, chatId)
			ctx.p2pConfigsMutex.Unlock()
		}()

		// Get or create P2P config
		ctx.p2pConfigsMutex.Lock()
		p2pConfig := ctx.p2pConfigs[chatId]
		ctx.p2pConfigsMutex.Unlock()

		if p2pConfig == nil {
			var err error
			p2pConfig, err = ctx.getP2PConfigs(nil)
			if err != nil {
				return err
			}
			ctx.p2pConfigsMutex.Lock()
			ctx.p2pConfigs[chatId] = p2pConfig
			ctx.p2pConfigsMutex.Unlock()
		}

		err := ctx.binding.CreateP2PCall(chatId)
		if err != nil {
			return err
		}

		err = ctx.binding.SetStreamSources(
			chatId,
			artistcalls.CaptureStream,
			mediaDescription,
		)
		if err != nil {
			return err
		}

		ctx.p2pConfigsMutex.Lock()
		dhConfig := artistcalls.DhConfig{
			G:      p2pConfig.DhConfig.G,
			P:      p2pConfig.DhConfig.P,
			Random: p2pConfig.DhConfig.Random,
		}
		gaOrB := p2pConfig.GAorB
		ctx.p2pConfigsMutex.Unlock()

		newGAorB, err := ctx.binding.InitExchange(chatId, dhConfig, gaOrB)
		if err != nil {
			return err
		}

		ctx.p2pConfigsMutex.Lock()
		p2pConfig.GAorB = newGAorB
		ctx.p2pConfigsMutex.Unlock()

		protocolRaw := artistcalls.GetProtocol()
		protocol := &tg.PhoneCallProtocol{
			UdpP2P:          protocolRaw.UdpP2P,
			UdpReflector:    protocolRaw.UdpReflector,
			MinLayer:        protocolRaw.MinLayer,
			MaxLayer:        protocolRaw.MaxLayer,
			LibraryVersions: protocolRaw.Versions,
		}

		userId, err := ctx.app.GetSendableUser(chatId)
		if err != nil {
			return err
		}

		ctx.inputCallsMutex.RLock()
		inputCall := ctx.inputCalls[chatId]
		ctx.inputCallsMutex.RUnlock()

		ctx.p2pConfigsMutex.RLock()
		isOutgoing := p2pConfig.IsOutgoing
		gaOrBHash := p2pConfig.GAorB
		ctx.p2pConfigsMutex.RUnlock()

		if isOutgoing {
			_, err = ctx.app.PhoneRequestCall(
				&tg.PhoneRequestCallParams{
					Protocol: protocol,
					UserID:   userId,
					GAHash:   gaOrBHash,
					RandomID: int32(tg.GenRandInt()),
					Video: mediaDescription.Camera != nil ||
						mediaDescription.Screen != nil,
				},
			)
			if err != nil {
				return err
			}
		} else {
			_, err = ctx.app.PhoneAcceptCall(
				inputCall,
				gaOrBHash,
				protocol,
			)
			if err != nil {
				return err
			}
		}

		select {
		case err = <-p2pConfig.WaitData:
			if err != nil {
				return err
			}
		case <-time.After(10 * time.Second):
			return fmt.Errorf("timed out waiting for an answer")
		}

		ctx.p2pConfigsMutex.RLock()
		gaOrB = p2pConfig.GAorB
		fingerprint := p2pConfig.KeyFingerprint
		ctx.p2pConfigsMutex.RUnlock()

		res, err := ctx.binding.ExchangeKeys(chatId, gaOrB, fingerprint)
		if err != nil {
			return err
		}

		ctx.p2pConfigsMutex.RLock()
		isOutgoing = p2pConfig.IsOutgoing
		ctx.p2pConfigsMutex.RUnlock()

		if isOutgoing {
			confirmRes, err := ctx.app.PhoneConfirmCall(
				inputCall,
				res.GAOrB,
				res.KeyFingerprint,
				protocol,
			)
			if err != nil {
				return err
			}
			ctx.p2pConfigsMutex.Lock()
			p2pConfig.PhoneCall = confirmRes.PhoneCall.(*tg.PhoneCallObj)
			ctx.p2pConfigsMutex.Unlock()
		}

		ctx.p2pConfigsMutex.RLock()
		phoneCall := p2pConfig.PhoneCall
		ctx.p2pConfigsMutex.RUnlock()

		err = ctx.binding.ConnectP2P(
			chatId,
			parseRTCServers(phoneCall.Connections),
			phoneCall.Protocol.LibraryVersions,
			phoneCall.P2PAllowed,
		)
		if err != nil {
			return err
		}

	} else {
		// Group call handling
		var err error
		jsonParams, err = ctx.binding.CreateCall(chatId)
		if err != nil {
			ctx.binding.Stop(chatId)
			return err
		}

		err = ctx.binding.SetStreamSources(chatId, artistcalls.CaptureStream, mediaDescription)
		if err != nil {
			ctx.binding.Stop(chatId)
			return err
		}

		inputGroupCall, err := ctx.GetInputGroupCall(chatId)
		if err != nil {
			ctx.binding.Stop(chatId)
			return err
		}

		resultParams := "{\"transport\": null}"
		callResRaw, err := ctx.app.PhoneJoinGroupCall(
			&tg.PhoneJoinGroupCallParams{
				Muted:        false,
				VideoStopped: mediaDescription.Camera == nil,
				Call:         inputGroupCall,
				Params: &tg.DataJson{
					Data: jsonParams,
				},
				JoinAs: &tg.InputPeerUser{
					UserID:     ctx.self.ID,
					AccessHash: ctx.self.AccessHash,
				},
			},
		)
		if err != nil {
			ctx.binding.Stop(chatId)
			return err
		}

		callRes := callResRaw.(*tg.UpdatesObj)
		for _, u := range callRes.Updates {
			switch update := u.(type) {
			case *tg.UpdateGroupCallConnection:
				resultParams = update.Params.Data
			}
		}

		err = ctx.binding.Connect(chatId, resultParams, false)
		if err != nil {
			return err
		}

		connectionMode, err := ctx.binding.GetConnectionMode(chatId)
		if err != nil {
			return err
		}

		if connectionMode == artistcalls.StreamConnection && len(jsonParams) > 0 {
			ctx.pendingConnectionsMutex.Lock()
			ctx.pendingConnections[chatId] = &PendingConnection{
				MediaDescription: mediaDescription,
				Payload:          jsonParams,
			}
			ctx.pendingConnectionsMutex.Unlock()
		}
	}

	select {
	case err := <-waitChan:
		return err
	case <-time.After(20 * time.Second):
		return fmt.Errorf("connection timeout: no response from ntgcalls")
	}
}

// ───────────── handle_updates ─────────────

type participantUpdate struct {
	participantId      int64
	participant        *tg.GroupCallParticipant
	wasCamera          bool
	wasScreen          bool
	hasCamera          bool
	hasScreen          bool
	cameraEndpoint     string
	screenEndpoint     string
	cameraSourceGroups []*tg.GroupCallParticipantVideoSourceGroup
	screenSourceGroups []*tg.GroupCallParticipantVideoSourceGroup
	oldCameraEndpoint  string
	oldScreenEndpoint  string
}

var (
	ErrConnectionTimeout = errors.New("connection timeout")
	ErrConnectionFailed  = errors.New("connection failed")
)

func (ctx *Context) handleUpdates() {
	ctx.app.AddRawHandler(
		&tg.UpdatePhoneCallSignalingData{},
		func(m tg.Update, c *tg.Client) error {
			signalingData := m.(*tg.UpdatePhoneCallSignalingData)
			userId, err := ctx.convertCallId(signalingData.PhoneCallID)
			if err == nil {
				_ = ctx.binding.SendSignalingData(userId, signalingData.Data)
			}
			return nil
		},
	)

	ctx.app.AddRawHandler(
		&tg.UpdatePhoneCall{},
		func(m tg.Update, _ *tg.Client) error {
			phoneCall := m.(*tg.UpdatePhoneCall).PhoneCall

			var ID int64
			var AccessHash int64
			var userId int64

			switch call := phoneCall.(type) {
			case *tg.PhoneCallAccepted:
				userId = call.ParticipantID
				ID = call.ID
				AccessHash = call.AccessHash
			case *tg.PhoneCallWaiting:
				userId = call.ParticipantID
				ID = call.ID
				AccessHash = call.AccessHash
			case *tg.PhoneCallRequested:
				userId = call.AdminID
				ID = call.ID
				AccessHash = call.AccessHash
			case *tg.PhoneCallObj:
				userId = call.AdminID
			case *tg.PhoneCallDiscarded:
				userId, _ = ctx.convertCallId(call.ID)
			}

			switch phoneCall.(type) {
			case *tg.PhoneCallAccepted, *tg.PhoneCallRequested, *tg.PhoneCallWaiting:
				ctx.inputCallsMutex.Lock()
				ctx.inputCalls[userId] = &tg.InputPhoneCall{
					ID:         ID,
					AccessHash: AccessHash,
				}
				ctx.inputCallsMutex.Unlock()
			}

			ctx.p2pConfigsMutex.RLock()
			p2pConfig := ctx.p2pConfigs[userId]
			ctx.p2pConfigsMutex.RUnlock()

			switch call := phoneCall.(type) {
			case *tg.PhoneCallAccepted:
				if p2pConfig != nil {
					ctx.p2pConfigsMutex.Lock()
					p2pConfig.GAorB = call.GB
					ctx.p2pConfigsMutex.Unlock()
					p2pConfig.WaitData <- nil
				}
			case *tg.PhoneCallObj:
				if p2pConfig != nil {
					ctx.p2pConfigsMutex.Lock()
					p2pConfig.GAorB = call.GAOrB
					p2pConfig.KeyFingerprint = call.KeyFingerprint
					p2pConfig.PhoneCall = call
					ctx.p2pConfigsMutex.Unlock()
					p2pConfig.WaitData <- nil

				}
			case *tg.PhoneCallDiscarded:
				var reasonMessage string

				switch call.Reason.(type) {
				case *tg.PhoneCallDiscardReasonBusy:
					reasonMessage = fmt.Sprintf("the user %d is busy", userId)
				case *tg.PhoneCallDiscardReasonHangup:
					reasonMessage = fmt.Sprintf("call declined by %d", userId)
				}
				if p2pConfig != nil {
					p2pConfig.WaitData <- errors.New(reasonMessage)
				}
				ctx.inputCallsMutex.Lock()
				delete(ctx.inputCalls, userId)
				ctx.inputCallsMutex.Unlock()

				ctx.binding.Stop(userId)

			case *tg.PhoneCallRequested:
				if p2pConfig == nil {
					p2pConfigs, err := ctx.getP2PConfigs(call.GAHash)
					if err != nil {
						return err
					}
					ctx.p2pConfigsMutex.Lock()
					ctx.p2pConfigs[userId] = p2pConfigs
					ctx.p2pConfigsMutex.Unlock()

					ctx.callbacksMutex.RLock()
					callbacks := make([]func(client *Context, chatId int64), len(ctx.incomingCallCallbacks))
					copy(callbacks, ctx.incomingCallCallbacks)
					ctx.callbacksMutex.RUnlock()

					for _, callback := range callbacks {
						go callback(ctx, userId)
					}
				}
			}
			return nil
		},
	)

	ctx.app.AddRawHandler(
		&tg.UpdateGroupCallParticipants{},
		func(m tg.Update, c *tg.Client) error {
			participantsUpdate := m.(*tg.UpdateGroupCallParticipants)
			chatId, err := ctx.convertGroupCallId(
				participantsUpdate.Call.(*tg.InputGroupCallObj).ID,
			)
			if err != nil {
				return nil
			}

			var updates []participantUpdate
			var selfUpdate *tg.GroupCallParticipant

			ctx.participantsMutex.Lock()
			if ctx.callParticipants[chatId] == nil {
				ctx.callParticipants[chatId] = &CallParticipantsCache{
					CallParticipants: make(map[int64]*tg.GroupCallParticipant),
				}
			}

			ctx.callSourcesMutex.Lock()
			if ctx.callSources == nil {
				ctx.callSources = make(map[int64]*CallSources)
			}
			if ctx.callSources[chatId] == nil {
				ctx.callSources[chatId] = &CallSources{
					CameraSources: make(map[int64]string),
					ScreenSources: make(map[int64]string),
				}
			}

			for _, participant := range participantsUpdate.Participants {
				participantId := getParticipantId(participant.Peer)

				if participant.Left {
					delete(
						ctx.callParticipants[chatId].CallParticipants,
						participantId,
					)

					var oldCamera, oldScreen string
					oldCamera = ctx.callSources[chatId].CameraSources[participantId]
					oldScreen = ctx.callSources[chatId].ScreenSources[participantId]
					delete(ctx.callSources[chatId].CameraSources, participantId)
					delete(ctx.callSources[chatId].ScreenSources, participantId)

					if oldCamera != "" || oldScreen != "" {
						updates = append(updates, participantUpdate{
							participantId:     participantId,
							participant:       participant,
							oldCameraEndpoint: oldCamera,
							oldScreenEndpoint: oldScreen,
						})
					}
					continue
				}

				ctx.callParticipants[chatId].CallParticipants[participantId] = participant

				wasCamera := ctx.callSources[chatId].CameraSources[participantId] != ""
				wasScreen := ctx.callSources[chatId].ScreenSources[participantId] != ""
				hasCamera := participant.Video != nil
				hasScreen := participant.Presentation != nil

				update := participantUpdate{
					participantId: participantId,
					participant:   participant,
					wasCamera:     wasCamera,
					wasScreen:     wasScreen,
					hasCamera:     hasCamera,
					hasScreen:     hasScreen,
				}

				if hasCamera && !wasCamera {
					update.cameraEndpoint = participant.Video.Endpoint
					update.cameraSourceGroups = participant.Video.SourceGroups
					ctx.callSources[chatId].CameraSources[participantId] = participant.Video.Endpoint
				} else if !hasCamera && wasCamera {
					update.oldCameraEndpoint = ctx.callSources[chatId].CameraSources[participantId]
					delete(ctx.callSources[chatId].CameraSources, participantId)
				}

				if hasScreen && !wasScreen {
					update.screenEndpoint = participant.Presentation.Endpoint
					update.screenSourceGroups = participant.Presentation.SourceGroups
					ctx.callSources[chatId].ScreenSources[participantId] = participant.Presentation.Endpoint
				} else if !hasScreen && wasScreen {
					update.oldScreenEndpoint = ctx.callSources[chatId].ScreenSources[participantId]
					delete(ctx.callSources[chatId].ScreenSources, participantId)
				}

				if update.cameraEndpoint != "" || update.screenEndpoint != "" ||
					update.oldCameraEndpoint != "" || update.oldScreenEndpoint != "" {
					updates = append(updates, update)
				}

				if participantId == ctx.self.ID {
					selfUpdate = participant
				}
			}

			ctx.callParticipants[chatId].LastMtprotoUpdate = time.Now()
			ctx.callSourcesMutex.Unlock()
			ctx.participantsMutex.Unlock()

			for _, update := range updates {
				if update.cameraEndpoint != "" {
					_, _ = ctx.binding.AddIncomingVideo(
						chatId,
						update.cameraEndpoint,
						parseVideoSources(update.cameraSourceGroups),
					)
				}
				if update.oldCameraEndpoint != "" {
					_ = ctx.binding.RemoveIncomingVideo(
						chatId,
						update.oldCameraEndpoint,
					)
				}

				if update.screenEndpoint != "" {
					_, _ = ctx.binding.AddIncomingVideo(
						chatId,
						update.screenEndpoint,
						parseVideoSources(update.screenSourceGroups),
					)
				}
				if update.oldScreenEndpoint != "" {
					_ = ctx.binding.RemoveIncomingVideo(
						chatId,
						update.oldScreenEndpoint,
					)
				}
			}

			if selfUpdate != nil {
				connectionMode, err := ctx.binding.GetConnectionMode(chatId)
				if err != nil {
					return nil
				}

				if connectionMode == artistcalls.StreamConnection &&
					selfUpdate.CanSelfUnmute {
					ctx.pendingConnectionsMutex.RLock()
					pending := ctx.pendingConnections[chatId]
					ctx.pendingConnectionsMutex.RUnlock()

					if pending != nil {
						ctx.connectCall(
							chatId,
							pending.MediaDescription,
							pending.Payload,
						)
					}

				} else if !selfUpdate.CanSelfUnmute {
					ctx.mutedByAdminMutex.Lock()
					if !slices.Contains(ctx.mutedByAdmin, chatId) {
						ctx.mutedByAdmin = append(ctx.mutedByAdmin, chatId)
					}
					ctx.mutedByAdminMutex.Unlock()

				} else {
					ctx.mutedByAdminMutex.Lock()
					wasMuted := slices.Contains(ctx.mutedByAdmin, chatId)
					if wasMuted {
						ctx.mutedByAdmin = stdRemove(ctx.mutedByAdmin, chatId)
					}
					ctx.mutedByAdminMutex.Unlock()

					if wasMuted {
						state, err := ctx.binding.GetState(chatId)
						if err != nil {
							return nil
						}
						if err := ctx.setCallStatus(participantsUpdate.Call, state); err != nil {
							return nil
						}
					}
				}
			}

			return nil
		},
	)

	ctx.app.AddRawHandler(
		&tg.UpdateGroupCall{},
		func(m tg.Update, c *tg.Client) error {
			updateGroupCall := m.(*tg.UpdateGroupCall)
			if groupCallRaw := updateGroupCall.Call; groupCallRaw != nil {
				chatID, err := ctx.parseChatId(updateGroupCall.Peer)
				if err != nil {
					return err
				}
				switch groupCall := groupCallRaw.(type) {
				case *tg.GroupCallObj:
					ctx.inputGroupCallsMutex.Lock()
					ctx.inputGroupCalls[chatID] = &tg.InputGroupCallObj{
						ID:         groupCall.ID,
						AccessHash: groupCall.AccessHash,
					}
					ctx.inputGroupCallsMutex.Unlock()
					return nil
				case *tg.GroupCallDiscarded:
					ctx.inputGroupCallsMutex.Lock()
					delete(ctx.inputGroupCalls, chatID)
					ctx.inputGroupCallsMutex.Unlock()
					ctx.binding.Stop(chatID)
					// Telegram tore down the call itself — tell listeners
					// (e.g. the music room state) so they don't keep
					// thinking playback is still active.
					go ctx.fireCallDiscarded(chatID)
					return nil
				}
			}
			return nil
		},
	)

	ctx.binding.OnRequestBroadcastTimestamp(func(chatId int64) {
		ctx.inputGroupCallsMutex.RLock()
		inputGroupCall := ctx.inputGroupCalls[chatId]
		ctx.inputGroupCallsMutex.RUnlock()

		if inputGroupCall != nil {
			channels, err := ctx.app.PhoneGetGroupCallStreamChannels(
				inputGroupCall,
			)
			if err == nil {
				_ = ctx.binding.SendBroadcastTimestamp(
					chatId,
					channels.Channels[0].LastTimestampMs,
				)
			}
		}
	})

	ctx.binding.OnRequestBroadcastPart(
		func(chatId int64, segmentPartRequest artistcalls.SegmentPartRequest) {
			ctx.inputGroupCallsMutex.RLock()
			inputGroupCall := ctx.inputGroupCalls[chatId]
			ctx.inputGroupCallsMutex.RUnlock()

			if inputGroupCall != nil {
				file, err := ctx.app.UploadGetFile(
					&tg.UploadGetFileParams{
						Location: &tg.InputGroupCallStream{
							Call:         inputGroupCall,
							TimeMs:       segmentPartRequest.Timestamp,
							Scale:        0,
							VideoChannel: segmentPartRequest.ChannelID,
							VideoQuality: max(
								int32(segmentPartRequest.Quality),
								0,
							),
						},
						Offset: 0,
						Limit:  segmentPartRequest.Limit,
					},
				)

				status := artistcalls.SegmentStatusNotReady
				var data []byte
				data = nil

				if err != nil {
					secondsWait := tg.GetFloodWait(err)
					if secondsWait == 0 {
						status = artistcalls.SegmentStatusResyncNeeded
					}
				} else {
					data = file.(*tg.UploadFileObj).Bytes
					status = artistcalls.SegmentStatusSuccess
				}

				_ = ctx.binding.SendBroadcastPart(
					chatId,
					segmentPartRequest.SegmentID,
					segmentPartRequest.PartID,
					status,
					segmentPartRequest.QualityUpdate,
					data,
				)
			}
		},
	)

	ctx.binding.OnSignal(func(chatId int64, signal []byte) {
		ctx.inputCallsMutex.RLock()
		inputCall := ctx.inputCalls[chatId]
		ctx.inputCallsMutex.RUnlock()

		_, _ = ctx.app.PhoneSendSignalingData(inputCall, signal)
	})

	ctx.binding.OnConnectionChange(
		func(chatId int64, state artistcalls.NetworkInfo) {
			ctx.waitConnectMutex.RLock()
			waitChan := ctx.waitConnect[chatId]
			ctx.waitConnectMutex.RUnlock()

			if waitChan != nil {
				switch state.State {
				case artistcalls.Connected:
					waitChan <- nil
				case artistcalls.Closed, artistcalls.Failed:
					waitChan <- ErrConnectionFailed
				case artistcalls.Timeout:
					waitChan <- ErrConnectionTimeout
				default:
				}
			}
		},
	)

	ctx.binding.OnUpgrade(func(chatId int64, state artistcalls.MediaState) {
		ctx.inputGroupCallsMutex.RLock()
		inputGroupCall := ctx.inputGroupCalls[chatId]
		ctx.inputGroupCallsMutex.RUnlock()

		if err := ctx.setCallStatus(inputGroupCall, state); err != nil {
			fmt.Println(err)
		}
	})

	ctx.binding.OnStreamEnd(
		func(chatId int64, streamType artistcalls.StreamType, streamDevice artistcalls.StreamDevice) {
			ctx.callbacksMutex.RLock()
			callbacks := make(
				[]artistcalls.StreamEndCallback,
				len(ctx.streamEndCallbacks),
			)
			copy(callbacks, ctx.streamEndCallbacks)
			ctx.callbacksMutex.RUnlock()

			for _, callback := range callbacks {
				go callback(chatId, streamType, streamDevice)
			}
		},
	)

	ctx.binding.OnFrame(
		func(chatId int64, mode artistcalls.StreamMode, device artistcalls.StreamDevice, frames []artistcalls.Frame) {
			ctx.callbacksMutex.RLock()
			callbacks := make([]artistcalls.FrameCallback, len(ctx.frameCallbacks))
			copy(callbacks, ctx.frameCallbacks)
			ctx.callbacksMutex.RUnlock()

			for _, callback := range callbacks {
				go callback(chatId, mode, device, frames)
			}
		},
	)
}

// ───────────── join_presentation ─────────────

func (ctx *Context) joinPresentation(chatId int64, join bool) error {
	defer func() {
		ctx.waitConnectMutex.Lock()
		delete(ctx.waitConnect, chatId)
		ctx.waitConnectMutex.Unlock()
	}()

	connectionMode, err := ctx.binding.GetConnectionMode(chatId)
	if err != nil {
		return err
	}

	if connectionMode == artistcalls.StreamConnection {
		ctx.pendingConnectionsMutex.Lock()
		if ctx.pendingConnections[chatId] != nil {
			ctx.pendingConnections[chatId].Presentation = join
		}
		ctx.pendingConnectionsMutex.Unlock()
		return nil
	}

	if connectionMode != artistcalls.RtcConnection {
		return nil
	}

	if join {
		ctx.presentationsMutex.Lock()
		alreadyPresenting := slices.Contains(ctx.presentations, chatId)
		ctx.presentationsMutex.Unlock()

		if alreadyPresenting {
			return nil
		}

		ctx.waitConnectMutex.Lock()
		waitChan := make(chan error)
		ctx.waitConnect[chatId] = waitChan
		ctx.waitConnectMutex.Unlock()

		jsonParams, err := ctx.binding.InitPresentation(chatId)
		if err != nil {
			return err
		}

		ctx.inputGroupCallsMutex.RLock()
		inputGroupCall := ctx.inputGroupCalls[chatId]
		ctx.inputGroupCallsMutex.RUnlock()

		resultParams := "{\"transport\": null}"
		callResRaw, err := ctx.app.PhoneJoinGroupCallPresentation(
			inputGroupCall,
			&tg.DataJson{
				Data: jsonParams,
			},
		)
		if err != nil {
			return err
		}

		callRes := callResRaw.(*tg.UpdatesObj)
		for _, u := range callRes.Updates {
			switch update := u.(type) {
			case *tg.UpdateGroupCallConnection:
				resultParams = update.Params.Data
			}
		}

		err = ctx.binding.Connect(chatId, resultParams, true)
		if err != nil {
			return err
		}

		<-waitChan

		ctx.presentationsMutex.Lock()
		ctx.presentations = append(ctx.presentations, chatId)
		ctx.presentationsMutex.Unlock()

	} else {
		// Leave presentation
		ctx.presentationsMutex.Lock()
		isPresenting := slices.Contains(ctx.presentations, chatId)
		if isPresenting {
			ctx.presentations = stdRemove(ctx.presentations, chatId)
		}
		ctx.presentationsMutex.Unlock()

		if !isPresenting {
			return nil
		}

		err = ctx.binding.StopPresentation(chatId)
		if err != nil {
			return err
		}

		ctx.inputGroupCallsMutex.RLock()
		inputGroupCall := ctx.inputGroupCalls[chatId]
		ctx.inputGroupCallsMutex.RUnlock()

		_, err = ctx.app.PhoneLeaveGroupCallPresentation(inputGroupCall)
		if err != nil {
			return err
		}
	}

	return nil
}

// ───────────── set_call_status ─────────────

func (ctx *Context) setCallStatus(
	call tg.InputGroupCall,
	state artistcalls.MediaState,
) error {
	_, err := ctx.app.PhoneEditGroupCallParticipant(
		&tg.PhoneEditGroupCallParticipantParams{
			Call: call,
			Participant: &tg.InputPeerUser{
				UserID:     ctx.self.ID,
				AccessHash: ctx.self.AccessHash,
			},
			Muted:              state.Muted,
			VideoPaused:        state.VideoPaused,
			VideoStopped:       state.VideoStopped,
			PresentationPaused: state.PresentationPaused,
		},
	)
	return err
}

// ───────────── update_sources ─────────────

type videoToAdd struct {
	participantId int64
	endpoint      string
	sourceGroups  []*tg.GroupCallParticipantVideoSourceGroup
	isCamera      bool
}

func (ctx *Context) updateSources(chatId int64) error {
	participants, err := ctx.GetParticipants(chatId)
	if err != nil {
		return err
	}

	var videosToAdd []videoToAdd
	var shouldAddToMutedByAdmin bool

	ctx.callSourcesMutex.Lock()
	if ctx.callSources[chatId] == nil {
		ctx.callSources[chatId] = &CallSources{
			CameraSources: make(map[int64]string),
			ScreenSources: make(map[int64]string),
		}
	}
	for _, participant := range participants {
		participantId := getParticipantId(participant.Peer)

		if participant.Video != nil &&
			ctx.callSources[chatId].CameraSources[participantId] == "" {
			ctx.callSources[chatId].CameraSources[participantId] = participant.Video.Endpoint
			videosToAdd = append(videosToAdd, videoToAdd{
				participantId: participantId,
				endpoint:      participant.Video.Endpoint,
				sourceGroups:  participant.Video.SourceGroups,
				isCamera:      true,
			})
		}

		if participant.Presentation != nil &&
			ctx.callSources[chatId].ScreenSources[participantId] == "" {
			ctx.callSources[chatId].ScreenSources[participantId] = participant.Presentation.Endpoint
			videosToAdd = append(videosToAdd, videoToAdd{
				participantId: participantId,
				endpoint:      participant.Presentation.Endpoint,
				sourceGroups:  participant.Presentation.SourceGroups,
				isCamera:      false,
			})
		}

		if participantId == ctx.self.ID && !participant.CanSelfUnmute {
			shouldAddToMutedByAdmin = true
		}
	}
	ctx.callSourcesMutex.Unlock()

	for _, video := range videosToAdd {
		_, err = ctx.binding.AddIncomingVideo(
			chatId,
			video.endpoint,
			parseVideoSources(video.sourceGroups),
		)
		if err != nil {
			return err
		}
	}

	if shouldAddToMutedByAdmin {
		ctx.mutedByAdminMutex.Lock()
		if !slices.Contains(ctx.mutedByAdmin, chatId) {
			ctx.mutedByAdmin = append(ctx.mutedByAdmin, chatId)
		}
		ctx.mutedByAdminMutex.Unlock()
	}

	return nil
}
