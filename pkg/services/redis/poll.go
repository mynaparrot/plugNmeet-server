package redisservice

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/encoding/protojson"
)

// Poll Redis layout (all keys share the pnm:polls:{roomId} room prefix and use the same s.defaultTTL):
//
//	pnm:polls:{roomId}:index              SET of pollIds for the room
//	pnm:polls:{roomId}:meta:{pollId}      HASH: data (PollInfo protojson) plus
//	                                      the three operational fields
//	                                      is_running/expires_at/is_anonymous
//	pnm:polls:{roomId}:voters:{pollId}    HASH userId -> PollRespondent protojson
//	                                      (anonymous polls store the marker)
//	pnm:polls:{roomId}:results:{pollId}   HASH total_resp plus "{optionId}_count"
//	                                      for choice polls or
//	                                      "txt_count:{normKey}"/"txt_disp:{normKey}"
//	                                      for text-answer polls
//
// plus the shared pnm:pollsWithDuration janitor-hint HASH.
const (
	pollsKey             = Prefix + "polls:"
	pollIndexSubKey      = ":index"
	pollMetaSubKey       = ":meta:"
	pollVotersSubKey     = ":voters:"
	pollResultsSubKey    = ":results:"
	pollsWithDurationKey = Prefix + "pollsWithDuration"

	PollDataField        = "data"
	PollIsRunningField   = "is_running"
	PollExpiresAtField   = "expires_at"
	PollIsAnonymousField = "is_anonymous"

	PollTotalRespField = "total_resp"
	PollCountSuffix    = "_count"

	// PollTextCountPrefix keys the aggregated counter of one normalized text
	// answer; PollTextDisplayPrefix stores the first submitted display text.
	PollTextCountPrefix   = "txt_count:"
	PollTextDisplayPrefix = "txt_disp:"

	// PollAnonymousMarker is the opaque voters-hash value stored for anonymous
	// polls; no names or choices are ever stored for them.
	PollAnonymousMarker = "anonymous"
)

func pollRoomIndexKey(roomId string) string {
	return pollsKey + roomId + pollIndexSubKey
}

func pollMetaKey(roomId, pollId string) string {
	return fmt.Sprintf("%s%s%s%s", pollsKey, roomId, pollMetaSubKey, pollId)
}

func pollVotersKey(roomId, pollId string) string {
	return fmt.Sprintf("%s%s%s%s", pollsKey, roomId, pollVotersSubKey, pollId)
}

func pollResultsKey(roomId, pollId string) string {
	return fmt.Sprintf("%s%s%s%s", pollsKey, roomId, pollResultsSubKey, pollId)
}

// pollDurationIndexField returns the field used to index a poll in the
// pollsWithDuration hash: "{roomId}:{pollId}".
func pollDurationIndexField(roomId, pollId string) string {
	return fmt.Sprintf("%s:%s", roomId, pollId)
}

// pollToMetaFields maps a PollInfo onto the metadata hash: the full protojson
// payload plus the three operational fields the Lua scripts touch directly.
func pollToMetaFields(info *plugnmeet.PollInfo) (map[string]interface{}, error) {
	data, err := protojson.Marshal(info)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		PollDataField: string(data),
		// booleans use "1"/"0" to match the Lua scripts' checks
		PollIsRunningField:   u64String(boolU64(info.GetIsRunning())),
		PollExpiresAtField:   strconv.FormatInt(info.GetExpiresAt(), 10),
		PollIsAnonymousField: u64String(boolU64(info.GetIsAnonymous())),
	}, nil
}

// pollFromMetaFields decodes the stored protojson and then overlays the
// operational hash fields, which are authoritative: a stale data payload must
// never override is_running/expires_at/is_anonymous.
func pollFromMetaFields(fields map[string]string) (*plugnmeet.PollInfo, error) {
	raw := fields[PollDataField]
	if raw == "" {
		return nil, fmt.Errorf("poll meta %q field missing", PollDataField)
	}
	info := new(plugnmeet.PollInfo)
	if err := protojson.Unmarshal([]byte(raw), info); err != nil {
		return nil, err
	}
	if v, ok := fields[PollIsRunningField]; ok && v != "" {
		info.IsRunning = v == "1"
	}
	if v, ok := fields[PollExpiresAtField]; ok && v != "" {
		info.ExpiresAt, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := fields[PollIsAnonymousField]; ok && v != "" {
		info.IsAnonymous = v == "1"
	}
	return info, nil
}

func boolU64(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// parseUint64/u64String are the shared counter string conversions used by the
// results hash and the duration index.
func parseUint64(s string) (uint64, error) {
	return strconv.ParseUint(s, 10, 64)
}

func u64String(v uint64) string {
	return strconv.FormatUint(v, 10)
}

func pollOptionCountField(optionId uint64) string {
	return u64String(optionId) + PollCountSuffix
}

// ExtractTextAnswerResults aggregates raw results-hash counters into a sorted
// text-answer list: highest count first, then case-insensitive text ascending.
func ExtractTextAnswerResults(counters map[string]string) []*plugnmeet.PollTextAnswerResult {
	res := make([]*plugnmeet.PollTextAnswerResult, 0, len(counters))
	for k, raw := range counters {
		if !strings.HasPrefix(k, PollTextCountPrefix) {
			continue
		}
		count, err := parseUint64(raw)
		if err != nil || count == 0 {
			continue
		}
		normKey := strings.TrimPrefix(k, PollTextCountPrefix)
		text := counters[PollTextDisplayPrefix+normKey]
		if text == "" {
			text = normKey
		}
		res = append(res, &plugnmeet.PollTextAnswerResult{Text: text, Count: count})
	}
	slices.SortFunc(res, func(a, b *plugnmeet.PollTextAnswerResult) int {
		if c := cmp.Compare(b.GetCount(), a.GetCount()); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.GetText()), strings.ToLower(b.GetText()))
	})
	return res
}

func countersU64(m map[string]string, field string) uint64 {
	v, _ := parseUint64(m[field])
	return v
}
