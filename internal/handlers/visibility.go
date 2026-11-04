package handlers

import "github.com/UnlockedLabs/unlocked-open-content/internal/models"

type setFacilityVisibilityRequest struct {
	FacilityIDs      []uint `json:"facility_ids"`
	VisibilityStatus bool   `json:"visibility_status"`
}

func (srv *server) setHelpfulLinkVisibility(args *models.QueryContext, id int, facilityIDs []uint, visible bool) error {
	link, err := srv.db.GetLinkFromId(uint(id))
	if err != nil {
		return err
	}
	statuses := buildVisibilityStatuses(link.ID, link.OpenContentProviderID, facilityIDs, visible)
	return srv.db.UpsertFacilityVisibilityStatuses(args, statuses, visible)
}

func (srv *server) setVideoVisibility(args *models.QueryContext, id int, facilityIDs []uint, visible bool) error {
	video, err := srv.db.GetVideoByID(id, args.FacilityID)
	if err != nil {
		return err
	}
	statuses := buildVisibilityStatuses(video.ID, video.OpenContentProviderID, facilityIDs, visible)
	return srv.db.UpsertFacilityVisibilityStatuses(args, statuses, visible)
}

func (srv *server) setLibraryVisibility(args *models.QueryContext, id int, facilityIDs []uint, visible bool) (*models.Library, error) {
	library, err := srv.db.GetLibraryByID(id)
	if err != nil {
		return nil, err
	}
	statuses := buildVisibilityStatuses(library.ID, library.OpenContentProviderID, facilityIDs, visible)
	if err := srv.db.UpsertFacilityVisibilityStatuses(args, statuses, visible); err != nil {
		return nil, err
	}
	library.VisibilityStatus = visible
	return library, nil
}

// buildVisibilityStatuses is one status for each facility, without duplicates.
func buildVisibilityStatuses(contentID, providerID uint, facilityIDs []uint, visible bool) []models.FacilityVisibilityStatus {
	statuses := make([]models.FacilityVisibilityStatus, 0, len(facilityIDs))
	seen := make(map[uint]bool, len(facilityIDs))
	for _, facilityID := range facilityIDs {
		if seen[facilityID] {
			continue
		}
		seen[facilityID] = true
		statuses = append(statuses, models.FacilityVisibilityStatus{
			FacilityID:            facilityID,
			OpenContentProviderID: providerID,
			ContentID:             contentID,
			VisibilityStatus:      visible,
		})
	}
	return statuses
}
