package config

import "errors"

var (
	UserNotActive                  = errors.New("user isn't active now")
	InvalidConsumerKey             = errors.New("invalid consumer_key")
	VerificationFailed             = errors.New("verification failed")
	UserIdOrEmailRequired          = errors.New("either value of user_id or lis_person_contact_email_primary  required")
	NoOnlineUserFound              = errors.New("no online user found")
	NoOnlineAdminFound             = errors.New("no online admin user found")
	NotFoundErr                    = errors.New("not found")
	ErrRoomNotFound                = errors.New("room not found")
	ErrRecordingNotFound           = errors.New("recording not found")
	ErrFileNotFound                = errors.New("file not found")
	ErrConversionTimeout           = errors.New("file conversion timeout reached, process will continue in background")
	NoBreakoutRoomsFound           = errors.New("no breakout rooms found")
	InvalidNilRoomMetadata         = errors.New("invalid nil room metadata information")
	ErrRequestedRecordingsNotFound = errors.New("one or more of the requested recordings were not found or did not match the room_id")
)
