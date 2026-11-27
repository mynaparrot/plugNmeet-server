package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// Backend interfaces the tools depend on. They live here next to
// ToolDependencies so the package's contract with pkg/models and
// pkg/services/redis stays in one place.

type PollBackend interface {
	// CreatePoll opens a choice poll with the given options, or a free-text
	// poll when isTextAnswer is true (options are then ignored).
	CreatePoll(ctx context.Context, roomID, question string, options []string, isTextAnswer bool) (pollID string, err error)
	// CreateQuiz opens the same poll shape as a quiz: the correct option is
	// marked server-side before the poll is broadcast, so the board can reveal
	// the answer when the poll closes.
	CreateQuiz(ctx context.Context, roomID, question string, options []string, correctOptionIndex int) (pollID string, err error)
	ClosePoll(ctx context.Context, roomID, pollID string) error
	// GetPollResults lists the room's polls when pollID is empty and returns
	// one poll's detailed results otherwise. The concrete shape is PollResult
	// or []PollResult, ready to be encoded for the model.
	GetPollResults(ctx context.Context, roomID, pollID string) (any, error)
}

type ChatBackend interface {
	SendChatMessage(ctx context.Context, roomID, toUserID, msg string) error
}

type RoomInfoBackend interface {
	GetRoomSnapshot(ctx context.Context, roomID string) (*RoomSnapshot, error)
	SetWorkspaceLock(ctx context.Context, roomID, surface string, locked bool, userID string) error
	// SetWorkspaceVisibility shows or hides a surface without changing its lock,
	// so the agent can open or close the whiteboard or shared notepad.
	SetWorkspaceVisibility(ctx context.Context, roomID, surface string, visible bool) error
}

// NotepadBackend opens the room's shared notepad, so `notepad_write` and
// `notepad_read` can land even when nobody has opened it yet. It never unlocks
// the notepad: the participant stays in view-only mode until the agent unlocks it.
type NotepadBackend interface {
	EnsureNotepadActive(ctx context.Context, roomID string) error
}

// WhiteboardBackend brings the whiteboard into view, so whiteboard calls land
// on a surface the participant can actually see. It never unlocks the board.
type WhiteboardBackend interface {
	EnsureWhiteboardVisible(ctx context.Context, roomID string) error
}

// BreakoutBackend creates and manages the room's breakout rooms.
type BreakoutBackend interface {
	// CreateBreakoutRooms creates one child room per title and distributes the
	// online participants across them. It returns how many rooms were created.
	CreateBreakoutRooms(ctx context.Context, roomID, requestedUserID string, durationMinutes uint64, welcomeMsg string, titles []string) (int, error)
	// ManageBreakoutRooms runs one of list, broadcast_message,
	// extend_duration, end_room or end_all. Only list returns a value.
	ManageBreakoutRooms(ctx context.Context, roomID, action, breakoutRoomID, message string, durationMinutes uint64) (any, error)
}

// InsightsBackend starts and stops the room's insights services on behalf of
// the requester, who must be an administrator of the room.
type InsightsBackend interface {
	ConfigureSpeechToText(ctx context.Context, roomID, action string, spokenLangs, transLangs, allowedUserIDs []string, enableSynthesis bool) error
	ConfigureChatTranslation(ctx context.Context, roomID, action string, transLangs []string, defaultLang string) error
	ConfigureMeetingSummarization(ctx context.Context, roomID, action, customPrompt string) error
}

// RoomOpsBackend drives the room-wide tools around the timer, the external
// media surfaces and the recording.
type RoomOpsBackend interface {
	ManageCountdownTimer(ctx context.Context, roomID, userID, action string, durationSeconds uint64) error
	ManageExternalMedia(ctx context.Context, roomID, userID, action, mediaType, rawURL string) error
	ManageRecording(ctx context.Context, roomID, action string) error
}

// ParticipantsBackend moderates the room's participants.
type ParticipantsBackend interface {
	ManageParticipants(ctx context.Context, roomID, requestedUserID, action, targetUserID, lockService string, locked bool) error
}

// ExternalURLHookRunner lets a deployment vet the external knowledge and MCP URLs b-server is about to dial,
// rewrite them to a proxy, or answer with the document itself.
type ExternalURLHookRunner func(ctx context.Context, req *hooks.ExternalUrlHookData) (*hooks.ExternalUrlHookData, error)

