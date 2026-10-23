package models

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestBreakoutParentRoomRefFormatAndParse(t *testing.T) {
	tests := []struct {
		name      string
		ref       string
		wantRoom  string
		wantTable uint64
		wantErr   bool
	}{
		{name: "valid", ref: FormatBreakoutParentRoomRef("room-1", 42), wantRoom: "room-1", wantTable: 42},
		{name: "valid room id with colon", ref: "room:1:7", wantRoom: "room:1", wantTable: 7},
		{name: "empty", ref: "", wantErr: true},
		{name: "no separator", ref: "room-1", wantErr: true},
		{name: "empty room id", ref: ":42", wantErr: true},
		{name: "empty table id", ref: "room-1:", wantErr: true},
		{name: "non numeric table id", ref: "room-1:abc", wantErr: true},
		{name: "zero table id", ref: "room-1:0", wantErr: true},
		{name: "negative table id", ref: "room-1:-1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roomId, tableId, err := ParseBreakoutParentRoomRef(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got roomId=%q tableId=%d", tt.ref, roomId, tableId)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.ref, err)
			}
			if roomId != tt.wantRoom || tableId != tt.wantTable {
				t.Fatalf("got (%q, %d), want (%q, %d)", roomId, tableId, tt.wantRoom, tt.wantTable)
			}
		})
	}
}

