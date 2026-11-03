package models

import (
	"context"
	"math"
	"net/url"
	"slices"

	"github.com/UnlockedLabs/unlocked-contracts/features"
)

type PaginationMeta struct {
	CurrentPage int   `json:"current_page"`
	LastPage    int   `json:"last_page"`
	PerPage     int   `json:"per_page"`
	Total       int64 `json:"total"`
}

func NewPaginationInfo(currentPage, perPage int, total int64) PaginationMeta {
	lastPage := int(total) / perPage
	if int(total)%perPage > 0 {
		lastPage++
	}
	return PaginationMeta{
		CurrentPage: currentPage,
		LastPage:    lastPage,
		PerPage:     perPage,
		Total:       total,
	}
}

// QueryContext is a list request's paging, filters and caller.
type QueryContext struct {
	Params        url.Values
	Page          int
	PerPage       int
	FacilityID    uint
	UserID        uint
	OrderBy       string
	Order         string
	IsAdmin       bool
	Search        string
	Tags          []string
	Total         int64
	All           bool
	Ctx           context.Context
	FeatureAccess []features.Access
}

func (q *QueryContext) HasFeature(feature features.Access) bool {
	return slices.Contains(q.FeatureAccess, feature)
}

func (q QueryContext) IntoMeta() PaginationMeta {
	return NewPaginationInfo(q.Page, q.PerPage, q.Total)
}

func (q QueryContext) CalcOffset() int {
	return int(math.Abs(float64((q.Page - 1) * q.PerPage)))
}

// OrderClause is the request's order, or fallback when it has none.
func (q QueryContext) OrderClause(fallback string) string {
	val := q.OrderBy + " " + q.Order
	if val == " " || q.OrderBy == "" {
		return fallback
	}
	return val
}

func (q QueryContext) SearchQuery() string {
	if q.Search == "" {
		return ""
	}
	return "%" + q.Search + "%"
}
