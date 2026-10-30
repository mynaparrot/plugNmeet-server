package models

import (
	"errors"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

const testNowMs int64 = 1_700_000_000_000

func newTestCountdownFeatures() *plugnmeet.CountdownTimerFeatures {
	return &plugnmeet.CountdownTimerFeatures{IsAllow: true}
}

func assertCountdownErr(t *testing.T, err, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestApplyCountdownTimerChangeStatus(t *testing.T) {
	cf := newTestCountdownFeatures()
	cf.IsActive = true
	cf.Status = countdownTimerStatusRunning
	cf.DurationSeconds = 300
	cf.EndTime = testNowMs + 1000
	cf.RemainingSeconds = 42
	cf.StartedBy = "user-1"

	active := true
	if err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task:     plugnmeet.CountdownTimerTask_CHANGE_STATUS,
		IsActive: &active,
	}, testNowMs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cf.IsActive || cf.Status != countdownTimerStatusIdle {
		t.Fatalf("state = (active=%v, status=%q), want (true, idle)", cf.IsActive, cf.Status)
	}
	if cf.DurationSeconds != 0 || cf.EndTime != 0 || cf.RemainingSeconds != 0 || cf.StartedBy != "" {
		t.Fatalf("runtime fields not reset: %+v", cf)
	}

	inactive := false
	if err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task:     plugnmeet.CountdownTimerTask_CHANGE_STATUS,
		IsActive: &inactive,
	}, testNowMs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cf.IsActive || cf.Status != "" {
		t.Fatalf("state = (active=%v, status=%q), want (false, \"\")", cf.IsActive, cf.Status)
	}

	err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask_CHANGE_STATUS,
	}, testNowMs)
	assertCountdownErr(t, err, config.ErrCountdownTimerGeneric)
}

func TestApplyCountdownTimerStart(t *testing.T) {
	tests := []struct {
		name     string
		duration uint32
		wantErr  error
	}{
		{name: "valid", duration: 300},
		{name: "max 24h", duration: countdownTimerMaxDurationSeconds},
		{name: "zero", duration: 0, wantErr: config.ErrCountdownTimerInvalidDuration},
		{name: "over 24h", duration: countdownTimerMaxDurationSeconds + 1, wantErr: config.ErrCountdownTimerInvalidDuration},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cf := newTestCountdownFeatures()
			err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
				Task:            plugnmeet.CountdownTimerTask_START_TIMER,
				DurationSeconds: &tt.duration,
				UserId:          "user-1",
			}, testNowMs)
			if tt.wantErr != nil {
				assertCountdownErr(t, err, tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !cf.IsActive || cf.Status != countdownTimerStatusRunning {
				t.Fatalf("state = (active=%v, status=%q), want (true, running)", cf.IsActive, cf.Status)
			}
			if cf.DurationSeconds != tt.duration || cf.EndTime != testNowMs+int64(tt.duration)*1000 {
				t.Fatalf("duration/endTime = (%d, %d), want (%d, %d)",
					cf.DurationSeconds, cf.EndTime, tt.duration, testNowMs+int64(tt.duration)*1000)
			}
			if cf.StartedBy != "user-1" || cf.RemainingSeconds != 0 {
				t.Fatalf("startedBy/remaining = (%q, %d), want (user-1, 0)", cf.StartedBy, cf.RemainingSeconds)
			}
		})
	}
}

func TestApplyCountdownTimerPause(t *testing.T) {
	cf := newTestCountdownFeatures()
	cf.IsActive = true
	cf.Status = countdownTimerStatusRunning
	cf.DurationSeconds = 300
	cf.EndTime = testNowMs + 300_000

	// 60.8s have elapsed -> 239.2s left, rounded up to 240s.
	if err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask_PAUSE_TIMER,
	}, testNowMs+60_800); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cf.Status != countdownTimerStatusPaused || cf.RemainingSeconds != 240 || cf.EndTime != 0 {
		t.Fatalf("state = (status=%q, remaining=%d, endTime=%d), want (paused, 240, 0)",
			cf.Status, cf.RemainingSeconds, cf.EndTime)
	}

	// Remaining time is capped at the original duration.
	cf.Status = countdownTimerStatusRunning
	cf.EndTime = testNowMs + 400_000
	if err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask_PAUSE_TIMER,
	}, testNowMs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cf.RemainingSeconds != cf.DurationSeconds {
		t.Fatalf("remaining = %d, want cap at %d", cf.RemainingSeconds, cf.DurationSeconds)
	}

	// Expired timer pauses at zero instead of underflowing.
	cf.Status = countdownTimerStatusRunning
	cf.EndTime = testNowMs - 5000
	if err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask_PAUSE_TIMER,
	}, testNowMs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cf.RemainingSeconds != 0 {
		t.Fatalf("remaining = %d, want 0", cf.RemainingSeconds)
	}

	// Not running -> error.
	cf.Status = countdownTimerStatusIdle
	err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask_PAUSE_TIMER,
	}, testNowMs)
	assertCountdownErr(t, err, config.ErrCountdownTimerNotRunning)
}

