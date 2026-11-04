package models

import (
	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"gorm.io/gorm"
)

type FacilityVisibilityStatus struct {
	FacilityID            uint  `gorm:"primaryKey" json:"facility_id"`
	OpenContentProviderID uint  `gorm:"primaryKey" json:"open_content_provider_id"`
	ContentID             uint  `gorm:"primaryKey" json:"content_id"`
	VisibilityStatus      bool  `gorm:"default:false;not null" json:"visibility_status"`
	CreateUserID          *uint `json:"create_user_id"`
	UpdateUserID          *uint `json:"update_user_id"`

	OpenContentProvider *OpenContentProvider `gorm:"foreignKey:OpenContentProviderID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL" json:"open_content_provider"`
}

func (FacilityVisibilityStatus) TableName() string { return "facility_visibility_statuses" }

func (f *FacilityVisibilityStatus) BeforeCreate(tx *gorm.DB) error {
	if c, ok := identity.FromContext(tx.Statement.Context); ok {
		f.CreateUserID = &c.UserID
		tx.Statement.SetColumn("create_user_id", c.UserID)
	}
	return nil
}

func (f *FacilityVisibilityStatus) BeforeUpdate(tx *gorm.DB) error {
	if c, ok := identity.FromContext(tx.Statement.Context); ok {
		f.UpdateUserID = &c.UserID
		tx.Statement.SetColumn("update_user_id", c.UserID)
	}
	return nil
}

// ContentFacilityVisibility is whether one facility shows a library, video or
// link.
type ContentFacilityVisibility struct {
	FacilityID       uint   `json:"facility_id"`
	FacilityName     string `json:"facility_name"`
	VisibilityStatus bool   `json:"visibility_status"`
}
