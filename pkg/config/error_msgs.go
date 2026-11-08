package config

import "errors"

// This file holds the internal control sentinels which are compared with
// errors.Is and whose text is never sent to a caller. Client-facing i18n keys
// live in api_error_msgs.go, the curated English text of the Auth API, LTI and BBB
// integrations in auth_error_msgs.go.
var (
	NotFoundErr                    = errors.New("not found")
	ErrRecordingRoomNotFound       = errors.New("room not found")
	ErrRecordingNotFound           = errors.New("recording not found")
	ErrRequestedRecordingsNotFound = errors.New("one or more of the requested recordings were not found or did not match the room_id")
	NoOnlineUserFound              = errors.New("no online user found")
	NoOnlineAdminFound             = errors.New("no online admin user found")
	InvalidNilRoomMetadata         = errors.New("invalid nil room metadata information")
	// ErrFileNotFound, ErrInvalidFilePath and ErrConversionTimeout are internal
	// control sentinels compared with errors.Is; their text is never sent to clients.
	ErrFileNotFound      = errors.New("file not found")
	ErrConversionTimeout = errors.New("file conversion timeout reached, process will continue in background")
	ErrInvalidFilePath   = errors.New("invalid file path")
)
