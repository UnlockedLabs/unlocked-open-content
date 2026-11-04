package models

import (
	"encoding/xml"
	"html"
)

// Feed is the Kiwix catalog the library scrape reads.
type Feed struct {
	XMLName xml.Name `xml:"feed"`
	Entries []Entry  `xml:"entry"`
}

type Entry struct {
	ID           string       `xml:"id"`
	Title        string       `xml:"title"`
	Updated      string       `xml:"updated"`
	Summary      CustomString `xml:"summary"`
	Language     string       `xml:"language"`
	Name         string       `xml:"name"`
	Flavour      string       `xml:"flavour"`
	Category     string       `xml:"category"`
	Tags         string       `xml:"tags"`
	ArticleCount int          `xml:"articleCount"`
	MediaCount   int          `xml:"mediaCount"`
	Author       Author       `xml:"author"`
	Publisher    Publisher    `xml:"publisher"`
	Links        []Link       `xml:"link"`
}

type CustomString string

func (cst *CustomString) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	var summary string
	if err := dec.DecodeElement(&summary, &start); err != nil {
		return err
	}
	*cst = CustomString(html.UnescapeString(summary))
	return nil
}

type Author struct {
	Name string `xml:"name"`
}

type Publisher struct {
	Name string `xml:"name"`
}

type Link struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr"`
}
