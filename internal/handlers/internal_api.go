package handlers

import (
	"context"
	"math"
	"time"

	api "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/UnlockedLabs/unlocked-open-content/internal/database"
)

// openContentAPI answers core's questions (api.OpenContent) from the
// database.
type openContentAPI struct{ db *database.DB }

// newAPI is Open Content's answers to core, reading db.
func newAPI(db *database.DB) api.OpenContent {
	return openContentAPI{db: db}
}

func (a openContentAPI) EndActivity(_ context.Context, activityID int64) {
	a.db.UpdateOpenContentActivityStopTS(activityID)
}

func (a openContentAPI) TopLibraries(ctx context.Context, userID, facilityID uint) ([]api.ContentUsage, error) {
	return a.db.GetTopLibraries(ctx, userID, facilityID)
}

func (a openContentAPI) RecentVideos(ctx context.Context, userID uint) ([]api.ContentUsage, error) {
	return a.db.GetRecentVideos(ctx, userID)
}

func (a openContentAPI) ResourcesAccessed(ctx context.Context, userID uint) (int64, error) {
	return a.db.CountResourcesAccessed(ctx, userID)
}

func (a openContentAPI) ActivityEngagement(ctx context.Context, userID uint) (api.ActivityEngagement, error) {
	return a.db.GetActivityEngagement(ctx, userID)
}

func (a openContentAPI) FacilityContent(ctx context.Context, facilityIDs []uint) (map[uint]api.FacilityContent, error) {
	return a.db.GetFacilityContent(ctx, facilityIDs)
}

// Knowledge Center

func kcPriorWindow(start, end *time.Time) (*time.Time, *time.Time) {
	if start == nil || end == nil {
		return nil, nil
	}
	duration := end.Sub(*start)
	priorStart := start.Add(-duration)
	priorEnd := *start
	return &priorStart, &priorEnd
}

func kcPctChange(current, prior int64) int {
	if prior <= 0 {
		return 0
	}
	return int(math.Round(float64(current-prior) / float64(prior) * 100))
}

func (a openContentAPI) Interactions(ctx context.Context, w api.Window) (api.Interactions, error) {
	total, err := a.db.CountActivities(ctx, w)
	if err != nil {
		return api.Interactions{}, err
	}
	unique, err := a.db.CountActiveUsers(ctx, w)
	if err != nil {
		return api.Interactions{}, err
	}
	change := 0
	if priorStart, priorEnd := kcPriorWindow(w.Start, w.End); priorStart != nil {
		priorTotal, err := a.db.CountActivities(ctx, api.Window{Start: priorStart, End: priorEnd, FacilityID: w.FacilityID})
		if err != nil {
			return api.Interactions{}, err
		}
		change = kcPctChange(total, priorTotal)
	}
	return api.Interactions{Total: total, Unique: unique, Change: change}, nil
}

func (a openContentAPI) AvgSessionMinutes(ctx context.Context, w api.Window) (float64, error) {
	return a.db.GetAvgSessionMinutes(ctx, w)
}

func (a openContentAPI) RepeatEngagement(ctx context.Context, w api.Window) (api.RepeatEngagement, error) {
	visits, err := a.db.GetVisitsPerUser(ctx, w)
	if err != nil {
		return api.RepeatEngagement{}, err
	}
	var engagement api.RepeatEngagement
	for _, n := range visits {
		switch {
		case n >= 5:
			engagement.FivePlus++
		case n >= 2:
			engagement.TwoToFour++
		default:
			engagement.Once++
		}
	}
	return engagement, nil
}

func (a openContentAPI) LibraryViewsByCategory(ctx context.Context, w api.Window) ([]api.CategoryViews, error) {
	return a.db.GetLibraryViewsByCategory(ctx, w)
}

func (a openContentAPI) TopLibraryVisits(ctx context.Context, w api.Window, limit int) ([]api.ContentVisits, error) {
	return a.topContentVisits(ctx, "libraries", w, limit)
}

func (a openContentAPI) TopVideoVisits(ctx context.Context, w api.Window, limit int) ([]api.ContentVisits, error) {
	return a.topContentVisits(ctx, "videos", w, limit)
}

// topContentVisits is the most visited content in table in w, each with its
// change from the window before.
func (a openContentAPI) topContentVisits(ctx context.Context, table string, w api.Window, limit int) ([]api.ContentVisits, error) {
	current, err := a.db.GetTopContentVisits(ctx, table, w, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]api.ContentVisits, 0, len(current))
	if len(current) == 0 {
		return rows, nil
	}

	priorVisits := make(map[uint]int64)
	if priorStart, priorEnd := kcPriorWindow(w.Start, w.End); priorStart != nil {
		ids := make([]uint, 0, len(current))
		for _, c := range current {
			ids = append(ids, c.ContentID)
		}
		priorVisits, err = a.db.GetContentVisits(ctx, table, ids, api.Window{Start: priorStart, End: priorEnd, FacilityID: w.FacilityID})
		if err != nil {
			return nil, err
		}
	}

	for _, c := range current {
		rows = append(rows, api.ContentVisits{
			Title:  c.Title,
			Visits: c.Visits,
			Change: kcPctChange(c.Visits, priorVisits[c.ContentID]),
		})
	}
	return rows, nil
}

// Demo seed

func (a openContentAPI) ResetDemoActivity(ctx context.Context, before time.Time) error {
	return a.db.ResetDemoActivity(ctx, before)
}

func (a openContentAPI) SeedDemoActivity(ctx context.Context, facilityID uint, userIDs []uint) error {
	return a.db.SeedDemoActivity(ctx, facilityID, userIDs)
}
