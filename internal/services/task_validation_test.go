package services

import (
	"errors"
	"strings"
	"testing"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/models"
)

func validTask() models.Task {
	return models.Task{Name: "Focus", Category: "deep-work", StartTime: "09:00", EndTime: "10:00", Energy: 3, Date: "2026-10-29", Status: "pending"}
}

func TestValidateNewTask(t *testing.T) {
	cases := []struct {
		name string
		edit func(*models.Task)
		ok   bool
		code apierr.Code // set when !ok
	}{
		{"valid", func(*models.Task) {}, true, ""},
		{"ends at 24:00", func(t *models.Task) { t.StartTime, t.EndTime = "23:00", "24:00" }, true, ""},
		{"impossible date", func(t *models.Task) { t.Date = "2026-02-30" }, false, apierr.TaskDateInvalid},
		{"non-ISO date", func(t *models.Task) { t.Date = "29.10.2026" }, false, apierr.TaskDateInvalid},
		{"unpadded time", func(t *models.Task) { t.StartTime = "9:00" }, false, apierr.TaskTimeInvalid},
		{"12h time", func(t *models.Task) { t.StartTime = "09:00 AM" }, false, apierr.TaskTimeInvalid},
		{"end equals start", func(t *models.Task) { t.EndTime = "09:00" }, false, apierr.TaskEndBeforeStart},
		{"end before start", func(t *models.Task) { t.StartTime, t.EndTime = "10:00", "09:00" }, false, apierr.TaskEndBeforeStart},
		{"crosses midnight", func(t *models.Task) { t.StartTime, t.EndTime = "23:00", "01:00" }, false, apierr.TaskEndBeforeStart},
		{"end after 24:00", func(t *models.Task) { t.EndTime = "24:30" }, false, apierr.TaskTimeInvalid},
		{"start at 24:00", func(t *models.Task) { t.StartTime, t.EndTime = "24:00", "24:00" }, false, apierr.TaskEndBeforeStart},
		{"unknown category", func(t *models.Task) { t.Category = "nap" }, false, apierr.TaskCategoryInvalid},
		{"energy 0", func(t *models.Task) { t.Energy = 0 }, false, apierr.TaskEnergyInvalid},
		{"energy 6", func(t *models.Task) { t.Energy = 6 }, false, apierr.TaskEnergyInvalid},
		{"empty name", func(t *models.Task) { t.Name = "" }, false, apierr.TaskNameLength},
		{"blank name", func(t *models.Task) { t.Name = "   " }, false, apierr.TaskNameLength},
		{"120-char name", func(t *models.Task) { t.Name = strings.Repeat("é", 120) }, true, ""},
		{"121-char name", func(t *models.Task) { t.Name = strings.Repeat("a", 121) }, false, apierr.TaskNameLength},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := validTask()
			tc.edit(&task)
			err := validateNewTask(&task)
			if (err == nil) != tc.ok {
				t.Fatalf("ok=%v, got %v", tc.ok, err)
			}
			if err != nil && !errors.Is(err, ErrInvalidTask) {
				t.Fatalf("validation errors must wrap ErrInvalidTask, got %v", err)
			}
			if code, _ := apierr.CodeOf(err); err != nil && code != tc.code {
				t.Fatalf("want code %q, got %q (%v)", tc.code, code, err)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestMergeTaskUpdate(t *testing.T) {
	current := validTask()
	current.Distractions = 4

	cases := []struct {
		name      string
		upd       TaskUpdate
		ok        bool
		cols      []string
		timesMove bool
	}{
		{"status only", TaskUpdate{Status: ptr("completed")}, true, []string{"status"}, false},
		{"bad status", TaskUpdate{Status: ptr("moved")}, false, nil, false},
		{"distractions 100", TaskUpdate{Distractions: ptr(100)}, true, []string{"distractions"}, false},
		{"distractions 101", TaskUpdate{Distractions: ptr(101)}, false, nil, false},
		{"distractions -1", TaskUpdate{Distractions: ptr(-1)}, false, nil, false},
		{"energy 9", TaskUpdate{Energy: ptr(9)}, false, nil, false},
		{"bad category", TaskUpdate{Category: ptr("nap")}, false, nil, false},
		{"long name", TaskUpdate{Name: ptr(strings.Repeat("a", 121))}, false, nil, false},
		{"end before existing start", TaskUpdate{EndTime: ptr("08:00")}, false, nil, false},
		{"move later", TaskUpdate{StartTime: ptr("10:00"), EndTime: ptr("11:00")}, true, []string{"start_time", "end_time"}, true},
		{"bad date", TaskUpdate{Date: ptr("2026-13-01")}, false, nil, false},
		{"new date", TaskUpdate{Date: ptr("2026-10-30")}, true, []string{"date"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cols, merged, timesMove, err := mergeTaskUpdate(current, tc.upd)
			if (err == nil) != tc.ok {
				t.Fatalf("ok=%v, got %v", tc.ok, err)
			}
			if err != nil {
				return
			}
			if len(cols) != len(tc.cols) {
				t.Fatalf("expected only %v to be written, got %v", tc.cols, cols)
			}
			for _, c := range tc.cols {
				if _, ok := cols[c]; !ok {
					t.Fatalf("expected column %s in %v", c, cols)
				}
			}
			if timesMove != tc.timesMove {
				t.Fatalf("timesMove=%v, want %v", timesMove, tc.timesMove)
			}
			if merged.Distractions != 4 && tc.upd.Distractions == nil {
				t.Fatal("merge must keep fields that were not provided")
			}
		})
	}
}

func TestMergeTaskUpdate_LegacyInvalidRowCanStillChangeStatus(t *testing.T) {
	legacy := validTask()
	legacy.StartTime, legacy.EndTime = "10:00", "09:00" // stored before validation existed
	if _, _, _, err := mergeTaskUpdate(legacy, TaskUpdate{Status: ptr("completed")}); err != nil {
		t.Fatalf("a status-only update must not re-validate untouched times: %v", err)
	}
}

func TestMergeTaskUpdate_MapsLegacyStatuses(t *testing.T) {
	for legacy, want := range map[string]string{"pending": models.StatusPlanned, "completed": models.StatusDone} {
		cols, merged, _, err := mergeTaskUpdate(validTask(), TaskUpdate{Status: ptr(legacy)})
		if err != nil || cols["status"] != want || merged.Status != want {
			t.Fatalf("%s: want %s, got %v %v %v", legacy, want, cols, merged.Status, err)
		}
	}
}
