package models

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	BreakoutRoomFormat = "%s-%s"
	// DefaultWhiteboardFileId is the whiteboard file id a room gets when no
	// office file is shared. Breakout rooms that don't inherit shared content
	// reset to this value so they start with a blank board, identical to a
	// normal room created without a file.
	DefaultWhiteboardFileId = ""
	// DefaultOfficeFileSentinel is the a-client store value for the built-in
	// blank whiteboard board (whiteboard slice's currentWhiteboardOfficeFileId
	// defaults to "default"). It is a valid breakout share target: it carries no
	// uploaded-file KV entry (unlike a real office file), so the create flow
	// seeds child rooms with file_id "default" and skips the room-file lookup.
	DefaultOfficeFileSentinel = "default"
)

func (m *BreakoutRoomModel) CreateBreakoutRooms(userCtx context.Context, r *plugnmeet.CreateBreakoutRoomsReq) ([]*plugnmeet.BreakoutRoom, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":   r.RoomId,
		"method":   "CreateBreakoutRooms",
		"numRooms": len(r.Rooms),
	})
	log.Infoln("New request to create breakout rooms received")

	mainRoom, meta, err := m.natsService.GetRoomInfoWithMetadata(r.RoomId)
	if err != nil {
		log.WithError(err).Error("Failed to get parent room info")
		return nil, fmt.Errorf("failed to get parent room info")
	}

	if mainRoom == nil || meta == nil {
		err = errors.New("invalid empty parent room information")
		log.WithError(err).Error()
		return nil, err
	}

	// Step 1: do not allow nesting breakout rooms inside a breakout room.
	if meta.IsBreakoutRoom {
		err = errors.New("breakout rooms cannot be created inside another breakout room")
		log.WithError(err).Error()
		return nil, err
	}

	// enforce the allowed number of breakout rooms for this parent room.
	// AllowedNumberRooms == 0 means "not explicitly limited" (unlimited), so we
	// only reject when a positive limit is set and the request exceeds it.
	if meta.RoomFeatures != nil && meta.RoomFeatures.BreakoutRoomFeatures != nil {
		allowedRooms := meta.RoomFeatures.BreakoutRoomFeatures.AllowedNumberRooms
		if allowedRooms > 0 && uint32(len(r.Rooms)) > allowedRooms {
			err = fmt.Errorf("number of breakout rooms exceeds the allowed limit of %d", allowedRooms)
			log.WithError(err).Error()
			return nil, err
		}
	}

	// let's check if the parent room has a duration set or not
	if meta.RoomFeatures.RoomDuration != nil && *meta.RoomFeatures.RoomDuration > 0 {
		err = m.rm.CompareDurationWithParentRoom(r.RoomId, r.Duration)
		if err != nil {
			log.WithError(err).Error("Duration comparison with parent room failed")
			return nil, fmt.Errorf("duration comparison with parent room failed")
		}
	}

	// set room duration
	meta.RoomFeatures.RoomDuration = &r.Duration
	meta.IsBreakoutRoom = true
	meta.WelcomeMessage = r.WelcomeMsg
	meta.ParentRoomId = r.RoomId

	// Step 2: children can never host their own breakouts.
	if meta.RoomFeatures.BreakoutRoomFeatures != nil {
		meta.RoomFeatures.BreakoutRoomFeatures.IsActive = false
		meta.RoomFeatures.BreakoutRoomFeatures.IsAllow = false
	}

	// disable few features
	meta.RoomFeatures.WaitingRoomFeatures.IsActive = false

	// we'll disable now. in the future, we can think about those
	meta.RoomFeatures.RecordingFeatures.IsAllow = false
	meta.RoomFeatures.AllowRtmp = new(false)
	meta.RoomFeatures.ExternalBroadcastingFeatures.IsAllow = false

	// clear few main room data
	meta.RoomFeatures.DisplayExternalLinkFeatures.IsActive = false
	meta.RoomFeatures.ExternalMediaPlayerFeatures.IsActive = false

	// Step 4: validate/normalize the optional whiteboard share request.
	shareSet, childWbf, err := m.normalizeBreakoutWhiteboardShare(r, meta, log)
	if err != nil {
		log.WithError(err).Error("Failed to validate whiteboard share")
		return nil, err
	}

	// Step 3: auto-switch presenter ONLY when a validated whiteboard share is
	// active, because the later seeding flow needs the creating admin to hold the
	// presenter role (whiteboard session-data fetch is presenter-authorized).
	// If the user is already presenter, skip to avoid a redundant broadcast.
	if shareSet {
		if !m.natsService.IsUserPresenter(r.RoomId, r.RequestedUserId) {
			if switchErr := m.um.SwitchPresenter(&plugnmeet.SwitchPresenterReq{
				Task:            plugnmeet.SwitchPresenterTask_PROMOTE,
				RoomId:          r.RoomId,
				UserId:          r.RequestedUserId,
				RequestedUserId: r.RequestedUserId,
			}); switchErr != nil {
				log.WithError(switchErr).Error("failed to make requesting user presenter for whiteboard share")
				return nil, switchErr
			}
		}
	}

	// Step 5: apply the computed whiteboard features to the shared child metadata
	// (the same pointer is reused for every child room, which is fine because
	// all children inherit the identical shared content).
	meta.RoomFeatures.WhiteboardFeatures = childWbf

	// get all parent room files metadata into the breakout room bucket.
	parentFiles, _ := m.natsService.GetAllRoomFiles(r.RoomId)

	e := make(map[string]bool)
	createdRooms := make([]*plugnmeet.BreakoutRoom, 0, len(r.Rooms))

	for _, room := range r.Rooms {
		bRoomId := fmt.Sprintf(BreakoutRoomFormat, r.RoomId, room.Id)
		roomLog := log.WithFields(logrus.Fields{
			"breakoutRoomId":    bRoomId,
			"breakoutRoomTitle": room.Title,
		})

		bRoom := new(plugnmeet.CreateRoomReq)
		bRoom.RoomId = bRoomId
		meta.RoomTitle = room.Title
		bRoom.Metadata = meta
		// Step 6: capture the returned roomSid so it flows into the Redis hash,
		// the create response, and downstream listRooms/myRooms consumers.
		ari, err := m.rm.CreateRoom(userCtx, bRoom)

		if err != nil {
			roomLog.WithError(err).Error("Failed to create breakout room")
			e[bRoom.RoomId] = true
			continue
		}

		// store the canonical full breakout room id (<parentRoomId>-<n>) so the
		// stored proto and the Redis hash field agree; no client-side id rebuild needed.
		room.Id = bRoomId
		room.RoomSid = ari.GetSid()
		room.Duration = r.Duration
		room.Created = uint64(time.Now().Unix())

		marshal, err := protojson.Marshal(room)
		if err != nil {
			roomLog.WithError(err).Error("Failed to marshal breakout room data")
			e[bRoom.RoomId] = true
			continue
		}

		err = m.rs.InsertOrUpdateBreakoutRoom(r.RoomId, bRoom.RoomId, marshal)
		if err != nil {
			roomLog.WithError(err).Error("Failed to insert breakout room in nats")
			e[bRoom.RoomId] = true
			continue
		}

		createdRooms = append(createdRooms, room)

		// copy all parent room files metadata into the breakout room bucket.
		if parentFiles != nil {
			for _, pf := range parentFiles {
				if cErr := m.natsService.AddRoomFile(bRoomId, pf); cErr != nil {
					roomLog.WithError(cErr).Warn("failed to copy parent room file into breakout room")
				}
			}
		}

		// now send invitation notification
		for _, u := range room.Users {
			err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_JOIN_BREAKOUT_ROOM, r.RoomId, bRoom.RoomId, &u.Id)
			if err != nil {
				roomLog.WithError(err).WithField("userId", u.Id).Error("Failed to send breakout room invitation")
				continue
			}
		}
	}

	if len(e) == len(r.Rooms) {
		err = errors.New("breakout room creation wasn't successful for any room")
		log.WithError(err).Error()
		return nil, err
	}

	// again here for update
	origMeta, err := m.natsService.UnmarshalRoomMetadata(mainRoom.Metadata)
	if err != nil {
		log.WithError(err).Error("Failed to unmarshal original parent room metadata")
		return createdRooms, err
	}
	origMeta.RoomFeatures.BreakoutRoomFeatures.IsActive = true
	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, origMeta)
	if err != nil {
		log.WithError(err).Error("Failed to update parent room metadata")
		return createdRooms, err
	}

	// send analytics
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_BREAKOUT_ROOM,
		RoomId:    r.RoomId,
	})

	log.Info("Finished breakout rooms creation")
	return createdRooms, err
}

