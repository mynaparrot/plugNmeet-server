package config

import "errors"

// This file holds the internal control sentinels which are compared with
// errors.Is. NotFoundErr and InvalidNilRoomMetadata are internal and their text
// is never sent to a caller; the other sentinels double as client-facing i18n
// keys, so their text may reach a response body and must stay a translation key.
// Curated English text of the Auth API, LTI and BBB integrations lives in
// auth_error_msgs.go.
var (
	NotFoundErr                    = errors.New("not found")
	ErrRecordingRoomNotFound       = errors.New("room.errors.room-not-found")
	ErrRecordingNotFound           = errors.New("recording.errors.not-found")
	ErrRequestedRecordingsNotFound = errors.New("recording.errors.requested-not-found")
	NoOnlineUserFound              = errors.New("room.errors.no-online-user")
	NoOnlineAdminFound             = errors.New("room.errors.no-online-admin")
	InvalidNilRoomMetadata         = errors.New("invalid nil room metadata information")
	// ErrFileNotFound, ErrInvalidFilePath and ErrConversionTimeout are internal
	// control sentinels compared with errors.Is; their text is never sent to clients.
	ErrFileNotFound      = errors.New("file not found")
	ErrConversionTimeout = errors.New("file conversion timeout reached, process will continue in background")
	ErrInvalidFilePath   = errors.New("invalid file path")
)
