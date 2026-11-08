package config

import "errors"

// This file holds every client-facing API message. Their values are i18n keys
// that the client resolves via t(), never raw operational error text. Models log
// operational errors and return the matching generic sentinel of their block.
// Curated English text for the Auth API, LTI and BBB integrations lives in
// auth_error_msgs.go.

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
	ErrPollTextAnswerRequired     = errors.New("polls.errors.text-answer-required")
	ErrPollTextAnswerTooLong      = errors.New("polls.errors.text-answer-too-long")
	ErrPollAlreadyVoted           = errors.New("polls.errors.already-voted")
	ErrPollWaitForClose           = errors.New("polls.errors.wait-for-close")
	ErrPollOnlyAdmin              = errors.New("polls.errors.only-admin")
	ErrPollOnlyAdminViewSelection = errors.New("polls.errors.only-admin-view-selection")
	ErrPollRoomIdRequired         = errors.New("polls.errors.room-id-required")
	ErrPollPollIdRequired         = errors.New("polls.errors.poll-id-required")
	ErrPollUserAndPollIdRequired  = errors.New("polls.errors.user-and-poll-id-required")
	ErrPollGeneric                = errors.New("polls.errors.generic")
)

// Countdown-timer errors are returned to clients; their values are i18n keys
// the client resolves via t(), never raw operational error text.
var (
	ErrCountdownTimerOnlyAdmin       = errors.New("countdown-timer.errors.only-admin")
	ErrCountdownTimerNotAllowed      = errors.New("countdown-timer.errors.not-allowed")
	ErrCountdownTimerNotActive       = errors.New("countdown-timer.errors.not-active")
	ErrCountdownTimerInvalidDuration = errors.New("countdown-timer.errors.invalid-duration")
	ErrCountdownTimerNotRunning      = errors.New("countdown-timer.errors.not-running")
	ErrCountdownTimerNotPaused       = errors.New("countdown-timer.errors.not-paused")
	ErrCountdownTimerGeneric         = errors.New("countdown-timer.errors.generic")
)

// Room errors are returned to clients by the room endpoints (/api and
// /auth/room/*); their values are i18n keys the client resolves via t(), never
// raw operational error text. Models log operational errors and return
// ErrRoomUnexpectedError for them.
var (
	ErrRoomOnlyAdmin         = errors.New("room.errors.only-admin")
	ErrRoomInvalidRequest    = errors.New("room.errors.invalid-request")
	ErrRoomIdRequired        = errors.New("room.errors.room-id-required")
	ErrRoomIdMismatch        = errors.New("room.errors.room-id-mismatch")
	ErrRoomNotFound          = errors.New("room.errors.room-not-found")
	ErrRoomNotActive         = errors.New("room.errors.room-not-active")
	ErrRoomUnexpectedError   = errors.New("room.errors.unexpected")
	ErrRoomEndFailed         = errors.New("room.errors.end-failed")
	ErrRoomFeatureNotAllowed = errors.New("room.errors.feature-not-allowed")
	ErrRoomSipDialInEnabled  = errors.New("room.errors.sip-dial-in-already-enabled")
	ErrRoomIngressExists     = errors.New("room.errors.ingress-already-created")
	ErrRoomInvalidUrl        = errors.New("room.errors.invalid-url")
	ErrRoomUserNotFound      = errors.New("room.errors.user-not-found")
	ErrRoomIdNotAllowed      = errors.New("room.errors.room-id-not-allowed")
	ErrRoomPreassignedLimit  = errors.New("room.errors.preassigned-limit")
	ErrRoomPreassignedUser   = errors.New("room.errors.preassigned-user")
)

