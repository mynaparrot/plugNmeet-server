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

// Poll errors are returned to clients; their values are i18n keys the client
// resolves via t(), never raw operational error text. Models log operational
// errors and return ErrPollGeneric for them.
var (
	ErrPollNotFound               = errors.New("polls.errors.poll-not-found")
	ErrPollClosed                 = errors.New("polls.errors.poll-closed")
	ErrPollQuestionRequired       = errors.New("polls.errors.question-required")
	ErrPollMinOptions             = errors.New("polls.errors.min-options")
	ErrPollOptionRequired         = errors.New("polls.errors.option-required")
	ErrPollQuizNeedsCorrect       = errors.New("polls.errors.quiz-needs-correct")
	ErrPollDurationCap            = errors.New("polls.errors.duration-cap")
	ErrPollNoOptionSelected       = errors.New("polls.errors.no-option-selected")
	ErrPollAlreadyVoted           = errors.New("polls.errors.already-voted")
	ErrPollWaitForClose           = errors.New("polls.errors.wait-for-close")
	ErrPollOnlyAdmin              = errors.New("polls.errors.only-admin")
	ErrPollOnlyAdminViewSelection = errors.New("polls.errors.only-admin-view-selection")
	ErrPollRoomIdRequired         = errors.New("polls.errors.room-id-required")
	ErrPollPollIdRequired         = errors.New("polls.errors.poll-id-required")
	ErrPollUserAndPollIdRequired  = errors.New("polls.errors.user-and-poll-id-required")
	ErrPollGeneric                = errors.New("polls.errors.generic")
)

// Breakout-room errors are returned to clients; their values are i18n keys the
// client resolves via t(), never raw operational error text. Models log
// operational errors and return ErrBkRoomUnexpectedError for them.
var (
	ErrBkRoomOnlyAdmin                   = errors.New("breakout-room.notifications.only-admin")
	ErrBkRoomUnexpectedError             = errors.New("breakout-room.notifications.unexpected-error")
	ErrBkRoomNotFound                    = errors.New("breakout-room.notifications.room-not-found")
	ErrBkRoomMainRoomNotActive           = errors.New("breakout-room.notifications.main-room-not-active")
	ErrBkRoomNotRunning                  = errors.New("breakout-room.notifications.breakout-room-not-running")
	ErrBkRoomUnlimitedDuration           = errors.New("breakout-room.notifications.breakout-room-unlimited-duration")
	ErrBkRoomDurationExceedsParent       = errors.New("breakout-room.notifications.duration-exceeds-parent")
	ErrBkRoomCannotCreateInsideBreakout  = errors.New("breakout-room.notifications.cannot-create-inside-breakout")
	ErrBkRoomMaxRoomsExceeded            = errors.New("breakout-room.notifications.max-rooms-exceeded")
	ErrBkRoomDuplicateTitle              = errors.New("breakout-room.notifications.duplicate-title")
	ErrBkRoomUserAssignedToMultipleRooms = errors.New("breakout-room.notifications.user-assigned-to-multiple-rooms")
	ErrBkRoomCreationFailed              = errors.New("breakout-room.notifications.creation-failed")
	ErrBkRoomAlreadyJoined               = errors.New("breakout-room.notifications.user-already-joined")
	ErrBkRoomUserNotAllowedJoin          = errors.New("breakout-room.notifications.user-not-allowed-join")
	ErrBkRoomUserNotAssigned             = errors.New("breakout-room.notifications.user-not-assigned")
	ErrBkRoomUserNotOnline               = errors.New("breakout-room.notifications.user-not-online")
	ErrBkRoomUserAlreadyInMain           = errors.New("breakout-room.notifications.user-already-in-main")
	ErrBkRoomUserAlreadyInRoom           = errors.New("breakout-room.notifications.user-already-in-room")
)