// normalizeBreakoutWhiteboardShare validates the optional whiteboard share
// request and returns whether a share is active plus the WhiteboardFeatures the
// child rooms should use. Shared files are referenced by id (NOT copied): the
// parent's uploaded-file metadata supplies the path/pages, and the download
// endpoint serves those paths without a room-membership check. share_notepad is
// NOT handled here (later phase).
func (m *BreakoutRoomModel) normalizeBreakoutWhiteboardShare(r *plugnmeet.CreateBreakoutRoomsReq, meta *plugnmeet.RoomMetadata, log *logrus.Entry) (bool, *plugnmeet.WhiteboardFeatures, error) {
	parentWbf := meta.GetRoomFeatures().GetWhiteboardFeatures()
	childWbf := &plugnmeet.WhiteboardFeatures{
		IsAllow: parentWbf.GetIsAllow(),
	}

	share := r.GetWhiteboardShare()
	if share == nil {
		// No share requested: blank whiteboard (same defaults as a normal room
		// created without a file).
		childWbf.WhiteboardFileId = DefaultWhiteboardFileId
		return false, childWbf, nil
	}

	// Self-insert E2EE: each participant types their own secret locally and it
	// is cleared immediately so we'll never do cross sharing
	if meta.GetRoomFeatures().GetEndToEndEncryptionFeatures().GetIsEnabled() &&
		meta.GetRoomFeatures().GetEndToEndEncryptionFeatures().GetEnabledSelfInsertEncryptionKey() {
		log.Infoln("whiteboard share ignored: self-insert E2EE is enabled")
		childWbf.WhiteboardFileId = DefaultWhiteboardFileId
		return false, childWbf, nil
	}

	if share.FileId == "" {
		log.Infoln("whiteboard share has no file id, treating as a blank whiteboard create")
		childWbf.WhiteboardFileId = DefaultWhiteboardFileId
		return false, childWbf, nil
	}

	// The built-in "default" board is a valid share target. It is the blank
	// board every room starts with and carries NO uploaded-file KV entry, so
	// there is nothing to look up in the parent's room-file bucket. Seed the
	// child directly with file_id "default" and the requested page range. A
	// default-board share counts as a validated share (presenter auto-switch +
	// content seeding) exactly like a real office file.
	if share.FileId == DefaultOfficeFileSentinel {
		// filePageCount=0 disables the per-file page clamp: the client already
		// scopes the selected pages to the default board's total, and the server
		// has no separate page-count source for the built-in board.
		pages := normalizeSelectedPages(share.Pages, 0)
		maxPage := uint32(0)
		for _, p := range pages {
			if p > maxPage {
				maxPage = p
			}
		}
		// A board always has at least one page; never leave the child with a
		// 0-page whiteboard when no explicit page was selected.
		if maxPage == 0 {
			maxPage = 1
		}
		// Clamp current_page into the selected range (same as the office path;
		// no persistence/response field for it yet — kept for the response/echo).
		if share.CurrentPage > maxPage {
			share.CurrentPage = maxPage
		}
		if share.CurrentPage < 1 {
			share.CurrentPage = 1
		}
		childWbf.WhiteboardFileId = DefaultOfficeFileSentinel
		childWbf.FileName = ""
		childWbf.FilePath = ""
		// The breakout shows only the shared page range, so total_pages is the
		// highest selected page (1 when nothing is explicitly selected).
		childWbf.TotalPages = maxPage
		return true, childWbf, nil
	}

	// The shared file must exist in the PARENT room's file KV bucket.
	fileMeta, err := m.natsService.GetRoomFile(r.RoomId, share.FileId)
	if err != nil {
		return false, nil, fmt.Errorf("failed to read shared whiteboard file from parent room: %w", err)
	}
	if fileMeta == nil {
		return false, nil, fmt.Errorf("shared whiteboard file %q not found in parent room", share.FileId)
	}

	// Normalize the requested page range: drop values < 1, dedupe, sort ascending,
	// and drop any page beyond the file's known page count. Keep ORIGINAL page
	// numbering (never remap indices).
	pages := normalizeSelectedPages(share.Pages, fileMeta.GetTotalPages())
	if len(pages) == 0 {
		// treat as all pages
		if fileMeta.TotalPages != nil && *fileMeta.TotalPages > 0 {
			pages = make([]uint32, *fileMeta.TotalPages)
			for i := range pages {
				pages[i] = uint32(i + 1)
			}
		}
	}

	maxPage := uint32(0)
	for _, p := range pages {
		if p > maxPage {
			maxPage = p
		}
	}
	if fileMeta.TotalPages != nil && *fileMeta.TotalPages > 0 && maxPage == 0 {
		maxPage = uint32(*fileMeta.TotalPages)
	}

	// A file always has at least one page; never leave the child with a 0-page
	// whiteboard (unknown page count + no explicit selection).
	if maxPage == 0 {
		maxPage = 1
	}

	// Clamp current_page into the selected range. There is no persistence/response
	// field for it yet (later seeding phase); just keep the normalized value
	// available on the request for the response/echo.
	if share.CurrentPage > maxPage {
		share.CurrentPage = maxPage
	}
	if share.CurrentPage < 1 {
		share.CurrentPage = 1
	}

	childWbf.WhiteboardFileId = fileMeta.FileId
	childWbf.FileName = fileMeta.FileName
	childWbf.FilePath = fileMeta.FilePath
	// The breakout shows only the shared page range, so total_pages is the highest
	// selected page (max page count if all pages are shared), NOT the parent's full total.
	childWbf.TotalPages = maxPage

	return true, childWbf, nil
}

