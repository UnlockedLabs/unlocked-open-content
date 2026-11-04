package models

import "time"

type OpenContentActivity struct {
	ID                    int64     `gorm:"primaryKey" json:"id"`
	OpenContentProviderID uint      `gorm:"not null" json:"open_content_provider_id"`
	FacilityID            uint      `gorm:"not null" json:"facility_id"`
	UserID                uint      `gorm:"not null" json:"user_id"`
	ContentID             uint      `gorm:"not null" json:"content_id"`
	OpenContentUrlID      uint      `gorm:"not null" json:"open_content_url_id"`
	RequestTS             time.Time `gorm:"type:datetime;default:CURRENT_TIMESTAMP" json:"request_ts"`
	StopTS                time.Time `gorm:"type:datetime;default:NULL" json:"stop_ts"`

	OpenContentProvider *OpenContentProvider `gorm:"foreignKey:OpenContentProviderID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL" json:"open_content_provider"`
}

func (OpenContentActivity) TableName() string { return "open_content_activities" }

type OpenContentFavorite struct {
	UserID                uint      `gorm:"not null" json:"user_id"`
	ContentID             uint      `gorm:"not null" json:"content_id"`
	OpenContentProviderID uint      `gorm:"not null" json:"open_content_provider_id"`
	OpenContentUrlID      *uint     `json:"open_content_url_id,omitempty"`
	Name                  *string   `json:"name,omitempty"`
	FacilityID            *uint     `json:"facility_id"`
	CreatedAt             time.Time `json:"created_at"`
}

type OpenContentUrl struct {
	ID         uint   `gorm:"primaryKey" json:"-"`
	ContentURL string `gorm:"size:255" json:"content_url"`
}

func (OpenContentUrl) TableName() string { return "open_content_urls" }

// OpenContentItem is one library, video or helpful link in a mixed list, such
// as favorites or search results.
type OpenContentItem struct {
	Title                 string `json:"title"`
	Url                   string `json:"url"`
	ThumbnailUrl          string `json:"thumbnail_url"`
	Description           string `json:"description,omitempty"`
	VisibilityStatus      *bool  `json:"visibility_status"`
	OpenContentProviderId uint   `json:"open_content_provider_id"`
	ContentId             uint   `json:"content_id"`
	ContentType           string `json:"content_type"`
	ProviderName          string `json:"provider_name,omitempty"`
	ChannelTitle          string `json:"channel_title,omitempty"`
}

// OpenContentMeta is what a list adds to each library, video or link for the
// caller.
type OpenContentMeta struct {
	IsFavorited          bool `json:"is_favorited"`
	IsFeatured           bool `json:"is_featured"`
	VisibleFacilityCount int  `json:"visible_facility_count"`
}

// OpenContentParams is a bookmark to add (with a name) or remove (with a
// URL).
type OpenContentParams struct {
	Name                  string `json:"name"`
	UserID                uint   `json:"user_id"`
	ContentURL            string `json:"content_url"`
	ContentID             uint   `json:"content_id"`
	OpenContentProviderID uint   `json:"open_content_provider_id"`
}

// ContentVisits is how many visits a library or video had.
type ContentVisits struct {
	ContentID uint
	Title     string
	Visits    int64
}