// User and join-token errors are returned to clients; their values are i18n keys
// the client resolves via t(), never raw operational error text. Models log
// operational errors and return ErrUserUnexpectedError for them.
var (
	ErrUserOnlyAdmin               = errors.New("user.errors.only-admin")
	ErrUserInvalidRequest          = errors.New("user.errors.invalid-request")
	ErrUserInfoRequired            = errors.New("user.errors.user-info-required")
	ErrUserRoomIdMismatch          = errors.New("user.errors.room-id-mismatch")
	ErrUserRoomSidMismatch         = errors.New("user.errors.room-sid-mismatch")
	ErrUserRoomNotRunning          = errors.New("user.errors.room-not-running")
	ErrUserCannotRemoveSelf        = errors.New("user.errors.cannot-remove-self")
	ErrUserNotOnline               = errors.New("user.errors.user-not-online")
	ErrUserNotFound                = errors.New("user.errors.user-not-found")
	ErrUserCannotLockAdmin         = errors.New("notifications.cannot-lock-admin")
	ErrUserFailedToLoadParticipant = errors.New("footer.notice.failed-to-load-participant-info")
	ErrUserNoSuitableTrack         = errors.New("footer.notice.no-suitable-track-found")
	ErrUserMuteUnmuteFailed        = errors.New("footer.notice.failed-to-mute-unmute-track")
	ErrUserUnexpectedError         = errors.New("user.errors.unexpected")
	ErrUserReservedName            = errors.New("user.errors.reserved-name")
	ErrUserReservedUserId          = errors.New("user.errors.reserved-user-id")
	ErrUserInvalidUserId           = errors.New("user.errors.invalid-user-id")
	ErrUserRoomInfoNotFound        = errors.New("user.errors.room-info-not-found")
	ErrUserRoomEnded               = errors.New("user.errors.room-ended")
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

// Auth errors of the browser facing endpoints are returned to clients (HTTP body
// and NATS auth callout error) as i18n keys the client resolves via t(), never as
// English text. Operational errors are logged at the failure site and mapped to
// ErrAuthUnexpected. The Auth API of the integrators uses the MsgAuth* messages
// of auth_error_msgs.go instead.
var (
	ErrAuthUnexpected       = errors.New("notifications.error-occurred")
	ErrAuthHeaderMissing    = errors.New("notifications.auth-header-missing")
	ErrAuthTokenInvalid     = errors.New("notifications.invalid-token")
	ErrAuthTokenExpired     = errors.New("notifications.token-expired")
	ErrAuthInvalidRequest   = errors.New("notifications.invalid-request")
	ErrAuthRoomNotActive    = errors.New("notifications.room-not-active")
	ErrAuthUserBlocked      = errors.New("notifications.you-are-blocked")
	ErrAuthUserNotActive    = errors.New("notifications.user-not-active")
	ErrAuthUserWaitApproval = errors.New("notifications.waiting-for-approval")
	ErrAuthUserInfoNotFound = errors.New("notifications.user-info-not-found")
	ErrAuthDuplicateEntry   = errors.New("notifications.room-disconnected-duplicate-entry")
	ErrAuthUserRemoved      = errors.New("notifications.room-disconnected-participant-removed")
	ErrAuthMaxParticipants  = errors.New("notifications.max-num-participates-exceeded")
)

// File errors are returned to browser clients; their values are i18n keys the
// client resolves via t(), never raw operational error text. Models log
// operational errors and return ErrFileUnexpected for them.
var (
	ErrFileInvalidRequest = errors.New("file.errors.invalid-request")
	ErrFileMissingFile    = errors.New("file.errors.missing-file")
	ErrFileTooLarge       = errors.New("file.errors.too-large")
	ErrFileTypeNotAllowed = errors.New("file.errors.type-not-allowed")
	ErrFileChunksNotFound = errors.New("file.errors.chunks-not-found")
	ErrFileUploadFailed   = errors.New("file.errors.upload-failed")
	ErrFileMergeFailed    = errors.New("file.errors.merge-failed")
	ErrFilePathRequired   = errors.New("file.errors.file-path-required")
	ErrFileInvalidPath    = errors.New("file.errors.invalid-path")
	ErrFilePathNotFound   = errors.New("file.errors.not-found")
	ErrFileOnlyPresenter  = errors.New("file.errors.only-presenter")
	ErrFileUnexpected     = errors.New("file.errors.unexpected")
)

// Recording errors are returned to browser clients by the /api recording and
// rtmp endpoints; their values are i18n keys the client resolves via t(), never
// raw operational error text. Models log operational failures and return
// ErrRecordingOperationFailed for them.
var (
	ErrRecordingInvalidRequest     = errors.New("recording.errors.invalid-request")
	ErrRecordingTaskInProgress     = errors.New("recording.errors.task-in-progress")
	ErrRecordingRoomSidRequired    = errors.New("recording.errors.room-sid-required")
	ErrRecordingRtmpUrlRequired    = errors.New("recording.errors.rtmp-url-required")
	ErrRecordingInvalidRtmpUrl     = errors.New("recording.errors.invalid-rtmp-url")
	ErrRecordingOperationFailed    = errors.New("recording.errors.operation-failed")
	ErrRecordingNoRecorder         = errors.New("notifications.no-recorder-available")
	ErrRecordingAlreadyRunning     = errors.New("notifications.recording-already-running")
	ErrRecordingNotRunning         = errors.New("notifications.recording-not-running")
	ErrRecordingRtmpAlreadyRunning = errors.New("notifications.rtmp-already-running")
	ErrRecordingRtmpNotRunning     = errors.New("notifications.rtmp-not-running")
)

// MsgSuccess is the generic success message of the response envelopes. It is an
// i18n key the client resolves via t(), never English text.
const MsgSuccess = "notifications.success"

// AuthMsgTokenValid is the success message of the token verification response.
const AuthMsgTokenValid = "notifications.token-valid"

// MsgFileConversionStarted is the notice returned when a whiteboard file
// conversion did not finish within the request timeout and keeps running in the
// background. It is an i18n key the client resolves via t().
const MsgFileConversionStarted = "file.notifications.conversion-started"