func TestPlainParentRoomIdFromRef(t *testing.T) {
	if got := plainParentRoomIdFromRef(FormatBreakoutParentRoomRef("parent-room", 9)); got != "parent-room" {
		t.Fatalf("got %q, want parent-room", got)
	}
	if got := plainParentRoomIdFromRef(""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := plainParentRoomIdFromRef("parent-room"); got != "" {
		t.Fatalf("got %q, want empty for malformed reference", got)
	}
}

// TestPrepareRoomDbInfoCopiesParentReference locks in that DB preparation only
// copies the parent reference carried by the room metadata (which breakout
// room creation fills with `<plain roomId>:<parent table id>`); it must not do
// its own NATS lookup or formatting.
func TestPrepareRoomDbInfoCopiesParentReference(t *testing.T) {
	ref := FormatBreakoutParentRoomRef("parent-room", 7)
	child := &plugnmeet.CreateRoomReq{
		RoomId: "parent-room-1",
		Metadata: &plugnmeet.RoomMetadata{
			RoomTitle:      "child",
			IsBreakoutRoom: true,
			ParentRoomId:   ref,
		},
	}

	m := &RoomModel{}
	info, sid := m.prepareRoomDbInfo(child, nil)
	if sid == "" {
		t.Fatal("expected a generated room sid")
	}
	if info.RoomId != child.RoomId || info.IsBreakoutRoom != 1 || info.ParentRoomID != ref {
		t.Fatalf("child row = (roomId=%q, breakout=%d, parent=%q), want (%q, 1, %q)",
			info.RoomId, info.IsBreakoutRoom, info.ParentRoomID, child.RoomId, ref)
	}

	parentRoomId, parentTableId, err := ParseBreakoutParentRoomRef(info.ParentRoomID)
	if err != nil {
		t.Fatalf("stored parent reference %q is not parseable: %v", info.ParentRoomID, err)
	}
	if parentRoomId != "parent-room" || parentTableId != 7 {
		t.Fatalf("stored parent reference resolves to (%q, %d), want (parent-room, 7)", parentRoomId, parentTableId)
	}

	// updating an existing row must overwrite the reference with the metadata value
	existing := &dbmodels.RoomInfo{RoomId: child.RoomId}
	updated, _ := m.prepareRoomDbInfo(child, existing)
	if updated != existing || updated.ParentRoomID != ref {
		t.Fatalf("existing row parent reference = %q, want %q", updated.ParentRoomID, ref)
	}

	// main rooms carry no parent reference
	main := &plugnmeet.CreateRoomReq{
		RoomId:   "main-room",
		Metadata: &plugnmeet.RoomMetadata{RoomTitle: "main"},
	}
	mainInfo, _ := m.prepareRoomDbInfo(main, nil)
	if mainInfo.IsBreakoutRoom != 0 || mainInfo.ParentRoomID != "" {
		t.Fatalf("main room row = (breakout=%d, parent=%q), want (0, \"\")",
			mainInfo.IsBreakoutRoom, mainInfo.ParentRoomID)
	}
}

func TestUsageArtifactProvenance(t *testing.T) {
	m := &ArtifactModel{}

	mainRoom := &plugnmeet.RoomArtifactMetadata{}
	m.setUsageArtifactProvenance(mainRoom, 11, false)
	if mainRoom.OccurredInBreakoutRoom == nil || *mainRoom.OccurredInBreakoutRoom || mainRoom.SourceRoomTableId != 11 {
		t.Fatalf("main provenance = (breakout=%v, table=%d), want (false, 11)", mainRoom.OccurredInBreakoutRoom, mainRoom.SourceRoomTableId)
	}
	assertMarshaledProvenance(t, mainRoom, `"occurredInBreakoutRoom":false`)

	breakoutRoom := &plugnmeet.RoomArtifactMetadata{}
	m.setUsageArtifactProvenance(breakoutRoom, 23, true)
	if breakoutRoom.OccurredInBreakoutRoom == nil || !*breakoutRoom.OccurredInBreakoutRoom || breakoutRoom.SourceRoomTableId != 23 {
		t.Fatalf("breakout provenance = (breakout=%v, table=%d), want (true, 23)", breakoutRoom.OccurredInBreakoutRoom, breakoutRoom.SourceRoomTableId)
	}
	assertMarshaledProvenance(t, breakoutRoom, `"occurredInBreakoutRoom":true`)
}

// assertMarshaledProvenance verifies that the provenance field is present in the
// protojson representation stored in the DB, for both true and false values.
// protojson emits the default JSON (camelCase) field name here, matching the
// rest of the stored metadata.
func assertMarshaledProvenance(t *testing.T, metadata *plugnmeet.RoomArtifactMetadata, want string) {
	t.Helper()

	marshal, err := protojson.Marshal(metadata)
	if err != nil {
		t.Fatalf("failed to marshal metadata: %v", err)
	}
	if !strings.Contains(string(marshal), want) {
		t.Fatalf("marshaled metadata %s does not contain %s", marshal, want)
	}
}

func TestUsageArtifactOwnershipUsesExactParentRow(t *testing.T) {
	m, gdb, rc := newArtifactTestEnv(t)

	// Same roomId, two sessions: only the row encoded in the child reference
	// may own the usage artifact.
	oldParent := createTestRoomRow(t, gdb, "parent-room", false, "")
	currentParent := createTestRoomRow(t, gdb, "parent-room", false, "")
	child := createTestRoomRow(t, gdb, "parent-room-1", true, FormatBreakoutParentRoomRef("parent-room", currentParent.ID))
	seedChatTranslationUsage(t, rc, child.RoomId)
	artifactType := plugnmeet.RoomArtifactType_CHAT_TRANSLATION_USAGE

	m.CreateAllRoomUsageArtifacts(child.RoomId, child.Sid, child.ID, m.log)

	artifacts, total, err := m.ds.GetArtifacts([]string{"parent-room"}, nil, &artifactType, 0, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(artifacts) != 1 {
		t.Fatalf("got %d artifacts, want exactly 1", total)
	}

	artifact := artifacts[0]
	if artifact.RoomId != currentParent.RoomId {
		t.Fatalf("artifact owner roomId = %q, want %q", artifact.RoomId, currentParent.RoomId)
	}
	if artifact.RoomTableID != currentParent.ID {
		t.Fatalf("artifact owner table id = %d, want %d (not %d)", artifact.RoomTableID, currentParent.ID, oldParent.ID)
	}

	metadata := decodeArtifactMetadata(t, artifact.Metadata)
	if metadata.OccurredInBreakoutRoom == nil || !*metadata.OccurredInBreakoutRoom {
		t.Fatal("expected occurredInBreakoutRoom=true")
	}
	if !strings.Contains(artifact.Metadata, `"occurredInBreakoutRoom":true`) {
		t.Fatalf("marshaled metadata %s does not contain occurredInBreakoutRoom:true", artifact.Metadata)
	}
	if metadata.SourceRoomTableId != child.ID {
		t.Fatalf("source_room_table_id = %d, want %d", metadata.SourceRoomTableId, child.ID)
	}
}

func TestUsageArtifactOwnershipMainRoom(t *testing.T) {
	m, gdb, rc := newArtifactTestEnv(t)

	mainRoom := createTestRoomRow(t, gdb, "main-room", false, "")
	seedChatTranslationUsage(t, rc, mainRoom.RoomId)
	artifactType := plugnmeet.RoomArtifactType_CHAT_TRANSLATION_USAGE

	m.CreateAllRoomUsageArtifacts(mainRoom.RoomId, mainRoom.Sid, mainRoom.ID, m.log)

	artifacts, total, err := m.ds.GetArtifacts([]string{mainRoom.RoomId}, nil, &artifactType, 0, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(artifacts) != 1 {
		t.Fatalf("got %d artifacts, want exactly 1", total)
	}

	artifact := artifacts[0]
	if artifact.RoomId != mainRoom.RoomId || artifact.RoomTableID != mainRoom.ID {
		t.Fatalf("owner = (%q, %d), want (%q, %d)", artifact.RoomId, artifact.RoomTableID, mainRoom.RoomId, mainRoom.ID)
	}

	metadata := decodeArtifactMetadata(t, artifact.Metadata)
	if metadata.OccurredInBreakoutRoom == nil {
		t.Fatal("expected occurredInBreakoutRoom to be present for main-room usage")
	}
	if *metadata.OccurredInBreakoutRoom {
		t.Fatal("expected occurredInBreakoutRoom=false")
	}
	if !strings.Contains(artifact.Metadata, `"occurredInBreakoutRoom":false`) {
		t.Fatalf("marshaled metadata %s does not contain occurredInBreakoutRoom:false", artifact.Metadata)
	}
	if metadata.SourceRoomTableId != mainRoom.ID {
		t.Fatalf("source_room_table_id = %d, want %d", metadata.SourceRoomTableId, mainRoom.ID)
	}
}

func TestUsageArtifactOwnerResolutionFailureKeepsCostData(t *testing.T) {
	m, gdb, rc := newArtifactTestEnv(t)

	child := createTestRoomRow(t, gdb, "orphan-breakout", true, "parent-without-table-id")
	key := seedChatTranslationUsage(t, rc, child.RoomId)
	artifactType := plugnmeet.RoomArtifactType_CHAT_TRANSLATION_USAGE

	m.CreateAllRoomUsageArtifacts(child.RoomId, child.Sid, child.ID, m.log)

	artifacts, total, err := m.ds.GetArtifacts(nil, nil, &artifactType, 0, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(artifacts) != 0 {
		t.Fatalf("got %d artifacts, want none when parent resolution fails", total)
	}

	usage, err := rc.HGet(context.Background(), key, redisservice.TotalUsageField).Result()
	if err != nil {
		t.Fatalf("expected source cost data to be preserved, got error: %v", err)
	}
	if usage == "" {
		t.Fatal("expected source cost data to be preserved, but it was consumed")
	}
}

// installDumpTablePrefix is the table prefix used by sql_dump/install.sql. The
// project tables themselves are created through config.FormatDBTable, which is a
// no-op when no prefix is configured (as in tests).
const installDumpTablePrefix = "pnm_"

// createTestSchemaFromInstallDump recreates the requested tables from
// root/sql_dump/install.sql, so tests exercise the real project schema
// instead of a hand-maintained copy. dbmodels tags target MySQL (e.g.
// DEFAULT current_timestamp()), which rules out AutoMigrate, so the column
// definitions of each MySQL CREATE TABLE block are reused as-is.
func createTestSchemaFromInstallDump(t *testing.T, gdb *gorm.DB, tables ...string) {
	t.Helper()

	dump, err := os.ReadFile(filepath.Join("..", "..", "sql_dump", "install.sql"))
	if err != nil {
		t.Fatalf("failed to read install.sql schema: %v", err)
	}
	for _, table := range tables {
		ddl := sqliteDDLFromInstallDump(string(dump), table)
		if ddl == "" {
			t.Fatalf("table %q not found in install.sql", installDumpTablePrefix+table)
		}
		if err := gdb.Exec(ddl).Error; err != nil {
			t.Fatalf("failed to create %s test table from install.sql: %v", table, err)
		}
	}
}

// sqliteDDLFromInstallDump builds a SQLite CREATE TABLE statement for one of
// the two tables used here from the backtick column lines of its install.sql
// block. Indexes, unique keys, foreign keys and ENGINE clauses are skipped;
// only the MySQL bits used by the current dump are rewritten (the
// auto-increment id, current_timestamp() defaults, the inline ON UPDATE
// clause and UNSIGNED). Returns "" when the table is not in the dump.
func sqliteDDLFromInstallDump(dump, table string) string {
	start := strings.Index(dump, "CREATE TABLE IF NOT EXISTS `"+installDumpTablePrefix+table+"`")
	if start < 0 {
		return ""
	}
	block := dump[start:]
	if end := strings.Index(block, ";"); end >= 0 {
		block = block[:end]
	}

	var columns []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSuffix(strings.TrimSpace(line), ",")
		if !strings.HasPrefix(line, "`") {
			continue
		}
		if strings.Contains(line, "AUTO_INCREMENT") {
			columns = append(columns, "`id` INTEGER PRIMARY KEY AUTOINCREMENT")
			continue
		}
		line = strings.ReplaceAll(line, "ON UPDATE current_timestamp()", "")
		line = strings.ReplaceAll(line, "current_timestamp()", "CURRENT_TIMESTAMP")
		line = strings.ReplaceAll(line, " UNSIGNED", "")
		columns = append(columns, strings.TrimRight(line, " \t"))
	}

	return fmt.Sprintf("CREATE TABLE %s (\n\t%s\n)", config.FormatDBTable(table), strings.Join(columns, ",\n\t"))
}

func newArtifactTestEnv(t *testing.T) (*ArtifactModel, *gorm.DB, *redis.Client) {
	t.Helper()

	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("failed to open sqlite test db: %v", err)
	}
	createTestSchemaFromInstallDump(t, gdb, "room_info", "room_artifacts")

	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	ds := dbservice.New(dbservice.Args{Ctx: ctx, Db: gdb, Logger: logger})

	host := os.Getenv("TEST_REDIS_HOST")
	if host == "" {
		host = "localhost:6379"
	}
	rc := redis.NewClient(&redis.Options{Addr: host})
	if err := rc.Ping(ctx).Err(); err != nil {
		t.Skipf("no redis fixture at %s: %v", host, err)
	}
	t.Cleanup(func() {
		_ = rc.Close()
	})

	ttl := 2 * time.Minute
	app := &config.AppConfig{
		RedisInfo: config.RedisInfo{DefaultTTL: &ttl},
		Insights:  &config.InsightsConfig{},
	}
	rs := redisservice.New(redisservice.Args{Ctx: ctx, Rc: rc, App: app, Logger: logger})
	analyticsModel := &AnalyticsModel{app: app}

	m := &ArtifactModel{
		ctx:            ctx,
		app:            app,
		ds:             ds,
		rs:             rs,
		analyticsModel: analyticsModel,
		log:            logger.WithField("test", "artifact"),
	}
	return m, gdb, rc
}

func createTestRoomRow(t *testing.T, gdb *gorm.DB, roomId string, isBreakout bool, parentRef string) *dbmodels.RoomInfo {
	t.Helper()

	breakout := 0
	if isBreakout {
		breakout = 1
	}
	row := &dbmodels.RoomInfo{
		RoomId:         roomId,
		Sid:            fmt.Sprintf("%s-%d", roomId, time.Now().UnixNano()),
		IsRunning:      0,
		IsBreakoutRoom: breakout,
		ParentRoomID:   parentRef,
	}
	if err := gdb.Create(row).Error; err != nil {
		t.Fatalf("failed to create room row: %v", err)
	}
	return row
}

func seedChatTranslationUsage(t *testing.T, rc *redis.Client, roomId string) string {
	t.Helper()

	key := fmt.Sprintf("%s:%s:usage", redisservice.ChatTranslationRedisKey, roomId)
	if err := rc.HSet(context.Background(), key, map[string]interface{}{
		redisservice.TotalUsageField: int64(120),
		"user-1":                     int64(120),
	}).Err(); err != nil {
		t.Fatalf("failed to seed chat translation usage: %v", err)
	}
	t.Cleanup(func() {
		_ = rc.Del(context.Background(), key).Err()
	})
	return key
}

func decodeArtifactMetadata(t *testing.T, raw string) *plugnmeet.RoomArtifactMetadata {
	t.Helper()

	metadata := new(plugnmeet.RoomArtifactMetadata)
	if err := protojson.Unmarshal([]byte(raw), metadata); err != nil {
		t.Fatalf("failed to decode artifact metadata: %v", err)
	}
	return metadata
}
