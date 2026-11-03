package tasks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	log "github.com/sirupsen/logrus"
)

const (
	MinImgSize = 150
)

func (ks *KiwixService) IntoLibrary(entry models.Entry, providerId uint) *models.Library {
	url, thumbnailURL := ks.ParseUrls(entry.Title, entry.Links)
	return &models.Library{
		OpenContentProviderID: providerId,
		ExternalID:            models.StringPtr(entry.ID),
		Title:                 entry.Title,
		Language:              models.StringPtr(entry.Language),
		Description:           models.StringPtr(string(entry.Summary)),
		Url:                   url,
		ThumbnailUrl:          models.StringPtr(thumbnailURL),
	}
}

func (ks *KiwixService) downloadAndHostThumbnailImg(lib, thumbnail string) (string, error) {
	imgUrl, err := url.JoinPath(ks.BaseUrl, thumbnail)
	if err != nil {
		log.Errorf("error joining URL: %v", err)
		return "", err
	}
	parsedURL, err := url.Parse(imgUrl)
	if err != nil {
		log.Errorf("error parsing imgUrl: %v", err)
		return "", err
	}
	query := parsedURL.Query()
	query.Set("size", "48")
	parsedURL.RawQuery = query.Encode()

	finalURL := parsedURL.String()
	log.Infof("downloading thumbnail image from URL: %s", finalURL)

	req, err := http.NewRequest(http.MethodGet, finalURL, nil)
	if err != nil {
		log.Errorf("error creating request: %v", err)
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "image/*")

	resp, err := ks.Client.Do(req)
	if err != nil {
		log.Errorf("error fetching thumbnail image from URL: %v", err)
		return "", err
	}
	defer func() {
		if resp.Body.Close() != nil {
			log.Errorf("error closing response body: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		log.Errorf("failed to fetch thumbnail image: received %v response", resp.Status)
		return "", fmt.Errorf("failed to fetch thumbnail image: %v", resp.Status)
	}

	imgData, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Errorf("error reading thumbnail image: %v", err)
		return "", err
	}

	var filename string
	if len(imgData) < MinImgSize {
		log.Errorf("thumbnail image %s is too small: %d bytes", lib, len(imgData))
		return "/kiwix.jpg", nil
	} else {
		filename = lib + ".png"
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	_, err = part.Write(imgData)
	if err != nil {
		return "", err
	}
	fields := map[string]string{"filename": filename, "size": fmt.Sprintf("%d", len(imgData)), "type": "image/png"}
	for key, value := range fields {
		err = writer.WriteField(key, value)
		if err != nil {
			return "", err
		}
	}

	err = writer.Close()
	if err != nil {
		log.Errorf("error closing writer: %v", err)
		return "", err
	}

	uploadURL := os.Getenv("APP_URL") + "/upload"
	req, err = http.NewRequest(http.MethodPost, uploadURL, body)
	if err != nil {
		log.Errorf("error creating upload request: %v", err)
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	uploadResp, err := ks.Client.Do(req)
	if err != nil {
		log.Errorf("error sending upload request: %v", err)
		return "", err
	}
	defer func() {
		if uploadResp.Body.Close() != nil {
			log.Errorf("error closing response body: %v", err)
		}
	}()
	if uploadResp.StatusCode != http.StatusOK {
		log.Errorf("failed to upload image: received %v response", uploadResp.Status)
		return "", fmt.Errorf("failed to upload image: %v", uploadResp.Status)
	}

	type UploadResponse struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
		Message string `json:"message"`
	}

	urlRes := &UploadResponse{}
	err = json.NewDecoder(uploadResp.Body).Decode(urlRes)
	if err != nil {
		log.Errorf("error decoding upload response: %v", err)
		return "", err
	}

	return urlRes.Data.URL, nil
}

func (ks *KiwixService) thumbnailExists(lib string) bool {
	thumbnailURL := os.Getenv("APP_URL") + "/api/photos/" + lib + ".png"
	req, err := http.NewRequest(http.MethodHead, thumbnailURL, nil)
	if err != nil {
		log.Errorf("error creating request: %v", err)
		return false
	}
	resp, err := ks.Client.Do(req)
	if err != nil {
		log.Errorf("error fetching thumbnail: %v", err)
		return false
	}
	defer func() {
		if resp.Body.Close() != nil {
			log.Errorf("error closing response body: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		log.Errorf("thumbnail does not exist: received %v response", resp.Status)
		return false
	}
	return true
}

func (ks *KiwixService) ParseUrls(externId string, links []models.Link) (string, string) {
	var url string
	var thumbnailURL string
	for _, link := range links {
		if link.Type == "text/html" {
			url = link.Href
		}
		if strings.Split(link.Type, "/")[0] == "image" || strings.Contains(link.Href, "catalog/v2/illustration") {
			if !ks.thumbnailExists(externId) {
				if thumbnail, err := ks.downloadAndHostThumbnailImg(externId, link.Href); err == nil {
					thumbnailURL = thumbnail
				} else {
					thumbnailURL = "/kiwix.jpg"
				}
			} else {
				thumbnailURL = "/api/photos/" + externId + ".png"
			}
		}
	}
	return url, thumbnailURL
}
