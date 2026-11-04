// Package models is Open Content's tables as Go types, and the shapes its
// routes read and answer with. The JSON names are the ones core sent before
// the move (EN-192), so the frontend sees no change.
package models

import (
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"gorm.io/gorm"
)

// DatabaseFields are the columns most of Open Content's tables share. The
// create and update user come from the identity.Claims in the query's context.
type DatabaseFields struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	CreateUserID *uint `json:"create_user_id"`
	UpdateUserID *uint `json:"update_user_id"`
}

func (m *DatabaseFields) BeforeCreate(tx *gorm.DB) error {
	if c, ok := identity.FromContext(tx.Statement.Context); ok {
		m.CreateUserID = &c.UserID
	}
	return nil
}

func (m *DatabaseFields) BeforeUpdate(tx *gorm.DB) error {
	if c, ok := identity.FromContext(tx.Statement.Context); ok {
		m.UpdateUserID = &c.UserID
		tx.Statement.SetColumn("update_user_id", c.UserID)
	}
	return nil
}

func FormatNullableString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func StringPtr(s string) *string {
	return &s
}

// NilIfBlank returns nil for empty or whitespace-only strings so they are
// stored as NULL.
func NilIfBlank(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
