package models

type Library struct {
	DatabaseFields
	OpenContentProviderID uint    `gorm:"not null" json:"open_content_provider_id"`
	ExternalID            *string `json:"external_id"`
	Title                 string  `gorm:"size:255;not null" json:"title"`
	Language              *string `gorm:"size:255" json:"language"`
	Description           *string `json:"description"`
	Url                   string  `gorm:"not null" json:"url"`
	ThumbnailUrl          *string `json:"thumbnail_url"`
	VisibilityStatus      bool    `gorm:"->" json:"visibility_status"`

	OpenContentProvider *OpenContentProvider  `gorm:"foreignKey:OpenContentProviderID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL" json:"open_content_provider"`
	Favorites           []OpenContentFavorite `gorm:"-" json:"favorites"`
}

func (Library) TableName() string { return "libraries" }

func (lib *Library) GetFacilityVisibilityStatus(facilityID uint) *FacilityVisibilityStatus {
	visibilityStatus := FacilityVisibilityStatus{
		FacilityID:            facilityID,
		OpenContentProviderID: lib.OpenContentProviderID,
		VisibilityStatus:      lib.VisibilityStatus,
		ContentID:             lib.ID,
	}
	return &visibilityStatus
}

func (lib *Library) IntoProxyPO() *LibraryProxyPO {
	proxyParams := LibraryProxyPO{
		ID:                    lib.ID,
		Path:                  lib.Url,
		BaseUrl:               lib.OpenContentProvider.Url,
		OpenContentProviderID: lib.OpenContentProvider.ID,
		VisibilityStatus:      lib.VisibilityStatus,
	}
	return &proxyParams
}

// LibraryProxyPO is what the library proxy needs to forward a request to
// Kiwix. It is cached in the library_paths bucket.
type LibraryProxyPO struct {
	ID                    uint
	OpenContentProviderID uint
	Path                  string
	BaseUrl               string
	VisibilityStatus      bool
}

type LibraryResponse struct {
	Library
	OpenContentMeta
	Tags []string `json:"tags" gorm:"-"`
}
