package artistubot

import (
	tg "github.com/amarnathcjd/gogram/telegram"

	"ArtistMusic/ArtistCalls"
)

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
