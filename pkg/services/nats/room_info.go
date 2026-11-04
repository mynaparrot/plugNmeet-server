package natsservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
)

// GetRoomInfo retrieves the room information for the given roomId from the consolidated bucket
func (s *NatsService) GetRoomInfo(roomId string) (*plugnmeet.NatsKvRoomInfo, error) {
	// try to get cached room info first
	if info := s.cs.getCachedRoomInfo(roomId); info != nil {
		return info, nil
	}

	bucket := s.formatConsolidatedRoomBucket(roomId)
	kv, err := s.getKV(bucket)
	if err != nil || kv == nil {
		return nil, err
	}

	info := new(plugnmeet.NatsKvRoomInfo)
	info.DbTableId, _ = s.getUint64Value(kv, s.formatRoomKey(RoomDbTableIdKey))
	info.RoomId, _ = s.getStringValue(kv, s.formatRoomKey(RoomIdKey))
	info.RoomSid, _ = s.getStringValue(kv, s.formatRoomKey(RoomSidKey))
	info.Status, _ = s.getStringValue(kv, s.formatRoomKey(RoomStatusKey))
	info.EmptyTimeout, _ = s.getUint64Value(kv, s.formatRoomKey(RoomEmptyTimeoutKey))
	info.MaxParticipants, _ = s.getUint64Value(kv, s.formatRoomKey(RoomMaxParticipants))
	info.CreatedAt, _ = s.getUint64Value(kv, s.formatRoomKey(RoomCreatedKey))
	info.Metadata, _ = s.getStringValue(kv, s.formatRoomKey(RoomMetadataKey))

	// So, for some reason, if the room info is not found in the cache,
	// then may be room wasn't created in this server.
	// So, we will start watching if status not ended
	if info.Status != RoomStatusEnded {
		s.cs.addRoomWatcher(kv, bucket, roomId)
	}

	return info, nil
}

// GetRoomInfoWithMetadata retrieves the room information along with metadata for the given roomId
func (s *NatsService) GetRoomInfoWithMetadata(roomId string) (*plugnmeet.NatsKvRoomInfo, *plugnmeet.RoomMetadata, error) {
	info, err := s.GetRoomInfo(roomId)
	if err != nil || info == nil {
		return nil, nil, err
	}

	metadata, err := s.UnmarshalRoomMetadata(info.Metadata)
	if err != nil {
		return nil, nil, err
	}

	return info, metadata, nil
}

// GetRoomMetadataStruct retrieves the room metadata as a structured object for the given roomId.
// It is highly optimized to check the cache first and then fetch only the required key.
func (s *NatsService) GetRoomMetadataStruct(roomId string) (*plugnmeet.RoomMetadata, error) {
	// Use the dedicated cache method to get only the metadata.
	if metadata, found := s.cs.getCachedRoomMetadata(roomId); found {
		if len(metadata) > 0 {
			return s.UnmarshalRoomMetadata(metadata)
		}
	}

	// If not in cache, directly fetch only the metadata key from NATS KV.
	bucket := s.formatConsolidatedRoomBucket(roomId)
	kv, err := s.getKV(bucket)
	if err != nil {
		return nil, err
	}
	if kv == nil {
		// Bucket not found, so no metadata
		return nil, nil
	}

	entry, err := kv.Get(s.ctx, s.formatRoomKey(RoomMetadataKey))
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, nil // Metadata key not found
		}
		return nil, err
	}

	if entry == nil || len(entry.Value()) == 0 {
		return nil, nil
	}

	return s.UnmarshalRoomMetadata(string(entry.Value()))
}

// PrepareRoomMetadataForWaitingUser returns a minimal room metadata JSON for users
// who are still waiting for approval. It strips sensitive data like the E2EE
// encryption key and most room features.
func (s *NatsService) PrepareRoomMetadataForWaitingUser(rawMetadata string) (string, error) {
	meta, err := s.UnmarshalRoomMetadata(rawMetadata)
	if err != nil {
		return "", err
	}

	return s.RedactRoomMetadataForWaitingUser(meta)
}

// RedactRoomMetadataForWaitingUser builds a minimal metadata JSON out of the given
// room metadata. See PrepareRoomMetadataForWaitingUser.
func (s *NatsService) RedactRoomMetadataForWaitingUser(meta *plugnmeet.RoomMetadata) (string, error) {
	if meta == nil {
		return "", errors.New("metadata cannot be nil")
	}

	rf := meta.GetRoomFeatures()
	redacted := &plugnmeet.RoomMetadata{
		RoomTitle:           meta.GetRoomTitle(),
		WelcomeMessage:      meta.WelcomeMessage,
		LogoutUrl:           meta.LogoutUrl,
		CopyrightConf:       meta.GetCopyrightConf(),
		DefaultLockSettings: meta.GetDefaultLockSettings(),
		RoomFeatures: &plugnmeet.RoomCreateFeatures{
			AllowWebcams:        rf.GetAllowWebcams(),
			MuteOnStart:         rf.GetMuteOnStart(),
			AdminOnlyWebcams:    rf.GetAdminOnlyWebcams(),
			AllowVirtualBg:      rf.AllowVirtualBg,
			WaitingRoomFeatures: rf.GetWaitingRoomFeatures(),
		},
	}

	// only expose the flags, never the encryption key or the included scopes
	if e2ee := rf.GetEndToEndEncryptionFeatures(); e2ee != nil {
		redacted.RoomFeatures.EndToEndEncryptionFeatures = &plugnmeet.EndToEndEncryptionFeatures{
			IsEnabled:                      e2ee.GetIsEnabled(),
			EnabledSelfInsertEncryptionKey: e2ee.GetEnabledSelfInsertEncryptionKey(),
		}
	}

	return s.MarshalToProtoJson(redacted)
}

// IsRoomStatusActive determines if a given room status string represents
// a state where the room is considered active (either newly created or actively running).
func (s *NatsService) IsRoomStatusActive(status string) bool {
	return status == RoomStatusCreated || status == RoomStatusActive
}
