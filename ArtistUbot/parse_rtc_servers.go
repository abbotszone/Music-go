package artistubot

import (
	tg "github.com/amarnathcjd/gogram/telegram"

	"ArtistMusic/ArtistCalls"
)

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
