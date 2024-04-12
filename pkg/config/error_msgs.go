package config

const (
	RequestedRoomNotExist            = "requested room does not exist"
	OnlyAdminCanRequest              = "only admin can send this request"
	NoRoomIdInToken                  = "no roomId in token"
	UserNotActive                    = "user isn't active now"
	CanNotDemotePresenter            = "can't demote current presenter"
	CanNotChangeAlternativePresenter = "can't change alternative presenter"
	CanNotPromoteToPresenter         = "can't promote to presenter"
	InvalidConsumerKey               = "invalid consumer_key"
	VerificationFailed               = "verification failed"
	UserIdOrEmailRequired            = "either value of user_id or lis_person_contact_email_primary  required"
)
