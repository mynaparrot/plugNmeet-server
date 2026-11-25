package models

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

// memoryJetStream serves and stores the key-value entries of one room bucket.
type memoryJetStream struct {
	jetstream.JetStream
	mu       sync.Mutex
	entries  map[string][]byte
	revision atomic.Uint64
}

// value returns the stored value for key.
func (j *memoryJetStream) value(key string) ([]byte, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	value, ok := j.entries[key]
	return value, ok
}

func (j *memoryJetStream) KeyValue(context.Context, string) (jetstream.KeyValue, error) {
	return memoryKV{js: j}, nil
}

func (j *memoryJetStream) CreateOrUpdateKeyValue(context.Context, jetstream.KeyValueConfig) (jetstream.KeyValue, error) {
	return memoryKV{js: j}, nil
}

func (j *memoryJetStream) Publish(context.Context, string, []byte, ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	return &jetstream.PubAck{}, nil
}

type memoryKV struct {
	jetstream.KeyValue
	js *memoryJetStream
}

func (kv memoryKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	value, ok := kv.js.value(key)
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return memoryEntry{raw: value}, nil
}

func (kv memoryKV) PutString(_ context.Context, key, value string) (uint64, error) {
	js := kv.js
	js.mu.Lock()
	defer js.mu.Unlock()

	js.entries[key] = []byte(value)
	return js.revision.Add(1), nil
}

// WatchAll returns a watcher that closes only when the context is canceled or
// Stop is called, so the room cache keeps its entries for the test run.
func (kv memoryKV) WatchAll(ctx context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	watcher := &openKeyWatcher{
		updates: make(chan jetstream.KeyValueEntry),
		done:    make(chan struct{}),
	}
	go func() {
		defer close(watcher.updates)
		select {
		case <-ctx.Done():
		case <-watcher.done:
		}
	}()

	return watcher, nil
}

type openKeyWatcher struct {
	jetstream.KeyWatcher
	updates chan jetstream.KeyValueEntry
	done    chan struct{}
	once    sync.Once
}

func (w *openKeyWatcher) Updates() <-chan jetstream.KeyValueEntry { return w.updates }

func (w *openKeyWatcher) Stop() error {
	w.once.Do(func() { close(w.done) })
	return nil
}

// memoryJetStreamContext is canceled when the test ends, which stops the room
// watchers of the NATS service.
func memoryJetStreamContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	return ctx
}

func (kv memoryKV) ListKeys(context.Context, ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	kv.js.mu.Lock()
	keys := make(chan string, len(kv.js.entries))
	for key := range kv.js.entries {
		keys <- key
	}
	kv.js.mu.Unlock()
	close(keys)

	return memoryKeyLister{keys: keys}, nil
}

type memoryKeyLister struct {
	keys <-chan string
}

func (l memoryKeyLister) Keys() <-chan string { return l.keys }
func (l memoryKeyLister) Stop() error         { return nil }

type memoryEntry struct {
	jetstream.KeyValueEntry
	raw []byte
}

func (e memoryEntry) Value() []byte { return e.raw }

// newWorkspaceUnlockTestModel returns a model whose room metadata lives in
// memory, so the workspace unlock can be inspected after a stop.
func newWorkspaceUnlockTestModel(t *testing.T, meta *plugnmeet.RoomMetadata) (*AIAgentModel, *memoryJetStream) {
	t.Helper()

	raw, err := protojson.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal room metadata: %v", err)
	}
	js := &memoryJetStream{
		entries: map[string][]byte{natsservice.RoomInfoKeyPrefix + natsservice.RoomMetadataKey: raw},
	}
	ttl := 2 * time.Hour
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	m.stopTimers = make(map[string]*time.Timer)
	m.natsService = natsservice.New(natsservice.Args{
		Ctx:    memoryJetStreamContext(t),
		App:    &config.AppConfig{NatsInfo: config.NatsInfo{DefaultTTL: &ttl}},
		Js:     js,
		Logger: logrus.New(),
	})

	return m, js
}

func lockedWorkspaceRoom() *plugnmeet.RoomMetadata {
	return &plugnmeet.RoomMetadata{
		RoomFeatures: &plugnmeet.RoomCreateFeatures{
			WhiteboardFeatures:    &plugnmeet.WhiteboardFeatures{IsAllow: true},
			SharedNotePadFeatures: &plugnmeet.SharedNotePadFeatures{IsAllow: true},
		},
		DefaultLockSettings: &plugnmeet.LockSettings{
			LockWhiteboard:    proto.Bool(true),
			LockSharedNotepad: proto.Bool(true),
		},
	}
}

func storedRoomMetadata(t *testing.T, js *memoryJetStream) *plugnmeet.RoomMetadata {
	t.Helper()

	raw, ok := js.value(natsservice.RoomInfoKeyPrefix + natsservice.RoomMetadataKey)
	if !ok {
		t.Fatal("room metadata was not stored")
	}
	meta := new(plugnmeet.RoomMetadata)
	if err := protojson.Unmarshal(raw, meta); err != nil {
		t.Fatalf("unmarshal room metadata: %v", err)
	}

	return meta
}

func TestAfterAgentStoppedUnlocksTheWorkspace(t *testing.T) {
	m, js := newWorkspaceUnlockTestModel(t, lockedWorkspaceRoom())
	m.afterAgentStopped("room-1", "user_stopped")

	locks := storedRoomMetadata(t, js).GetDefaultLockSettings()
	if locks.GetLockWhiteboard() || locks.GetLockSharedNotepad() {
		t.Errorf("locks = %v/%v, want both unlocked", locks.GetLockWhiteboard(), locks.GetLockSharedNotepad())
	}
}

func TestAfterAgentStoppedKeepsTheWorkspaceLockedWhenTheRoomCloses(t *testing.T) {
	for _, reason := range []string{aiAgentStopReasonEmptyRoom, aiAgentStopReasonBudget} {
		t.Run(reason, func(t *testing.T) {
			m, js := newWorkspaceUnlockTestModel(t, lockedWorkspaceRoom())
			m.afterAgentStopped("room-1", reason)

			locks := storedRoomMetadata(t, js).GetDefaultLockSettings()
			if !locks.GetLockWhiteboard() || !locks.GetLockSharedNotepad() {
				t.Errorf("locks = %v/%v, want both locked for a closing room", locks.GetLockWhiteboard(), locks.GetLockSharedNotepad())
			}
		})
	}
}