// NewExternalURLHookRunner adapts the hook configuration to a runner. It returns nil when no external URL hook is configured,
// so callers never branch on the script setup themselves.
func NewExternalURLHookRunner(cfg *hooks.Config, log *logrus.Entry) ExternalURLHookRunner {
	if !cfg.HasExternalUrlHook() {
		return nil
	}
	return func(_ context.Context, req *hooks.ExternalUrlHookData) (*hooks.ExternalUrlHookData, error) {
		return cfg.RunExternalUrlHook(req, log)
	}
}

// CacheStore caches fetched knowledge documents.
type CacheStore interface {
	SetAIKnowledgeDocument(ctx context.Context, roomID, rawURL, value string, ttl time.Duration) error
	GetAIKnowledgeDocument(ctx context.Context, roomID, rawURL string) (value string, found bool, err error)
}

// QuizStore keeps the answer key of a quiz next to the poll it belongs to.
type QuizStore interface {
	SaveAIQuizMetadata(ctx context.Context, roomID, pollID string, correctOptionIndex int, explanation string) error
	GetAIQuizMetadata(ctx context.Context, roomID, pollID string) (correctOptionIndex int, explanation string, found bool, err error)
}

// QuizDraft is a quiz preview stored between draft_quiz and publish_quiz.
type QuizDraft = insights.AIQuizDraft

// QuizDraftStore keeps the quiz previews of a room. Drafts live for minutes,
// never for the session, so the in-process fallback is a legitimate store.
type QuizDraftStore interface {
	SaveAIQuizDraft(ctx context.Context, roomID, draftID string, draft *QuizDraft) error
	GetAIQuizDraft(ctx context.Context, roomID, draftID string) (draft *QuizDraft, found bool, err error)
	DeleteAIQuizDraft(ctx context.Context, roomID, draftID string) error
}

// memoryQuizDraftStore is the fallback draft store used when the wiring layer
// did not provide one. Entries expire on read, so a forgotten draft cannot
// live longer than quizDraftMemoryTTL.
type memoryQuizDraftStore struct {
	mu     sync.Mutex
	drafts map[string]memoryQuizDraft
}

type memoryQuizDraft struct {
	draft     *QuizDraft
	expiresAt time.Time
}

const quizDraftMemoryTTL = 30 * time.Minute

var defaultQuizDrafts = &memoryQuizDraftStore{drafts: map[string]memoryQuizDraft{}}

func quizDraftMemoryKey(roomID, draftID string) string { return roomID + "\x00" + draftID }

func (s *memoryQuizDraftStore) SaveAIQuizDraft(_ context.Context, roomID, draftID string, draft *QuizDraft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drafts == nil {
		s.drafts = map[string]memoryQuizDraft{}
	}
	s.drafts[quizDraftMemoryKey(roomID, draftID)] = memoryQuizDraft{draft: draft, expiresAt: time.Now().Add(quizDraftMemoryTTL)}
	return nil
}

func (s *memoryQuizDraftStore) GetAIQuizDraft(_ context.Context, roomID, draftID string) (*QuizDraft, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := quizDraftMemoryKey(roomID, draftID)
	entry, ok := s.drafts[key]
	if !ok {
		return nil, false, nil
	}
	if time.Now().After(entry.expiresAt) {
		delete(s.drafts, key)
		return nil, false, nil
	}
	return entry.draft, true, nil
}

func (s *memoryQuizDraftStore) DeleteAIQuizDraft(_ context.Context, roomID, draftID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.drafts, quizDraftMemoryKey(roomID, draftID))
	return nil
}

// newQuizDraftID returns an unguessable draft id.
func newQuizDraftID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "draft-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "draft-" + hex.EncodeToString(buf)
}

// PollResult is one poll (or quiz) as the model sees it: options with their
// vote counts, never the individual respondents.
type PollResult struct {
	PollID           string             `json:"poll_id"`
	Question         string             `json:"question"`
	IsQuiz           bool               `json:"is_quiz"`
	IsRunning        bool               `json:"is_running"`
	IsAnonymous      bool               `json:"is_anonymous,omitempty"`
	IsTextAnswer     bool               `json:"is_text_answer,omitempty"`
	TotalRespondents uint64             `json:"total_respondents"`
	Options          []PollResultOption `json:"options,omitempty"`
	TextAnswers      []PollResultText   `json:"text_answers,omitempty"`
}

