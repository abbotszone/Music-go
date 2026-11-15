package artistubot

import (
	"time"

	tg "github.com/amarnathcjd/gogram/telegram"

	"ArtistMusic/ArtistCalls"
)

type (
	CallParticipantsCache struct {
		CallParticipants  map[int64]*tg.GroupCallParticipant
		LastMtprotoUpdate time.Time
	}

	CallSources struct {
		CameraSources, ScreenSources map[int64]string
	}

	P2PConfig struct {
		DhConfig       *tg.MessagesDhConfigObj
		PhoneCall      *tg.PhoneCallObj
		IsOutgoing     bool
		KeyFingerprint int64
		GAorB          []byte
		WaitData       chan error
	}

	PendingConnection struct {
		MediaDescription artistcalls.MediaDescription
		Payload          string
		Presentation     bool
	}
)