func TestApplyCountdownTimerResume(t *testing.T) {
	cf := newTestCountdownFeatures()
	cf.IsActive = true
	cf.Status = countdownTimerStatusPaused
	cf.DurationSeconds = 300
	cf.RemainingSeconds = 120

	if err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask_RESUME_TIMER,
	}, testNowMs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cf.Status != countdownTimerStatusRunning || cf.EndTime != testNowMs+120_000 || cf.RemainingSeconds != 0 {
		t.Fatalf("state = (status=%q, endTime=%d, remaining=%d), want (running, %d, 0)",
			cf.Status, cf.EndTime, cf.RemainingSeconds, testNowMs+120_000)
	}

	// No remaining seconds -> error.
	cf.Status = countdownTimerStatusPaused
	cf.RemainingSeconds = 0
	err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask_RESUME_TIMER,
	}, testNowMs)
	assertCountdownErr(t, err, config.ErrCountdownTimerNotPaused)

	// Not paused -> error.
	cf.Status = countdownTimerStatusRunning
	cf.RemainingSeconds = 60
	err = applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask_RESUME_TIMER,
	}, testNowMs)
	assertCountdownErr(t, err, config.ErrCountdownTimerNotPaused)
}

func TestApplyCountdownTimerExtend(t *testing.T) {
	t.Run("running defaults to 60s", func(t *testing.T) {
		cf := newTestCountdownFeatures()
		cf.IsActive = true
		cf.Status = countdownTimerStatusRunning
		cf.DurationSeconds = 300
		cf.EndTime = testNowMs + 100_000

		if err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
			Task: plugnmeet.CountdownTimerTask_EXTEND_TIMER,
		}, testNowMs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cf.DurationSeconds != 360 {
			t.Fatalf("duration = %d, want 360", cf.DurationSeconds)
		}
		if want := testNowMs + 100_000 + 60_000; cf.EndTime != want {
			t.Fatalf("endTime = %d, want %d", cf.EndTime, want)
		}
	})

	t.Run("running with expired end time extends from now", func(t *testing.T) {
		cf := newTestCountdownFeatures()
		cf.IsActive = true
		cf.Status = countdownTimerStatusRunning
		cf.DurationSeconds = 300
		cf.EndTime = testNowMs - 5000

		extend := uint32(30)
		if err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
			Task:            plugnmeet.CountdownTimerTask_EXTEND_TIMER,
			DurationSeconds: &extend,
		}, testNowMs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cf.DurationSeconds != 330 {
			t.Fatalf("duration = %d, want 330", cf.DurationSeconds)
		}
		if want := testNowMs + 30_000; cf.EndTime != want {
			t.Fatalf("endTime = %d, want %d", cf.EndTime, want)
		}
	})

	t.Run("paused extends remaining", func(t *testing.T) {
		cf := newTestCountdownFeatures()
		cf.IsActive = true
		cf.Status = countdownTimerStatusPaused
		cf.DurationSeconds = 300
		cf.RemainingSeconds = 120

		extend := uint32(90)
		if err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
			Task:            plugnmeet.CountdownTimerTask_EXTEND_TIMER,
			DurationSeconds: &extend,
		}, testNowMs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cf.DurationSeconds != 390 || cf.RemainingSeconds != 210 {
			t.Fatalf("duration/remaining = (%d, %d), want (390, 210)", cf.DurationSeconds, cf.RemainingSeconds)
		}
		if cf.EndTime != 0 {
			t.Fatalf("endTime = %d, want 0", cf.EndTime)
		}
	})

	t.Run("idle is not extendable", func(t *testing.T) {
		cf := newTestCountdownFeatures()
		cf.IsActive = true
		cf.Status = countdownTimerStatusIdle

		err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
			Task: plugnmeet.CountdownTimerTask_EXTEND_TIMER,
		}, testNowMs)
		assertCountdownErr(t, err, config.ErrCountdownTimerNotRunning)
	})
}

func TestApplyCountdownTimerStop(t *testing.T) {
	cf := newTestCountdownFeatures()
	cf.IsActive = true
	cf.Status = countdownTimerStatusPaused
	cf.DurationSeconds = 300
	cf.RemainingSeconds = 120
	cf.StartedBy = "user-1"

	if err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask_STOP_TIMER,
	}, testNowMs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cf.IsActive || cf.Status != countdownTimerStatusIdle {
		t.Fatalf("state = (active=%v, status=%q), want (true, idle)", cf.IsActive, cf.Status)
	}
	if cf.DurationSeconds != 0 || cf.EndTime != 0 || cf.RemainingSeconds != 0 || cf.StartedBy != "" {
		t.Fatalf("runtime fields not reset: %+v", cf)
	}

	cf.IsActive = false
	err := applyCountdownTimerTask(cf, &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask_STOP_TIMER,
	}, testNowMs)
	assertCountdownErr(t, err, config.ErrCountdownTimerNotActive)
}

func TestApplyCountdownTimerUnknownTask(t *testing.T) {
	err := applyCountdownTimerTask(newTestCountdownFeatures(), &plugnmeet.CountdownTimerReq{
		Task: plugnmeet.CountdownTimerTask(99),
	}, testNowMs)
	assertCountdownErr(t, err, config.ErrCountdownTimerGeneric)
}
