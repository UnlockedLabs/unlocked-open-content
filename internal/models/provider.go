package models

import (
	"fmt"
	"os"
	"strings"

	"gorm.io/gorm"
)

const (
	KolibriThumbnailUrl     string = "https://learningequality.org/static/assets/kolibri-ecosystem-logos/blob-logo.svg"
	Kiwix                   string = "Kiwix"
	KolibriDescription      string = "Kolibri provides an extensive library of educational content suitable for all learning levels."
	KiwixThumbnailURL       string = "/kiwix.jpg"
	KiwixDescription        string = "Kiwix is an offline reader that allows you to host a wide array of educational content."
	YoutubeThumbnail        string = "/youtube.png"
	Youtube                 string = "Youtube"
	YoutubeApi              string = "https://www.googleapis.com/youtube/v3/videos"
	YoutubeDescription      string = "Hand pick videos to be available to students from youtube URL's"
	HelpfulLinks            string = "HelpfulLinks"
	HelpfulLinksThumbnail   string = "/ul-logo-d.svg"
	HelpfulLinksUrl         string = ""
	HelpfulLinksDescription string = "Hand picked helpful links for users"
)

var KiwixLibraryUrl string = os.Getenv("KIWIX_SERVER_URL")

type OpenContentProvider struct {
	DatabaseFields
	Title            *string `gorm:"size:255"  json:"title"`
	Url              string  `gorm:"size:255;not null" json:"url"`
	ThumbnailUrl     *string `json:"thumbnail_url"`
	CurrentlyEnabled bool    `json:"currently_enabled"`
	Description      *string `json:"description"`

	Videos []Video `gorm:"foreignKey:OpenContentProviderID" json:"-"`
}

func (cp *OpenContentProvider) BeforeCreate(tx *gorm.DB) error {
	if err := cp.DatabaseFields.BeforeCreate(tx); err != nil {
		return err
	}
	if FormatNullableString(cp.Title) == Youtube && cp.Url == "" {
		cp.Url = YoutubeApi
	}
	if cp.Url != "" && !strings.HasPrefix(cp.Url, "http") {
		cp.Url = fmt.Sprintf("https://%s", cp.Url)
	}
	return nil
}

func (OpenContentProvider) TableName() string { return "open_content_providers" }