// PollResultOption is one option with its aggregate vote count.
type PollResultOption struct {
	ID        uint32 `json:"id"`
	Text      string `json:"text"`
	IsCorrect bool   `json:"is_correct,omitempty"`
	Votes     uint64 `json:"votes"`
}

// PollResultText is one aggregated text answer.
type PollResultText struct {
	Text  string `json:"text"`
	Count uint64 `json:"count"`
}

// ProgressEntry is one concept the participant worked on.
type ProgressEntry = redisservice.AIProgressEntry

// ProgressStore keeps the session's concept mastery.
type ProgressStore interface {
	RecordAILearningProgress(ctx context.Context, roomID string, entry redisservice.AIProgressEntry) error
}

// redisservice.RedisService is the production implementation of the stores.
var (
	_ CacheStore     = (*redisservice.RedisService)(nil)
	_ QuizStore      = (*redisservice.RedisService)(nil)
	_ QuizDraftStore = (*redisservice.RedisService)(nil)
	_ ProgressStore  = (*redisservice.RedisService)(nil)
	_ QuizDraftStore = (*memoryQuizDraftStore)(nil)
)

// ToolDependencies wires the backends the tools need. pkg/models passes
// redisservice.RedisService and its own adapters; this package only knows the
// interfaces, which keeps it free of an import cycle with pkg/models.
type ToolDependencies struct {
	Poll     PollBackend
	Quiz     QuizStore
	Chat     ChatBackend
	Room     RoomInfoBackend
	Progress ProgressStore
	// QuizDrafts keeps quiz previews between draft_quiz and publish_quiz; nil
	// uses the in-process fallback store.
	QuizDrafts QuizDraftStore
	// Notepad opens the room's shared notepad before a write is delegated to the
	// browser; nil leaves the notepad exactly as the room configured it.
	Notepad NotepadBackend
	// Whiteboard brings the whiteboard into view before a whiteboard call is
	// delegated to the browser; nil leaves the board as the room configured it.
	Whiteboard WhiteboardBackend
	// Cache stores fetched knowledge documents; nil disables caching.
	Cache CacheStore
	// Breakout, Insights, RoomOps and Participants are the room-administration
	// backends. They stay nil outside the tool wiring (e.g. in unit tests), so
	// the tools that need them are only registered when they are present.
	Breakout     BreakoutBackend
	Insights     InsightsBackend
	RoomOps      RoomOpsBackend
	Participants ParticipantsBackend

	// HTTPClient fetches external knowledge. When nil a client with the SSRF
	// guarded dialer is created per registry. External MCP calls reuse it as the
	// base client, so tests can inject one.
	HTTPClient *http.Client
	// AllowInsecureHTTP relaxes the HTTPS-only rule; tests only.
	AllowInsecureHTTP bool
	// ExternalURLHook vets, proxies or answers the knowledge and MCP URLs before
	// anything is dialled; nil keeps the built-in checks only.
	ExternalURLHook ExternalURLHookRunner

	// Client runs the tools that are delegated to a browser; nil disables the
	// client tools.
	Client ClientRPCSender

	// MCPScope is the consumer scope of the session (ai_agent, text_chat). Only
	// the external MCP servers whose own scope matches it, plus the shared ones,
	// are registered. Empty accepts every server.
	MCPScope string
	// RequireQuizApproval replaces the direct create_quiz tool with
	// draft_quiz/publish_quiz, so a quiz only reaches the room after the host
	// approved the preview. AI text chat sets it.
	RequireQuizApproval bool

	// ExtraTools lets the caller register tools defined outside this package,
	// e.g. the boundary guard's inert sentinel tool.
	ExtraTools []*Tool

	// RedisService enables the room's pre-warmed external MCP tools; nil
	// disables them. RoomID scopes their cache keys.
	RedisService *redisservice.RedisService
	RoomID       string

	Logger *logrus.Entry
}

func (d ToolDependencies) logger() *logrus.Entry {
	if d.Logger != nil {
		return d.Logger
	}
	return logrus.NewEntry(logrus.StandardLogger())
}

// quizDrafts returns the configured draft store or the in-process fallback.
func (d ToolDependencies) quizDrafts() QuizDraftStore {
	if d.QuizDrafts != nil {
		return d.QuizDrafts
	}
	return defaultQuizDrafts
}