// normalizeSelectedPages returns the requested page numbers cleaned up for use by
// a breakout room: values < 1 dropped, duplicates removed, sorted ascending, and
// any page beyond filePageCount (when > 0) dropped. Original page numbering is
// preserved (never remapped).
func normalizeSelectedPages(requested []uint32, filePageCount int32) []uint32 {
	seen := make(map[uint32]struct{})
	result := make([]uint32, 0, len(requested))
	for _, p := range requested {
		if p < 1 {
			continue
		}
		if filePageCount > 0 && int32(p) > filePageCount {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (m *BreakoutRoomModel) PostTaskAfterRoomStartWebhook(roomId string, metadata *plugnmeet.RoomMetadata) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":       roomId,
		"parentRoomId": metadata.ParentRoomId,
		"method":       "PostTaskAfterRoomStartWebhook",
	})
	log.Info("Handling post-start tasks for breakout room")

	// now in livekit rooms are created almost instantly & sending webhook response
	// if this happened then we'll have to wait few seconds otherwise room info can't be found
	time.Sleep(config.WaitBeforeBreakoutRoomOnAfterRoomStart)

	room, err := m.fetchBreakoutRoom(metadata.ParentRoomId, roomId)
	if err != nil {
		log.WithError(err).Error("Failed to fetch breakout room info")
		return err
	}
	room.Created = metadata.StartedAt
	room.Started = true

	marshal, err := protojson.Marshal(room)
	if err != nil {
		log.WithError(err).Error("Failed to marshal breakout room data")
		return err
	}

	err = m.rs.InsertOrUpdateBreakoutRoom(metadata.ParentRoomId, roomId, marshal)
	if err != nil {
		log.WithError(err).Error("Failed to update breakout room info in nats")
		return err
	}

	log.Info("Successfully handled post-start tasks for breakout room")
	return nil
}
