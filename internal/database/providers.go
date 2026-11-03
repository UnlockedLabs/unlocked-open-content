package database

import (
	"context"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
)

func (db *DB) GetVideoProvider() (*models.OpenContentProvider, error) {
	var provider models.OpenContentProvider
	if err := db.First(&provider, "title = ?", models.Youtube).Error; err != nil {
		return nil, newNotFoundDBError(err, "open_content_providers")
	}
	return &provider, nil
}

// GetEnabledProviderID is the first enabled content provider with this
// title, or zero if none is enabled.
func (db *DB) GetEnabledProviderID(ctx context.Context, title string) (uint, error) {
	providers := make([]models.OpenContentProvider, 0, 1)
	if err := db.WithContext(ctx).Find(&providers, "currently_enabled = true AND title = ?", title).Error; err != nil {
		return 0, newGetRecordsDBError(err, "open_content_providers")
	}
	if len(providers) == 0 {
		return 0, nil
	}
	return providers[0].ID, nil
}

// GetJobProvider is the content provider a job message names. An ID with no
// row gives an empty provider, not an error.
func (db *DB) GetJobProvider(ctx context.Context, id int) (*models.OpenContentProvider, error) {
	var provider models.OpenContentProvider
	if err := db.WithContext(ctx).Find(&provider, id).Error; err != nil {
		return nil, newGetRecordsDBError(err, "open_content_providers")
	}
	return &provider, nil
}

func (db *DB) GetHelpfulLinkOpenContentProviderId() (uint, error) {
	var provider models.OpenContentProvider
	if err := db.Model(&models.OpenContentProvider{}).Where("title = ?", "HelpfulLinks").First(&provider).Error; err != nil {
		return 0, newGetRecordsDBError(err, "open_content_providers")
	}
	return provider.ID, nil
}

func (db *DB) GetOpenContent(all bool) ([]models.OpenContentProvider, error) {
	tx := db.Model(&models.OpenContentProvider{})
	if !all {
		tx = tx.Where("currently_enabled = ?", true)
	}
	openContent := make([]models.OpenContentProvider, 0, 3)
	if err := tx.Find(&openContent).Error; err != nil {
		return nil, newNotFoundDBError(err, "open_content_providers")
	}
	return openContent, nil
}
