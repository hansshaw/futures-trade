package journal

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	bolt "go.etcd.io/bbolt"
	_ "golang.org/x/image/webp"
)

const MaxAssetBytes = 4 * 1024 * 1024
const MaxImagePixels = 24_000_000

type assetRecord struct {
	Meta  Asset  `json:"meta"`
	Bytes []byte `json:"bytes"`
}

func validateImage(name, dataURL string) (assetRecord, error) {
	if len(dataURL) > base64.StdEncoding.EncodedLen(MaxAssetBytes)+64 {
		return assetRecord{}, errors.New("每张截图最多4MiB")
	}
	header, encoded, ok := strings.Cut(dataURL, ",")
	mime := ""
	for _, m := range []string{"image/png", "image/jpeg", "image/webp"} {
		if header == "data:"+m+";base64" {
			mime = m
		}
	}
	if !ok || mime == "" {
		return assetRecord{}, errors.New("截图仅支持PNG、JPEG或WebP的base64 data URL")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > MaxAssetBytes {
		return assetRecord{}, errors.New("截图编码无效或超过4MiB")
	}
	magic := ""
	switch {
	case bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")):
		magic = "image/png"
	case len(raw) > 3 && raw[0] == 0xff && raw[1] == 0xd8 && raw[2] == 0xff:
		magic = "image/jpeg"
	case len(raw) >= 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WEBP":
		magic = "image/webp"
	}
	if mime != magic {
		return assetRecord{}, errors.New("截图文件内容与声明类型不一致")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxImagePixels/cfg.Height {
		return assetRecord{}, errors.New("无法识别截图或像素超过2400万")
	}
	if "image/"+format != mime {
		return assetRecord{}, errors.New("截图格式不一致")
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil || decoded.Bounds().Dx() != cfg.Width || decoded.Bounds().Dy() != cfg.Height {
		return assetRecord{}, errors.New("截图已截断或无法完整解码")
	}
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if name == "" || name == "." {
		name = "截图"
	}
	if len(name) > 240 || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n") {
		return assetRecord{}, errors.New("截图名称无效或过长")
	}
	id, err := newID()
	if err != nil {
		return assetRecord{}, err
	}
	return assetRecord{Meta: Asset{ID: id, Name: name, MIME: mime, Size: len(raw), Width: cfg.Width, Height: cfg.Height, CreatedAt: time.Now().UTC()}, Bytes: raw}, nil
}

func (s *Service) AddAsset(name, dataURL string) (Asset, error) {
	select {
	case s.assetSlots <- struct{}{}:
		defer func() { <-s.assetSlots }()
	default:
		return Asset{}, ErrBusy
	}
	record, err := validateImage(name, dataURL)
	if err != nil {
		return Asset{}, err
	}
	err = s.db.Update(func(tx *bolt.Tx) error { return put(tx, "assets", record.Meta.ID, record) })
	return record.Meta, err
}

func (s *Service) Asset(id string) (Asset, error) {
	var record assetRecord
	err := s.db.View(func(tx *bolt.Tx) error { return get(tx, "assets", id, &record) })
	if err != nil {
		return Asset{}, err
	}
	record.Meta.DataURL = "data:" + record.Meta.MIME + ";base64," + base64.StdEncoding.EncodeToString(record.Bytes)
	return record.Meta, nil
}
